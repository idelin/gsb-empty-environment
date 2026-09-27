"""Synthetic tax-rate version recalculation engine.

All rules are SYNTHETIC and for demonstration only; this module does not
provide real tax advice.

Public rounding definition (stable, contractual):
    Each invoice line's tax is computed independently as
        round_half_even(net_cents * rate)
    using exact rational arithmetic (fractions.Fraction). The invoice total
    tax is the SUM of the per-line taxes. Rounding the aggregated base is a
    DIFFERENT, non-equivalent operation and is intentionally not used.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import sys
from dataclasses import dataclass, field
from datetime import date
from fractions import Fraction
from typing import Optional


# ---------------------------------------------------------------------------
# Rounding
# ---------------------------------------------------------------------------

def round_half_even(value: Fraction) -> int:
    """Round an exact rational to the nearest integer, ties to even."""
    floor = value.numerator // value.denominator
    rem = value - floor  # 0 <= rem < 1
    twice = rem * 2
    if twice < 1:
        return floor
    if twice > 1:
        return floor + 1
    return floor if floor % 2 == 0 else floor + 1


ROUNDING_POLICIES = {"half_even": round_half_even}


# ---------------------------------------------------------------------------
# Domain model
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class TaxRate:
    """A synthetic tax rate version for one jurisdiction and interval."""
    version: str
    jurisdiction: str
    rate: Fraction
    valid_from: date
    valid_to: Optional[date] = None  # None = open-ended

    def covers(self, jurisdiction: str, on: date) -> bool:
        if self.jurisdiction != jurisdiction:
            return False
        if on < self.valid_from:
            return False
        return self.valid_to is None or on < self.valid_to


class RateTable:
    """Immutable collection of rate versions with lookup by jurisdiction+date."""

    def __init__(self, rates: list[TaxRate]):
        self._rates = list(rates)

    def match(self, jurisdiction: str, on: date, version: Optional[str] = None) -> TaxRate:
        candidates = [r for r in self._rates if r.covers(jurisdiction, on)]
        if version is not None:
            candidates = [r for r in candidates if r.version == version]
        if not candidates:
            raise LookupError(
                f"no synthetic rate for {jurisdiction!r} on {on.isoformat()}"
                + (f" (version {version!r})" if version else "")
            )
        # Latest valid_from wins among overlapping candidates.
        return max(candidates, key=lambda r: r.valid_from)


@dataclass(frozen=True)
class InvoiceLine:
    description: str
    net_cents: int


@dataclass(frozen=True)
class Invoice:
    """An issued invoice. Immutable once issued."""
    invoice_id: str
    jurisdiction: str
    issued_on: date
    lines: tuple[InvoiceLine, ...]
    rate_version: str
    rounding: str = "half_even"
    kind: str = "original"          # original | reversal | reissue
    references: Optional[str] = None  # invoice id this document refers to


@dataclass(frozen=True)
class ComputedLine:
    description: str
    net_cents: int
    tax_cents: int


@dataclass(frozen=True)
class ComputedInvoice:
    invoice: Invoice
    lines: tuple[ComputedLine, ...]
    total_net_cents: int
    total_tax_cents: int


@dataclass(frozen=True)
class Revision:
    """A late-arriving tax-rate revision (synthetic)."""
    revision_id: str
    new_rate_version: str


# ---------------------------------------------------------------------------
# Computation
# ---------------------------------------------------------------------------

def compute_invoice(invoice: Invoice, table: RateTable) -> ComputedInvoice:
    """Compute per-line taxes and the invoice total (sum of line taxes)."""
    try:
        rounder = ROUNDING_POLICIES[invoice.rounding]
    except KeyError:
        raise ValueError(f"unknown rounding policy: {invoice.rounding!r}") from None
    rate = table.match(invoice.jurisdiction, invoice.issued_on, invoice.rate_version)
    computed = tuple(
        ComputedLine(
            description=line.description,
            net_cents=line.net_cents,
            tax_cents=rounder(Fraction(line.net_cents) * rate.rate),
        )
        for line in invoice.lines
    )
    return ComputedInvoice(
        invoice=invoice,
        lines=computed,
        total_net_cents=sum(c.net_cents for c in computed),
        total_tax_cents=sum(c.tax_cents for c in computed),
    )


# ---------------------------------------------------------------------------
# Revision engine: reversal + reissue, idempotent per revision id
# ---------------------------------------------------------------------------

class RevisionEngine:
    """Applies late rate revisions to issued invoices.

    The original invoice is never modified. Each revision produces a reversal
    document (negated lines, referencing the original) and a reissued invoice
    computed with the new rate version. Applying the same revision id twice
    is a no-op replay: it returns the previously produced documents without
    creating new reversals.
    """

    def __init__(self, table: RateTable):
        self._table = table
        self._applied: dict[tuple[str, str], tuple[Invoice, Invoice]] = {}

    def apply(self, invoice: Invoice, revision: Revision) -> tuple[Invoice, Invoice, bool]:
        """Returns (reversal, reissue, created). created=False on replay."""
        key = (invoice.invoice_id, revision.revision_id)
        if key in self._applied:
            reversal, reissue = self._applied[key]
            return reversal, reissue, False

        reversal = Invoice(
            invoice_id=f"{invoice.invoice_id}-RV-{revision.revision_id}",
            jurisdiction=invoice.jurisdiction,
            issued_on=invoice.issued_on,
            lines=tuple(
                InvoiceLine(description=l.description, net_cents=-l.net_cents)
                for l in invoice.lines
            ),
            rate_version=invoice.rate_version,
            rounding=invoice.rounding,
            kind="reversal",
            references=invoice.invoice_id,
        )
        reissue = Invoice(
            invoice_id=f"{invoice.invoice_id}-RI-{revision.revision_id}",
            jurisdiction=invoice.jurisdiction,
            issued_on=invoice.issued_on,
            lines=invoice.lines,
            rate_version=revision.new_rate_version,
            rounding=invoice.rounding,
            kind="reissue",
            references=invoice.invoice_id,
        )
        self._applied[key] = (reversal, reissue)
        return reversal, reissue, True


# ---------------------------------------------------------------------------
# Async recalculation service
# ---------------------------------------------------------------------------

class RecalculationService:
    """Async worker: revisions are queued and applied asynchronously."""

    def __init__(self, engine: RevisionEngine):
        self.engine = engine
        self._queue: asyncio.Queue[tuple[Invoice, Revision]] = asyncio.Queue()
        self.results: list[dict] = []

    async def submit(self, invoice: Invoice, revision: Revision) -> None:
        await self._queue.put((invoice, revision))

    async def run(self) -> list[dict]:
        worker = asyncio.create_task(self._worker())
        await self._queue.join()
        worker.cancel()
        return self.results

    async def _worker(self) -> None:
        while True:
            invoice, revision = await self._queue.get()
            try:
                reversal, reissue, created = self.engine.apply(invoice, revision)
                self.results.append({
                    "invoice_id": invoice.invoice_id,
                    "revision_id": revision.revision_id,
                    "created": created,
                    "reversal": invoice_to_dict(compute_invoice(reversal, self.engine._table)),
                    "reissue": invoice_to_dict(compute_invoice(reissue, self.engine._table)),
                })
            finally:
                self._queue.task_done()


# ---------------------------------------------------------------------------
# JSON (de)serialization
# ---------------------------------------------------------------------------

def parse_date(s: str) -> date:
    return date.fromisoformat(s)


def rate_from_dict(d: dict) -> TaxRate:
    return TaxRate(
        version=d["version"],
        jurisdiction=d["jurisdiction"],
        rate=Fraction(d["numerator"], d["denominator"]),
        valid_from=parse_date(d["valid_from"]),
        valid_to=parse_date(d["valid_to"]) if d.get("valid_to") else None,
    )


def invoice_from_dict(d: dict) -> Invoice:
    return Invoice(
        invoice_id=d["id"],
        jurisdiction=d["jurisdiction"],
        issued_on=parse_date(d["issued_on"]),
        lines=tuple(
            InvoiceLine(description=l.get("description", ""), net_cents=int(l["net_cents"]))
            for l in d["lines"]
        ),
        rate_version=d["rate_version"],
        rounding=d.get("rounding", "half_even"),
    )


def invoice_to_dict(c: ComputedInvoice) -> dict:
    inv = c.invoice
    return {
        "id": inv.invoice_id,
        "kind": inv.kind,
        "references": inv.references,
        "jurisdiction": inv.jurisdiction,
        "issued_on": inv.issued_on.isoformat(),
        "rate_version": inv.rate_version,
        "rounding": inv.rounding,
        "lines": [
            {"description": l.description, "net_cents": l.net_cents, "tax_cents": l.tax_cents}
            for l in c.lines
        ],
        "total_net_cents": c.total_net_cents,
        "total_tax_cents": c.total_tax_cents,
    }


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _load(path: str) -> dict:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def cmd_compute(args: argparse.Namespace) -> None:
    data = _load(args.input)
    table = RateTable([rate_from_dict(r) for r in data["rates"]])
    invoice = invoice_from_dict(data["invoice"])
    json.dump(invoice_to_dict(compute_invoice(invoice, table)), sys.stdout, indent=2)
    sys.stdout.write("\n")


def cmd_revise(args: argparse.Namespace) -> None:
    data = _load(args.input)
    table = RateTable([rate_from_dict(r) for r in data["rates"]])
    invoice = invoice_from_dict(data["invoice"])
    engine = RevisionEngine(table)
    service = RecalculationService(engine)

    async def main() -> list[dict]:
        for rev in data["revisions"]:
            await service.submit(invoice, Revision(
                revision_id=rev["id"], new_rate_version=rev["new_rate_version"],
            ))
        return await service.run()

    results = asyncio.run(main())
    json.dump({"documents": results}, sys.stdout, indent=2)
    sys.stdout.write("\n")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="taxcalc",
        description="Synthetic tax-rate version recalculation (no real tax advice).",
    )
    sub = parser.add_subparsers(dest="command", required=True)
    for name, fn, helptext in (
        ("compute", cmd_compute, "compute per-line and total tax for one invoice"),
        ("revise", cmd_revise, "apply late rate revisions (async reversal + reissue)"),
    ):
        p = sub.add_parser(name, help=helptext)
        p.add_argument("input", help="path to a JSON input file")
        p.set_defaults(func=fn)
    return parser


def main(argv: Optional[list[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    args.func(args)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

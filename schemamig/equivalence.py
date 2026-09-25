"""Migration-path consistency: are two paths value-equivalent?

The type domain is finite, so we enumerate representative sample values
per type (including null and NaN, which are distinct) and search small
batches for a counterexample: an input batch on which the two migration
paths produce observably different results (different values, or one
raising while the other succeeds).
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass
from decimal import Decimal
from typing import Optional

from .batch import Batch
from .migration import Migration
from .schema import Schema
from .typesys import Type

SAMPLES = {
    Type.INT: (0, 1, -1, 2 ** 63 + 1, -(2 ** 63 + 1)),
    Type.DECIMAL: (Decimal("0"), Decimal("1.5"), Decimal("-2.25"),
                   Decimal("NaN"), Decimal("1E+30")),
    Type.STRING: ("", "0", "-3", "1.5", "abc"),
    Type.BOOL: (True, False),
}


class SearchSpaceTooLarge(Exception):
    pass


@dataclass
class Counterexample:
    reason: str
    batch: Optional[Batch]
    left: object    # Batch or Exception
    right: object   # Batch or Exception


def _domain(column):
    values = list(SAMPLES[column.type])
    if column.nullable:
        values.append(None)
    return values


def _outcome(mig: Migration, schema: Schema, batch: Batch):
    try:
        projected, _diagnostics = mig.project(schema, batch)
        return projected
    except Exception as exc:  # a conversion failure is an observable outcome
        return exc


def _same_outcome(a, b) -> bool:
    if isinstance(a, Batch) and isinstance(b, Batch):
        return a == b
    if isinstance(a, Exception) and isinstance(b, Exception):
        return type(a) is type(b)
    return False


def find_counterexample(schema: Schema, left: Migration, right: Migration,
                        max_rows: int = 2, cap: int = 100_000):
    """Return a Counterexample if the two paths differ, else None."""
    plan_left = left.apply(schema)
    plan_right = right.apply(schema)
    if plan_left.schema != plan_right.schema:
        return Counterexample("target schemas differ", None,
                              plan_left.schema, plan_right.schema)
    domains = [_domain(c) for c in schema.columns]
    row_space = list(itertools.product(*domains))
    total = sum(len(row_space) ** n for n in range(max_rows + 1))
    if total > cap:
        raise SearchSpaceTooLarge(
            f"{total} candidate batches exceed cap {cap}")
    for nrows in range(max_rows + 1):
        for rows in itertools.product(row_space, repeat=nrows):
            batch = Batch.from_rows(schema, rows)
            out_left = _outcome(left, schema, batch)
            out_right = _outcome(right, schema, batch)
            if not _same_outcome(out_left, out_right):
                return Counterexample("value mismatch", batch,
                                      out_left, out_right)
    return None

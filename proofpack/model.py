"""Domain model: facts (records), integer-range filter rules, aggregates.

Every fact, rule, and (in prover.py) computation step carries a stable ID
and a version. Weighted average is exact (Fraction); a zero total weight
is reported as UNDEFINED, never as a number.
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass
from fractions import Fraction

UNDEFINED = "UNDEFINED"


@dataclass(frozen=True)
class Record:
    """A fact. `rid` + `fact_version` form its stable identity."""

    rid: str
    fact_version: int
    key: int
    value: int
    weight: int

    def to_fact(self) -> dict:
        return {
            "rid": self.rid,
            "fact_version": self.fact_version,
            "key": self.key,
            "value": self.value,
            "weight": self.weight,
        }

    @staticmethod
    def from_fact(fact: dict) -> "Record":
        return Record(
            rid=fact["rid"],
            fact_version=fact["fact_version"],
            key=fact["key"],
            value=fact["value"],
            weight=fact["weight"],
        )


@dataclass(frozen=True)
class FilterRule:
    """Integer-interval filter: field in [lo, hi]."""

    rule_id: str
    version: int
    field: str
    lo: int
    hi: int

    def matches(self, record: Record) -> bool:
        return self.lo <= getattr(record, self.field) <= self.hi

    def to_dict(self) -> dict:
        return {
            "rule_id": self.rule_id,
            "version": self.version,
            "field": self.field,
            "lo": self.lo,
            "hi": self.hi,
        }

    @staticmethod
    def from_dict(d: dict) -> "FilterRule":
        return FilterRule(
            rule_id=d["rule_id"], version=d["version"],
            field=d["field"], lo=d["lo"], hi=d["hi"],
        )


def aggregate(records: list[Record]) -> dict:
    """Sum, count, and weighted average (numerator/denominator kept explicit)."""
    total = sum(r.value for r in records)
    count = len(records)
    numerator = sum(r.value * r.weight for r in records)
    denominator = sum(r.weight for r in records)
    wavg = UNDEFINED if denominator == 0 else str(Fraction(numerator, denominator))
    return {
        "sum": total,
        "count": count,
        "wavg_numerator": numerator,
        "wavg_denominator": denominator,
        "wavg": wavg,
    }


def contribution_summary(leaf_hexes: list[str]) -> dict:
    """Rolling digest over the ordered contribution list (count + digest)."""
    h = hashlib.sha256(b"proofpack/contrib-init/v1").hexdigest()
    for leaf in leaf_hexes:
        h = hashlib.sha256(
            b"proofpack/contrib/v1:" + h.encode() + b":" + leaf.encode()
        ).hexdigest()
    return {"count": len(leaf_hexes), "digest": h}

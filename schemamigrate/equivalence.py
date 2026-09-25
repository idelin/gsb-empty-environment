"""Migration-path equivalence over a finite generated domain.

For each column type we enumerate a small set of sample values (plus NULL
for nullable columns) and build candidate batches from their cartesian
product (row-capped). Two migration paths from the same source schema are
value-equivalent iff every candidate batch projects to the same output
under both paths; the first differing batch is returned as a
counterexample.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass
from decimal import Decimal

from .batch import Batch
from .types import NULL, TypeTag, values_equal

SAMPLE_VALUES = {
    TypeTag.INT: (0, 1, -1, 2**63, -(2**63)),
    TypeTag.DECIMAL: (Decimal("0"), Decimal("1.5"), Decimal("-2.5"), Decimal("0.1")),
    TypeTag.FLOAT: (0.0, -1.5, float("nan"), float("inf")),
    TypeTag.STRING: ("", "a", "hello"),
    TypeTag.BOOL: (True, False),
}


def sample_values(type_tag, nullable):
    values = list(SAMPLE_VALUES[type_tag])
    if nullable:
        values.append(NULL)
    return values


def candidate_batches(schema, row_cap=256):
    empty = Batch(schema, {c.name: () for c in schema.columns})
    if not schema.columns:
        return [empty]
    per_column = [sample_values(c.type, c.nullable) for c in schema.columns]
    rows = list(itertools.islice(itertools.product(*per_column), row_cap))
    data = {c.name: [row[i] for row in rows] for i, c in enumerate(schema.columns)}
    return [empty, Batch(schema, data)]


@dataclass
class EquivalenceReport:
    equivalent: bool
    detail: str
    counterexample: object = None
    left_output: object = None
    right_output: object = None


def _first_difference(left, right):
    for col in left.schema.columns:
        lv = left.column_values(col.name)
        rv = right.column_values(col.name)
        for i, (a, b) in enumerate(zip(lv, rv)):
            if not values_equal(a, b):
                return f"row {i}, column {col.name!r}: {a!r} != {b!r}"
    return "batches differ"


def check_path_equivalence(left_migration, right_migration, row_cap=256):
    if left_migration.source_schema != right_migration.source_schema:
        raise ValueError("migrations start from different source schemas")
    source = left_migration.source_schema
    if left_migration.target_schema != right_migration.target_schema:
        return EquivalenceReport(
            False,
            f"target schemas differ: {left_migration.target_schema!r} "
            f"vs {right_migration.target_schema!r}",
        )
    checked = 0
    for batch in candidate_batches(source, row_cap=row_cap):
        left_out, _ = left_migration.project(batch)
        right_out, _ = right_migration.project(batch)
        checked += 1
        if left_out != right_out:
            return EquivalenceReport(
                False,
                _first_difference(left_out, right_out),
                counterexample=batch,
                left_output=left_out,
                right_output=right_out,
            )
    return EquivalenceReport(
        True,
        f"value-equivalent over {checked} generated batches (row_cap={row_cap})",
    )

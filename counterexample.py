"""Bounded exhaustive search for minimal counterexamples between two plans.

A *counterexample* is a database instance (over the finite value domain) on
which the two plans produce different result *multisets*. Row order is
ignored; differing duplicate counts count as a difference.

Databases are enumerated in increasing total row count, so the first
counterexample found is minimal with respect to total database size.

IMPORTANT: if no counterexample is found within the budget, that is NOT a
proof of equivalence -- only a statement about the searched space.
"""

from __future__ import annotations

from collections import Counter
from dataclasses import dataclass, field
from itertools import combinations_with_replacement, product
from typing import Iterator, Optional

from relalg import NULL, Plan, Relation, evaluate


DEFAULT_DOMAIN = (0, 1, NULL)


def _compositions(total: int, parts: int, cap: int) -> Iterator[tuple[int, ...]]:
    """Yield all `parts`-tuples of ints in [0, cap] summing to `total`."""
    if parts == 1:
        if total <= cap:
            yield (total,)
        return
    for first in range(min(cap, total) + 1):
        for rest in _compositions(total - first, parts - 1, cap):
            yield (first,) + rest


def iter_databases(
    schemas: dict[str, tuple[str, ...]],
    domain: tuple = DEFAULT_DOMAIN,
    max_rows: int = 2,
) -> Iterator[dict[str, Relation]]:
    """Yield every database instance with at most `max_rows` rows per relation,
    in increasing total row count.

    Relation instances are multisets of rows drawn (with repetition) from the
    finite row space ``domain ** arity``.
    """
    names = list(schemas)
    row_space = {
        n: list(product(domain, repeat=len(schemas[n]))) for n in names
    }
    for total in range(0, max_rows * len(names) + 1):
        for sizes in _compositions(total, len(names), max_rows):
            choices = [
                combinations_with_replacement(row_space[n], k)
                for n, k in zip(names, sizes)
            ]
            for combo in product(*choices):
                yield {
                    n: Relation(schemas[n], tuple(rows))
                    for n, rows in zip(names, combo)
                }


@dataclass
class SearchResult:
    found: bool                       # a counterexample was found
    completed: bool                   # the whole budget was searched
    checked: int                      # number of database instances evaluated
    max_rows: int
    domain: tuple
    db: Optional[dict[str, Relation]] = None
    output_a: Optional[Relation] = None
    output_b: Optional[Relation] = None

    @property
    def proves_equivalence(self) -> bool:
        # Finding nothing within a finite budget never proves equivalence.
        return False


def find_counterexample(
    plan_a: Plan,
    plan_b: Plan,
    schemas: dict[str, tuple[str, ...]],
    domain: tuple = DEFAULT_DOMAIN,
    max_rows: int = 2,
    max_dbs: Optional[int] = None,
) -> SearchResult:
    """Search for the smallest database on which the plans' result
    multisets differ. Minimality is by total row count across relations."""
    empty = {n: Relation(s, ()) for n, s in schemas.items()}
    schema_a = evaluate(plan_a, empty).schema
    schema_b = evaluate(plan_b, empty).schema
    if schema_a != schema_b:
        raise ValueError(
            f"output schemas differ: {schema_a} vs {schema_b}; "
            "the plans are trivially distinguishable"
        )

    checked = 0
    for db in iter_databases(schemas, domain, max_rows):
        checked += 1
        if max_dbs is not None and checked > max_dbs:
            return SearchResult(
                found=False, completed=False, checked=checked - 1,
                max_rows=max_rows, domain=domain,
            )
        out_a = evaluate(plan_a, db)
        out_b = evaluate(plan_b, db)
        if Counter(out_a.rows) != Counter(out_b.rows):
            return SearchResult(
                found=True, completed=False, checked=checked,
                max_rows=max_rows, domain=domain,
                db=db, output_a=out_a, output_b=out_b,
            )
    return SearchResult(
        found=False, completed=True, checked=checked,
        max_rows=max_rows, domain=domain,
    )

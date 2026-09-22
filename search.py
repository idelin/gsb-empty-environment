"""Counterexample search: compare two plans as multisets over generated
inputs, and shrink any counterexample found to a (1-)minimal one.

Result semantics:
- Rows are compared as multisets (collections.Counter). Order is ignored
  (no ORDER BY); differing multiplicities of a row ARE a difference.
- If the budget (or the whole bounded space) is exhausted without a
  mismatch, that is NOT a proof of equivalence -- only a bounded check.
"""
from __future__ import annotations

import time
from collections import Counter
from dataclasses import dataclass, field

from generate import database_size, enum_databases
from relalg import evaluate


def results_equal(plan1, plan2, catalog, db):
    """True iff both plans produce the same schema and the same multiset
    of rows on db (order-insensitive, multiplicity-sensitive)."""
    schema1, rows1 = evaluate(plan1, catalog, db)
    schema2, rows2 = evaluate(plan2, catalog, db)
    return schema1 == schema2 and Counter(rows1) == Counter(rows2)


def is_counterexample(plan1, plan2, catalog, db):
    return not results_equal(plan1, plan2, catalog, db)


def minimize(plan1, plan2, catalog, db):
    """Greedily delete rows while the database remains a counterexample.

    Returns a 1-minimal counterexample: removing any single remaining row
    makes the two plans agree. Rows are also canonicalized (sorted) per
    table for stable output.
    """
    db = {name: sorted(rows, key=repr) for name, rows in db.items()}
    changed = True
    while changed:
        changed = False
        for name in db:
            i = 0
            while i < len(db[name]):
                candidate = db[name][:i] + db[name][i + 1:]
                trial = dict(db, **{name: candidate})
                if is_counterexample(plan1, plan2, catalog, trial):
                    db[name] = candidate
                    changed = True
                else:
                    i += 1
    return db


@dataclass
class SearchResult:
    # "counterexample" | "space_exhausted" | "budget_exhausted"
    status: str
    checked: int
    counterexample: dict | None = None
    outputs: tuple | None = None  # (rows1, rows2) on the counterexample
    note: str = ""

    @property
    def found(self):
        return self.status == "counterexample"


def find_counterexample(plan1, plan2, catalog, domain, max_rows,
                        budget=100_000, time_budget=None, do_minimize=True):
    """Enumerate databases and look for a multiset mismatch.

    budget: max number of databases to evaluate.
    time_budget: optional wall-clock limit in seconds.
    """
    checked = 0
    start = time.monotonic()
    for db in enum_databases(catalog, domain, max_rows):
        if checked >= budget:
            return SearchResult("budget_exhausted", checked,
                                note=f"stopped after budget of {budget} databases")
        if time_budget is not None and time.monotonic() - start > time_budget:
            return SearchResult("budget_exhausted", checked,
                                note=f"stopped after {time_budget}s time budget")
        checked += 1
        if is_counterexample(plan1, plan2, catalog, db):
            ce = minimize(plan1, plan2, catalog, db) if do_minimize else db
            _, rows1 = evaluate(plan1, catalog, ce)
            _, rows2 = evaluate(plan2, catalog, ce)
            return SearchResult("counterexample", checked, ce, (rows1, rows2))
    return SearchResult("space_exhausted", checked,
                        note=f"entire bounded space (max_rows={max_rows}, "
                             f"domain size={len(domain)}) checked")

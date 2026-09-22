"""Demo: hunt for minimal counterexamples between two query plans.

Run:  python3 cli.py
"""
from __future__ import annotations

from collections import Counter

from relalg import NULL, Col, Cmp, Join, LeftJoin, Lit, Project, Scan, Select
from search import find_counterexample

CATALOG = {"R": ["a"], "S": ["c", "b"]}
DOMAIN = [0, 1, NULL]
MAX_ROWS = 2


def fmt_row(row):
    return "(" + ", ".join("NULL" if v is NULL else repr(v) for v in row) + ")"


def fmt_bag(rows):
    counts = Counter(rows)
    if not counts:
        return "{ }"
    parts = []
    for row in sorted(counts, key=repr):
        n = counts[row]
        parts.append(fmt_row(row) + (f" x{n}" if n > 1 else ""))
    return "{ " + ", ".join(parts) + " " + "}"


def show_db(db):
    for name, rows in db.items():
        print(f"  {name} = {fmt_bag(rows)}")


def report(title, plan1, plan2, budget):
    print("=" * 68)
    print(title)
    print("=" * 68)
    result = find_counterexample(plan1, plan2, CATALOG, DOMAIN,
                                 MAX_ROWS, budget=budget)
    print(f"databases checked: {result.checked}")
    if result.found:
        print("STATUS: COUNTEREXAMPLE FOUND (plans are NOT equivalent)")
        print("minimal counterexample input (removing any row makes plans agree):")
        show_db(result.counterexample)
        rows1, rows2 = result.outputs
        print(f"  plan 1 -> {fmt_bag(rows1)}")
        print(f"  plan 2 -> {fmt_bag(rows2)}")
    else:
        print(f"STATUS: NO counterexample found ({result.status})")
        print(f"  {result.note}")
        print("  NOTE: this is only a bounded check, NOT a proof of equivalence.")
    print()


def main():
    # Pair 1: pushing a filter on the right side THROUGH a left outer join
    # is not valid in general (NULL padding / three-valued logic).
    pred = Cmp(">", Col("b"), Lit(0))
    cond = Cmp("=", Col("a"), Col("c"))
    plan1 = Select(pred, LeftJoin(cond, Scan("R"), Scan("S")))
    plan2 = LeftJoin(cond, Scan("R"), Select(pred, Scan("S")))
    report("Pair 1:  sigma_{b>0}(R LOJ S)   vs.   R LOJ sigma_{b>0}(S)",
           plan1, plan2, budget=100_000)

    # Pair 2: pushing a left-only filter through an INNER join is valid;
    # the bounded search should exhaust the space without a mismatch.
    pred2 = Cmp(">", Col("a"), Lit(0))
    plan3 = Select(pred2, Join(cond, Scan("R"), Scan("S")))
    plan4 = Join(cond, Select(pred2, Scan("R")), Scan("S"))
    report("Pair 2:  sigma_{a>0}(R JOIN S)   vs.   sigma_{a>0}(R) JOIN S",
           plan3, plan4, budget=100_000)

    # Pair 3: "join elimination" -- dropping a join whose only effect is
    # to filter/duplicate rows is invalid under bag semantics unless the
    # join key is unique and every left row matches. Multiplicity differs.
    plan5 = Project(("a",), Join(cond, Scan("R"), Scan("S")))
    plan6 = Project(("a",), Scan("R"))
    report("Pair 3:  pi_a(R JOIN S)   vs.   pi_a(R)  [join elimination]",
           plan5, plan6, budget=100_000)


if __name__ == "__main__":
    main()

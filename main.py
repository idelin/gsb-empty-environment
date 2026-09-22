"""Demo driver: search for minimal counterexamples between plan pairs.

Run:  python3.11 main.py [--max-rows N] [--domain 0,1,NULL]
"""

from __future__ import annotations

import argparse

from counterexample import DEFAULT_DOMAIN, find_counterexample
from relalg import (
    FULL, INNER, LEFT, NULL, Join, Project, Scan, Select,
    and_, col, eq, fmt_relation, is_null, lit, ne, not_, or_,
)


def build_demos():
    """Each demo: (title, plan_a, plan_b, base schemas)."""
    demos = []

    # 1. 3VL: sigma_{A = 1 OR A <> 1}(R)  vs  R
    #    In two-valued logic these are equal; with NULLs they are not.
    demos.append((
        "sigma_{A=1 OR A<>1}(R)  vs  R   [NULL / three-valued logic]",
        Select(or_(eq(col("A"), lit(1)), ne(col("A"), lit(1))), Scan("R")),
        Scan("R"),
        {"R": ("A",)},
    ))

    # 2. Inner join vs left outer join.
    demos.append((
        "R |x|_{B=C} S  vs  R LEFT OUTER JOIN S ON B=C",
        Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=INNER),
        Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=LEFT),
        {"R": ("A", "B"), "S": ("C", "D")},
    ))

    # 3. Multiplicity: pi_A(R LEFT JOIN S ON B=C) vs pi_A(R)
    #    The distinct-value sets are always identical; only duplicate
    #    counts can differ -> a pure multiset-semantics counterexample.
    demos.append((
        "pi_A(R LEFT JOIN S ON B=C)  vs  pi_A(R)   [duplicate counts matter]",
        Project(("A",), Join(Scan("R"), Scan("S"), on=(("B", "C"),), how=LEFT)),
        Project(("A",), Scan("R")),
        {"R": ("A", "B"), "S": ("C", "D")},
    ))

    # 4. Join commutativity (with projection to align column order):
    #    no counterexample exists; the search must report "not a proof".
    demos.append((
        "R |x|_{B=C} S  vs  pi_{A,B,C,D}(S |x|_{C=B} R)   [equivalent pair]",
        Join(Scan("R"), Scan("S"), on=(("B", "C"),)),
        Project(("A", "B", "C", "D"),
                Join(Scan("S"), Scan("R"), on=(("C", "B"),))),
        {"R": ("A", "B"), "S": ("C", "D")},
    ))

    return demos


def parse_domain(text: str):
    values = []
    for tok in text.split(","):
        tok = tok.strip()
        if tok.upper() == "NULL":
            values.append(NULL)
        else:
            values.append(int(tok))
    return tuple(values)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--max-rows", type=int, default=2,
                        help="max rows per relation (budget), default 2")
    parser.add_argument("--domain", type=parse_domain, default=DEFAULT_DOMAIN,
                        help="comma-separated values, e.g. '0,1,NULL'")
    args = parser.parse_args()

    print(f"domain = {[('NULL' if v is NULL else v) for v in args.domain]}, "
          f"budget = <= {args.max_rows} rows per relation\n")

    for i, (title, plan_a, plan_b, schemas) in enumerate(build_demos(), 1):
        print(f"=== Demo {i}: {title}")
        result = find_counterexample(
            plan_a, plan_b, schemas,
            domain=args.domain, max_rows=args.max_rows,
        )
        print(f"    databases checked: {result.checked}")
        if result.found:
            total = sum(len(rel.rows) for rel in result.db.values())
            print(f"    COUNTEREXAMPLE FOUND (minimal, total rows = {total})")
            for name, rel in result.db.items():
                print("    " + fmt_relation(rel).replace("\n", "\n    "))
            print("    plan A output:")
            print("    " + fmt_relation(result.output_a).replace("\n", "\n    "))
            print("    plan B output:")
            print("    " + fmt_relation(result.output_b).replace("\n", "\n    "))
        else:
            status = "budget fully searched" if result.completed else "search cut off"
            print(f"    no counterexample found ({status}, "
                  f"<= {result.max_rows} rows/relation)")
            print("    NOTE: this is NOT a proof of equivalence.")
        print()


if __name__ == "__main__":
    main()

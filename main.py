"""Demo: conservative subject-level L1 sensitivity analysis on synthetic data.

Run with:  python3.11 main.py
"""

from sensitivity import (
    Aggregate, Clip, Compose, GroupBy, Join, Table, TableDomain,
    analyze, enumerate_sensitivity, verify,
)


def show(title, query, domains=None):
    print("=" * 78)
    print(title)
    print("=" * 78)
    analysis = analyze(query)
    print("Step-by-step conservative derivation:")
    print(analysis.derivation.render(indent=1))
    if analysis.bounded:
        print(f"=> derived upper bound: sens <= {analysis.bound:g}")
    else:
        print("=> REJECTED: unbounded contribution (missing declared caps)")
    if domains is not None and analysis.bounded:
        witness = enumerate_sensitivity(query, domains)
        ok = witness.max_l1 <= analysis.bound + 1e-9
        tight = abs(witness.max_l1 - analysis.bound) <= 1e-9
        print("Finite-domain enumeration (synthetic data, sanity check only):")
        print(f"  max observed L1 over all adjacent pairs: {witness.max_l1:g}")
        print(f"  upper bound respected on the domain:     {ok}")
        print(f"  witness attains the bound (tight):       {tight}")
        print(f"  differing subject: {witness.subject}")
        for table, rows in witness.added_rows.items():
            print(f"  rows only in D ({table}): {rows}")
        print(f"  q(D)  = {witness.output_with}")
        print(f"  q(D') = {witness.output_without}")
    print()


def main():
    # Declared synthetic tables: (name, m = max rows/subject, b = value cap).
    t = Table("T", max_rows_per_subject=3, value_bound=2.0)

    show("1. Global sum: sens <= m * b = 3 * 2 = 6",
         Aggregate(t, "sum"),
         {"T": TableDomain(groups=("g",), values=(0.0, 2.0), max_rows=3)})

    show("2. Fixed group-by count over {g1, g2}: sens <= m = 3 "
         "(L1 over groups, not per-group max)",
         GroupBy(t, groups=("g1", "g2"), agg="count"),
         {"T": TableDomain(groups=("g1", "g2"), values=(0.0, 2.0), max_rows=3)})

    show("3. Clipping to [0, 1] shrinks the sum bound: sens <= m * min(b, 1) = 3",
         Aggregate(Clip(t, hi=1.0), "sum"),
         {"T": TableDomain(groups=("g",), values=(0.0, 2.0), max_rows=3)})

    t2 = Table("T2", max_rows_per_subject=2, value_bound=3.0)
    show("4. Self-join count (same subject referenced twice): "
         "sens <= m * m = 4, NOT 2 (correlated contributions, joint propagation)",
         Aggregate(Join(t2, t2), "count"),
         {"T2": TableDomain(groups=("g",), values=(0.0, 3.0), max_rows=2)})

    left = Table("L", max_rows_per_subject=2, value_bound=3.0)
    right = Table("R", max_rows_per_subject=2, value_bound=5.0)
    show("5. Join of two tables, summing left values: "
         "sens <= m_L * m_R * b_L = 2 * 2 * 3 = 12",
         Aggregate(Join(left, right, value_side="left"), "sum"),
         {"L": TableDomain(groups=("g",), values=(0.0, 3.0), max_rows=2),
          "R": TableDomain(groups=("g",), values=(0.0, 5.0), max_rows=2)})

    show("6. Sequential composition sum + count on the same table: "
         "sens <= m*b + m = 9, attained by ONE subject (correlation preserved)",
         Compose((Aggregate(t, "sum"), Aggregate(t, "count"))),
         {"T": TableDomain(groups=("g",), values=(0.0, 2.0), max_rows=3)})

    show("7. Missing row cap: rejected as unbounded",
         Aggregate(Table("U", max_rows_per_subject=None, value_bound=2.0), "sum"))

    show("8. Missing value cap: sum unbounded (count would still be bounded)",
         Aggregate(Table("U2", max_rows_per_subject=3, value_bound=None), "sum"))

    show("9. Join with an unbounded side: rejected "
         "(no unbounded joins without constraints)",
         Aggregate(Join(left, Table("R2", None, 5.0)), "count"))


if __name__ == "__main__":
    main()

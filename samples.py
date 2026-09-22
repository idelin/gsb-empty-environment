"""Runnable demo: synthetic bag relations, each rewrite's precondition, and
equivalence / counterexample outcomes. No SQL engine involved.

Run:  python3 samples.py
"""

from __future__ import annotations

from ra.expr import And, Cmp, Col, IsNull, Lit, Or, is_null_rejecting
from ra.rel import bag_eq
from ra.rewrite import (
    ALL_RULES,
    R1_PUSHDOWN_INNER,
    R2_PUSHDOWN_LEFTJOIN_PRESERVED,
    R3_OUTER_TO_INNER,
    X1_PUSHDOWN_NULL_SUPPLYING,
    r1_original,
    r2_original,
    r3_original,
    x1_original,
)
from ra.tvl import NULL

# --- Synthetic relations (bags: duplicates significant, NULL explicit) ------
# R(a, b)                         S(c, x)
R = [
    {"a": 1, "b": "x"},
    {"a": 1, "b": "x"},   # duplicate row: multiplicity must be preserved
    {"a": 2, "b": "y"},
    {"a": 3, "b": "z"},   # matches nothing in S
    {"a": 4, "b": NULL},  # NULL on the preserved side
]
S = [
    {"c": 1, "x": 5},
    {"c": 1, "x": 5},     # duplicate: inner join multiplicity multiplies
    {"c": 1, "x": 2},
    {"c": 2, "x": 7},
    {"c": 2, "x": NULL},  # NULL on the null-supplying side
]
R_ATTRS = frozenset({"a", "b"})
S_ATTRS = frozenset({"c", "x"})
THETA = Cmp("=", Col("a"), Col("c"))          # R.a = S.c

P_X_GT_3 = Cmp(">", Col("x"), Lit(3))         # null-rejecting on S
P_X_IS_NULL = IsNull(Col("x"))                # null-ACCEPTING on S
P_B_EQ_X = Cmp("=", Col("b"), Lit("x"))       # preserved-side predicate


def show(title, rows):
    print(f"    {title} ({len(rows)} row(s))")
    for row in rows:
        print(f"      {row}")


def main() -> None:
    print("=" * 78)
    print("Synthetic bag relations")
    print("=" * 78)
    show("R(a, b)", R)
    show("S(c, x)", S)

    print("\n" + "=" * 78)
    print("Null-rejection analysis on attrs(S) = {c, x}")
    print("=" * 78)
    probes = [
        ("x > 3", P_X_GT_3),
        ("x IS NULL", P_X_IS_NULL),
        ("x > 3 AND x < 10", And(P_X_GT_3, Cmp("<", Col("x"), Lit(10)))),
        ("x > 3 OR c > 3", Or(P_X_GT_3, Cmp(">", Col("c"), Lit(3)))),
    ]
    for text, pred in probes:
        verdict = is_null_rejecting(pred, S_ATTRS)
        print(f"    {text:<22} null-rejecting on S: {verdict}")

    print("\n" + "=" * 78)
    print("Rewrite rules: preconditions and outcomes")
    print("=" * 78)
    for rule in ALL_RULES:
        print(f"\n  [{rule.name}]")
        print(f"    PRECONDITION: {rule.precondition}")

    # R1: sigma_p(R |x| S) == R |x| sigma_p(S), p = x > 3
    print("\n  -- R1 demo: p = (x > 3)")
    ok = R1_PUSHDOWN_INNER.check(P_X_GT_3, R_ATTRS, S_ATTRS)
    before, after = r1_original(R, S, THETA, P_X_GT_3), R1_PUSHDOWN_INNER.apply(
        R, S, THETA, P_X_GT_3, S_ATTRS
    )
    print(f"    precondition holds: {ok}; equivalent: {bag_eq(before, after)}")
    show("sigma_{x>3}(R |x| S)", before)
    show("R |x| sigma_{x>3}(S)", after)

    # R2: sigma_p(R ⟕ S) == sigma_p(R) ⟕ S, p = (b = 'x')
    print("\n  -- R2 demo: p = (b = 'x')")
    ok = R2_PUSHDOWN_LEFTJOIN_PRESERVED.check(P_B_EQ_X, R_ATTRS, S_ATTRS)
    before = r2_original(R, S, THETA, P_B_EQ_X, S_ATTRS)
    after = R2_PUSHDOWN_LEFTJOIN_PRESERVED.apply(R, S, THETA, P_B_EQ_X, S_ATTRS)
    print(f"    precondition holds: {ok}; equivalent: {bag_eq(before, after)}")
    show("sigma_{b='x'}(R ⟕ S)", before)
    show("sigma_{b='x'}(R) ⟕ S", after)

    # R3: sigma_p(R ⟕ S) == sigma_p(R |x| S) because x > 3 is null-rejecting
    print("\n  -- R3 demo: p = (x > 3), null-rejecting -> LEFT JOIN becomes INNER")
    ok = R3_OUTER_TO_INNER.check(P_X_GT_3, R_ATTRS, S_ATTRS)
    before = r3_original(R, S, THETA, P_X_GT_3, S_ATTRS)
    after = R3_OUTER_TO_INNER.apply(R, S, THETA, P_X_GT_3, S_ATTRS)
    print(f"    precondition holds: {ok}; equivalent: {bag_eq(before, after)}")
    show("sigma_{x>3}(R ⟕ S)", before)
    show("sigma_{x>3}(R |x| S)", after)

    # X1 counterexample 1: IS NULL pushed into the right input
    print("\n  -- X1 counterexample (a): p = (x IS NULL) pushed into right input")
    before = x1_original(R, S, THETA, P_X_IS_NULL, S_ATTRS)
    after = X1_PUSHDOWN_NULL_SUPPLYING.apply(R, S, THETA, P_X_IS_NULL, S_ATTRS)
    print(f"    equivalent: {bag_eq(before, after)}  <- rewrite is INVALID")
    show("sigma_{x IS NULL}(R ⟕ S)", before)
    show("R ⟕ sigma_{x IS NULL}(S)", after)

    # X1 counterexample 2: even a null-rejecting predicate cannot be pushed
    print("\n  -- X1 counterexample (b): p = (x > 3) pushed into right input")
    before = x1_original(R, S, THETA, P_X_GT_3, S_ATTRS)
    after = X1_PUSHDOWN_NULL_SUPPLYING.apply(R, S, THETA, P_X_GT_3, S_ATTRS)
    print(f"    equivalent: {bag_eq(before, after)}  <- rewrite is INVALID")
    show("sigma_{x>3}(R ⟕ S)", before)
    show("R ⟕ sigma_{x>3}(S)", after)


if __name__ == "__main__":
    main()

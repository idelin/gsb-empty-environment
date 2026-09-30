"""Runnable tour of the exactsets library (stdlib only).

Run from the repository root:  python3 examples/demo.py
"""
import os
import sys
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from exactsets import Budget, Rat, evaluator, solve, verify
from exactsets import polys as P

X2M2 = [-2, 0, 1]  # x^2 - 2


def show(title, expr, samples=()):
    print("=" * 72)
    print(title)
    sol = solve(expr)
    assert sol.status == "complete", sol.note
    print("solution:", sol.set.canonical_str())
    print("-- reviewable steps --")
    print(sol.steps.summary())
    assert verify(sol.certificate)
    print("certificate: VERIFIED (identities, Sturm counts, signs, multiplicities)")
    if samples:
        bad = evaluator.cross_check(expr, sol.set, samples)
        assert not bad, bad
        print(f"cross-check: direct evaluator agrees on {len(samples)} rational samples")
    return sol.set


def main():
    # 1. (x^2-2)/(x^2-2) >= 0  -- cancellation must not restore +-sqrt(2)
    show(
        "(x^2-2)/(x^2-2) >= 0",
        Rat(X2M2, X2M2) >= 0,
        samples=[Fraction(k, 3) for k in range(-9, 10)],
    )

    # 2. (x^2-2)^2 <= 0  -- exactly two isolated points
    show(
        "(x^2-2)^2 <= 0",
        Rat(P.mul(X2M2, X2M2)) <= 0,
        samples=[Fraction(k, 2) for k in range(-6, 7)],
    )

    # 3. NOT(1/(x-1) > 0)  -- 1 stays excluded
    show(
        "NOT(1/(x-1) > 0)",
        ~(Rat([1], [-1, 1]) > 0),
        samples=[Fraction(k, 2) for k in range(-4, 7)],
    )

    # 4. shared roots of two different polynomials (gcd evidence)
    show(
        "((x^2-2)(x-1) >= 0) AND ((x^2-2)(x-3) <= 0)",
        (Rat(P.mul(X2M2, [-1, 1])) >= 0) & (Rat(P.mul(X2M2, [-3, 1])) <= 0),
        samples=[Fraction(k, 5) for k in range(-15, 21)],
    )

    # 5. set operations on existing solution sets + affine substitution
    print("=" * 72)
    print("set operations and affine substitution")
    s1 = solve(Rat(X2M2) <= 0).set            # [-sqrt2, sqrt2]
    s2 = solve(Rat([-1, 0, 1]) >= 0).set      # (-oo,-1] u [1,oo)
    print("s1           :", s1.canonical_str())
    print("s2           :", s2.canonical_str())
    print("s1 & s2      :", s1.intersection(s2).canonical_str())
    print("s1 | s2      :", s1.union(s2).canonical_str())
    print("s1 - s2      :", s1.difference(s2).canonical_str())
    print("s1, x=2t+1   :", s1.affine(Fraction(2), Fraction(1)).canonical_str())
    print("s1, x=-t     :", s1.affine(Fraction(-1), Fraction(0)).canonical_str())

    # 6. budget exhaustion: certified fragments + pending regions, no
    #    completeness claim
    print("=" * 72)
    print("budget exhaustion")
    sol = solve(Rat(X2M2) >= 0, Budget(max_refine=6))
    print("status:", sol.status, "| note:", sol.note)
    print("certified roots so far:", sol.proven_roots)
    print("pending isolating regions (lo, hi, root_count):", sol.pending)


if __name__ == "__main__":
    main()

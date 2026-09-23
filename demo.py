"""Runnable demonstration of the exact rational-function simplifier.

Usage:  python3.11 demo.py
"""

from fractions import Fraction

from rational import Polynomial, RationalFunction, ZeroDenominatorError

X = Polynomial([0, 1])


def main() -> None:

    X = Polynomial([0, 1])


    def heading(title: str) -> None:
        print("=" * 72)
        print(title)
        print("=" * 72)


    def sample(f: RationalFunction, values) -> None:
        for value in values:
            value = Fraction(value)
            try:
                print(f"    f({value}) = {f(value)}")
            except ZeroDivisionError:
                print(f"    f({value}) undefined (excluded point)")


    # 1. x/x -------------------------------------------------------------------
    heading("1. x/x  ->  1, but x = 0 stays excluded")
    f = RationalFunction(X, X)
    print(f.explain())
    sample(f, [0, 1, Fraction(7, 2)])
    print()

    # 2. Zero denominator rejected ---------------------------------------------
    heading("2. Zero denominator is rejected")
    try:
        RationalFunction(X + 1, Polynomial([0]))
    except ZeroDenominatorError as error:
        print(f"    raised {type(error).__name__}: {error}")
    print()

    # 3. Repeated root ----------------------------------------------------------
    heading("3. Repeated common factor: (x-1)^2(x+2) / ((x-1)^2*x)")
    f = RationalFunction((X - 1) ** 2 * (X + 2), (X - 1) ** 2 * X)
    print(f"    reduced = {f}")
    print(f"    domain  = {f.domain_text()}")
    sample(f, [1, 0, 3])
    print()

    # 4. Irrational removable singularity --------------------------------------
    heading("4. Common factor with irrational roots: (x^2-2)/(x^2-2)")
    p = X ** 2 - 2
    f = RationalFunction(p, p)
    print(f"    reduced = {f}")
    for point in f.exclusions:
        print(f"    excluded: {point}")
    print("    Values at admissible rationals are still exactly 1:")
    sample(f, [Fraction(3, 2), 2])
    print()

    # 5. Square-free decomposition + irrational exclusions after combination ----
    heading("5. Square-free decomposition and 1/x + 1/(x^2-2)")
    print("    Yun decomposition of (x-1)^2 (x+3)^3:")
    for multiplicity, factor in (((X - 1) ** 2) * (X + 3) ** 3).square_free():
        print(f"      multiplicity {multiplicity}: {factor}")
    a = RationalFunction(Polynomial([1]), X)
    b = RationalFunction(Polynomial([1]), X ** 2 - 2)
    result = a + b
    print(f"    1/x + 1/(x^2-2) = {result}")
    print(f"    domain = {result.domain_text()}")
    sample(result, [0, 1, 3])
    print()

    # 6. Denominator sign convention --------------------------------------------
    heading("6. Denominator leading sign normalised")
    f = RationalFunction(X, -X)
    print(f"    x/(-x) = {f}; denominator lc = {f.denominator.lc}")
    print(f"    domain  = {f.domain_text()}")
    print()

    # 7. Zero function -----------------------------------------------------------
    heading("7. Zero function 0/(x-5)")
    f = RationalFunction(Polynomial([0]), X - 5)
    print(f"    is zero function: {f.is_zero_function}; reduced = {f}")
    print(f"    domain = {f.domain_text()}")
    sample(f, [5, 9])
    print()

    # 8. Chained operations ------------------------------------------------------
    heading("8. Chained operations union exclusion sets: (x/x + 1/(x-1)) / (x-2)")
    a = RationalFunction(X, X)
    b = RationalFunction(Polynomial([1]), X - 1)
    c = RationalFunction(Polynomial([1]), X - 2)
    f = (a + b) / c
    print(f"    reduced = {f}")
    print(f"    domain  = {f.domain_text()}")
    sample(f, [0, 1, 2, Fraction(3)])
    print()
    print(f.explain())



if __name__ == "__main__":
    main()

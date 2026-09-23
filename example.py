from fractions import Fraction

from rational_power_series import (
    NonZeroConstantError,
    TruncatedPowerSeries,
    ZeroConstantError,
    add,
    compose,
    inverse,
    multiply,
)


def show(label: str, series: TruncatedPowerSeries) -> None:
    values = ", ".join(str(value) for value in series.coefficients)
    print(f"{label}: [{values}] mod x^{series.order}")


def main() -> None:
    f = TruncatedPowerSeries(
        [1, Fraction(-1, 2), 2, Fraction(-3, 4)],
        order=5,
    )
    h = TruncatedPowerSeries([2, 1], order=4)
    g = TruncatedPowerSeries(
        [0, 1, Fraction(1, 2), -1],
        order=5,
    )

    show("f", f)
    show("h", h)
    show("g", g)

    f_plus_h = add(f, h)
    show("f + h (order min(5, 4)=4)", f_plus_h)

    product = multiply(f, h, order=3)
    show("f * h retained only through order 3", product)

    f_inverse = inverse(f, order=5)
    show("f^-1", f_inverse)

    identity = multiply(f, f_inverse, order=5)
    show("f * f^-1 modulo x^5", identity)

    composition = compose(f, g, order=5)
    show("f(g(x)) modulo x^5", composition)

    try:
        inverse(TruncatedPowerSeries([0, 1], order=4))
    except ZeroConstantError as error:
        print(f"illegal inverse: {error}")

    try:
        compose(
            TruncatedPowerSeries([1, 2], order=4),
            TruncatedPowerSeries([1, 1], order=4),
            order=4,
        )
    except NonZeroConstantError as error:
        print(f"illegal composition: {error}")


if __name__ == "__main__":
    main()

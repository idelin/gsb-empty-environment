from fractions import Fraction

from rational_interpolation import (
    ConflictingObservations,
    HermiteStructureError,
    RationalHermiteInterpolator,
    format_newton,
    format_power,
    solve_constraints,
)


def print_bit_record(record):
    print(
        "bit growth:",
        f"input=({record['input_numerator_bits']}, {record['input_denominator_bits']})",
        f"newton=({record['newton_numerator_bits']}, {record['newton_denominator_bits']})",
        f"power=({record['power_numerator_bits']}, {record['power_denominator_bits']})",
    )


def main():
    print("== Hermite interpolation: p=x^3 ==")
    interpolator = RationalHermiteInterpolator()
    interpolator.add_point(0, 0)
    interpolator.add_derivative(0, 0)
    interpolator.add_point(1, 1)
    interpolator.add_derivative(1, 3)
    nodes, coefficients = interpolator.to_newton()
    print("Newton nodes:", nodes)
    print("Newton coefficients:", coefficients)
    print("Newton form:", format_newton(nodes, coefficients))
    print("power form:", format_power(interpolator.to_power()))
    report = interpolator.verify()
    print("verification passed:", report.passed)
    print("value residuals:", report.value_residuals)
    print("derivative residuals:", report.derivative_residuals)

    print("\n== Node order equivalence ==")
    before = interpolator.to_power()
    interpolator.reorder_nodes([1, 0])
    print("reordered nodes:", interpolator.to_newton()[0])
    print("power basis unchanged:", interpolator.to_power() == before)

    print("\n== Rational input and bit growth ==")
    growth = RationalHermiteInterpolator()
    growth.add_point(0, 0)
    growth.add_point(Fraction(1, 2), Fraction(1, 3))
    growth.add_point(Fraction(3, 2), Fraction(5, 7))
    print("power coefficients:", growth.to_power())
    print("power form:", format_power(growth.to_power()))
    print("verification passed:", growth.verify().passed)
    print_bit_record(growth.bit_growth_history[-1])

    print("\n== Removing a middle observation ==")
    trimmed = RationalHermiteInterpolator()
    trimmed.add_point(0, 0)
    trimmed.add_point(1, 3)
    trimmed.add_point(3, 12)
    trimmed.add_derivative(0, 1)
    print("before removal:", format_power(trimmed.to_power()))
    trimmed.remove_point(1)
    print("after removing p(1):", format_power(trimmed.to_power()))
    print("verification passed:", trimmed.verify().passed)

    print("\n== Conflict and incompatibility certificates ==")
    conflict = RationalHermiteInterpolator()
    conflict.add_point(0, 1)
    try:
        conflict.add_point(0, 2)
    except ConflictingObservations as error:
        print("conflict witness:", error.witness)
        print("conflict residual:", error.residual)

    inconsistent = solve_constraints([("value", 0, 1), ("value", 0, 2)])
    print("linear status:", inconsistent.status)
    print("linear witness:", inconsistent.witness)
    print("linear residual:", inconsistent.residual)

    print("\n== Derivative-only data outside the Newton domain ==")
    outside = RationalHermiteInterpolator()
    outside.add_point(0, 0)
    outside.add_derivative(0, 1)
    outside.add_point(1, 2)
    outside.remove_point(0)
    try:
        outside.to_power()
    except HermiteStructureError as error:
        print("Newton domain error:", error)
    print("independent diagnosis:", outside.diagnose())


if __name__ == "__main__":
    main()

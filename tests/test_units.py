from __future__ import annotations

import unittest
from fractions import Fraction

from units import (
    AffineUnitError,
    ConversionGraph,
    DimensionMismatchError,
    DimensionVector,
    InconsistencyEvidence,
    InconsistentUnitConstraints,
    IntegerSolutionLattice,
    QuantityType,
    Transform,
    UnitConstraintSystem,
    UnitRegistry,
    solve_integer_system,
    smith_normal_form,
    type_add,
    type_multiply,
    type_subtract,
)


def matmul(left, right):
    return [
        [
            sum(left[row][k] * right[k][column] for k in range(len(right)))
            for column in range(len(right[0]))
        ]
        for row in range(len(left))
    ]


class ExactLinearAlgebraTests(unittest.TestCase):
    def test_smith_normal_form_reconstructs_original_matrix(self):
        matrices = [
            [[2]],
            [[2, 4], [6, 8]],
            [[1, -1], [0, 0]],
            [[2, 0], [0, 3]],
            [[6, 10, 15]],
        ]
        for matrix in matrices:
            with self.subTest(matrix=matrix):
                diagonal, row_transform, column_transform = smith_normal_form(matrix)
                self.assertEqual(
                    matmul(matmul(row_transform, matrix), column_transform),
                    diagonal,
                )
                nonzero = [
                    diagonal[index][index]
                    for index in range(min(len(diagonal), len(diagonal[0])))
                    if diagonal[index][index] != 0
                ]
                self.assertTrue(all(value > 0 for value in nonzero))
                for earlier, later in zip(nonzero, nonzero[1:]):
                    self.assertEqual(later % earlier, 0)

    def test_integer_solution_lattice_and_independent_substitution(self):
        result = solve_integer_system([[1, -1]], [1])
        self.assertIsInstance(result, IntegerSolutionLattice)
        assert isinstance(result, IntegerSolutionLattice)
        self.assertEqual(result.particular, (1, 0))
        self.assertEqual(result.basis, ((1, 1),))
        self.assertEqual(result.evaluate((7,)), (8, 7))
        self.assertTrue(result.verification.ok)
        self.assertEqual(result.verification.particular_residuals, (0,))
        self.assertEqual(result.verification.basis_residuals, ((0,),))

    def test_integer_inconsistency_has_certificate_not_float_pass(self):
        result = solve_integer_system([[2]], [1])
        self.assertIsInstance(result, InconsistencyEvidence)
        assert isinstance(result, InconsistencyEvidence)
        self.assertEqual(result.divisor, 2)
        self.assertEqual(result.lhs_combination, (2,))
        self.assertEqual(result.rhs_combination, 1)
        self.assertIn("整除", result.describe())


class UnitExpressionTests(unittest.TestCase):
    def setUp(self):
        self.registry = UnitRegistry()
        for dimension in ("L", "T", "Theta"):
            self.registry.define_base_dimension(dimension)
        self.registry.define_unit("m", {"L": 1})
        self.registry.define_unit("s", {"T": 1})
        self.registry.define_unit("K", {"Theta": 1})
        self.registry.define_unit("delta_degC", {"Theta": 1})
        self.registry.define_unit(
            "degC",
            {"Theta": 1},
            affine=True,
            delta_unit="delta_degC",
        )

    def test_speed_times_time_is_length(self):
        meter = self.registry.unit("m")
        second = self.registry.unit("s")
        speed = meter / second
        distance = speed * second
        self.assertEqual(distance.dimension, DimensionVector({"L": 1}))
        self.assertTrue(all(isinstance(power, int) for power in distance.dimension.exponents.values()))

    def test_unit_powers_are_integer_vectors(self):
        acceleration = self.registry.unit("m") / (self.registry.unit("s") ** 2)
        self.assertEqual(acceleration.dimension.exponents, {"L": 1, "T": -2})
        with self.assertRaises(TypeError):
            _ = self.registry.unit("m") ** Fraction(1, 2)

    def test_absolute_affine_temperature_rejected_but_delta_participates(self):
        absolute_celsius = self.registry.unit("degC")
        delta_celsius = self.registry.unit("delta_degC")
        second = self.registry.unit("s")

        with self.assertRaises(AffineUnitError):
            _ = absolute_celsius * second
        with self.assertRaises(AffineUnitError):
            _ = absolute_celsius / second
        with self.assertRaises(AffineUnitError):
            _ = absolute_celsius**2

        delta_rate = delta_celsius / second
        self.assertEqual(delta_rate.dimension, DimensionVector({"Theta": 1, "T": -1}))

    def test_affine_add_subtract_type_rules(self):
        absolute = self.registry.quantity_type("degC")
        delta = self.registry.quantity_type("delta_degC")

        with self.assertRaises(AffineUnitError):
            type_add(absolute, absolute)
        self.assertFalse(type_subtract(absolute, absolute).affine)
        self.assertTrue(type_add(absolute, delta).affine)
        self.assertTrue(type_subtract(absolute, delta).affine)

    def test_unknown_dimension_and_affine_state_are_checked_jointly(self):
        self.registry.define_unknown_unit("X", affine=True)
        unknown_absolute = self.registry.quantity_type("X")
        time_type = QuantityType(DimensionVector({"T": 1}))
        with self.assertRaisesRegex(AffineUnitError, r"\?X"):
            type_multiply(unknown_absolute, time_type)

    def test_incompatible_addition_is_rejected(self):
        length = self.registry.quantity_type("m")
        time = self.registry.quantity_type("s")
        with self.assertRaises(DimensionMismatchError):
            type_add(length, time)


class UnknownConstraintTests(unittest.TestCase):
    def test_unknown_unit_gets_unique_integer_dimension(self):
        system = UnitConstraintSystem(["L", "T"], ["x"])
        unknown_x = DimensionVector.symbol("?x")
        meter = DimensionVector.symbol("L")
        second = DimensionVector.symbol("T")
        system.equate(unknown_x * second, meter, label="speed definition")

        solution = system.solve()
        dimension = solution.dimension_of("x")
        self.assertEqual(solution.lattice.free_rank, 0)
        self.assertEqual(dimension.evaluate(()), DimensionVector({"L": 1, "T": -1}))
        self.assertTrue(solution.verification.ok)

    def test_unknown_unit_can_return_free_dimension_lattice(self):
        system = UnitConstraintSystem(["L"], ["x", "y"])
        unknown_x = DimensionVector.symbol("?x")
        unknown_y = DimensionVector.symbol("?y")
        system.equate(unknown_x / unknown_y, DimensionVector.symbol("L"))

        solution = system.solve()
        self.assertEqual(solution.free_variables, ("k1",))
        self.assertEqual(solution.dimension_of("x").evaluate((3,)), DimensionVector({"L": 4}))
        self.assertEqual(solution.dimension_of("y").evaluate((3,)), DimensionVector({"L": 3}))
        self.assertTrue(solution.verification.ok)

    def test_unknown_constraint_contradiction_has_integer_evidence(self):
        system = UnitConstraintSystem(["L"], ["x"])
        system.equate(
            DimensionVector.symbol("?x", 2),
            DimensionVector.symbol("L"),
            label="would require x_L = 1/2",
        )
        with self.assertRaises(InconsistentUnitConstraints) as caught:
            system.solve()
        self.assertEqual(caught.exception.evidence.divisor, 2)
        self.assertIn("1/2", "would require x_L = 1/2")
        self.assertIn("整除", str(caught.exception))

    def test_unconstrained_unknowns_are_all_free(self):
        system = UnitConstraintSystem(["L", "T"], ["x"])
        solution = system.solve()
        self.assertEqual(solution.lattice.free_rank, 2)
        self.assertEqual(
            solution.dimension_of("x").evaluate((2, -3)),
            DimensionVector({"L": 2, "T": -3}),
        )


class ConversionGraphTests(unittest.TestCase):
    def make_temperature_registry(self):
        registry = UnitRegistry()
        registry.define_base_dimension("Theta")
        registry.define_unit("K", {"Theta": 1})
        registry.define_unit("delta_degC", {"Theta": 1})
        registry.define_unit(
            "degC",
            {"Theta": 1},
            affine=True,
            delta_unit="delta_degC",
        )
        return registry

    def test_exact_rational_conversion_and_step_domains(self):
        registry = UnitRegistry()
        registry.define_base_dimension("L")
        registry.define_unit("m", {"L": 1})
        registry.define_unit("km", {"L": 1})
        registry.define_conversion("km", "m", 1000, domain="长度比例单位；所有有理数")

        result = registry.convert(Fraction(3, 2), "km", "m")
        self.assertEqual(result.value, Fraction(3000, 2))
        self.assertIsInstance(result.value, Fraction)
        self.assertIn("适用域", result.explanation)
        self.assertIn("长度比例单位", result.explanation)

        with self.assertRaises(TypeError):
            registry.define_conversion("m", "km", 0.001)

    def test_proportional_and_affine_temperature_conversions_are_separate(self):
        registry = self.make_temperature_registry()
        registry.define_conversion("delta_degC", "K", 1)
        registry.define_conversion(
            "degC",
            "K",
            1,
            "5463/20",
            domain="摄氏绝对温度；可加减和求差，不参与乘除",
        )

        delta_transform = registry.snapshot().edges[("delta_degC", "K")]
        absolute_transform = registry.snapshot().edges[("degC", "K")]
        self.assertEqual(delta_transform.kind, "proportional")
        self.assertEqual(absolute_transform.kind, "affine")
        self.assertEqual(registry.convert(20, "degC", "K").value, Fraction(5863, 20))
        self.assertEqual(registry.convert(20, "delta_degC", "K").value, 20)

    def test_versioned_add_remove_and_old_snapshot(self):
        registry = UnitRegistry()
        registry.define_base_dimension("L")
        registry.define_unit("m", {"L": 1})
        registry.define_unit("km", {"L": 1})
        registry.define_conversion("km", "m", 1000)
        old_version = registry.version
        old_snapshot = registry.snapshot(old_version)

        registry.remove_unit("km")
        self.assertGreater(registry.version, old_version)
        with self.assertRaises(KeyError):
            registry.convert(1, "km", "m")
        self.assertEqual(old_snapshot.convert(2, "km", "m").value, 2000)
        self.assertIn("remove unit km", [revision.action for revision in registry.graph.history])

    def test_contradiction_cycle_is_reported_with_substitution_evidence(self):
        graph = ConversionGraph()
        for name in ("A", "B", "C"):
            graph.add_node(name)
        graph.add_edge("A", "B", 2)
        graph.add_edge("B", "C", 3)
        graph.add_edge("A", "C", 7)

        validation = graph.validate()
        self.assertFalse(validation.ok)
        self.assertTrue(all(check.ok for check in validation.edge_checks))
        self.assertEqual(len(validation.contradictions), 1)
        contradiction = validation.contradictions[0]
        self.assertEqual(contradiction.path, ("A", "B", "C", "A"))
        self.assertEqual(contradiction.composed.scale, Fraction(6, 7))
        self.assertNotEqual(contradiction.scale_residual, 0)
        self.assertTrue(any(residual != 0 for residual in contradiction.sample_residuals))
        self.assertIn("矛盾环", contradiction.describe())

    def test_consistent_cycle_has_no_contradiction(self):
        graph = ConversionGraph()
        for name in ("A", "B", "C"):
            graph.add_node(name)
        graph.add_edge("A", "B", 2)
        graph.add_edge("B", "C", 3)
        graph.add_edge("A", "C", 6)
        validation = graph.validate()
        self.assertTrue(validation.ok)
        self.assertEqual(validation.contradictions, ())

    def test_affine_transform_inverse_is_exact(self):
        transform = Transform(Fraction(9, 5), Fraction(32), "华氏/摄氏仿射温度")
        inverse = transform.inverse()
        value = Fraction(100)
        self.assertEqual(inverse.apply(transform.apply(value)), value)
        self.assertEqual(transform.kind, "affine")


if __name__ == "__main__":
    unittest.main()

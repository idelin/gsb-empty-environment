import unittest

from lexopt import (
    FEASIBLE,
    INFEASIBLE,
    OPTIMAL,
    UNKNOWN,
    Constraint,
    Model,
    Solver,
    all_different,
    check_solution,
    explain,
    linear_constraint,
    verify_result,
)


def bool_model():
    model = Model()
    model.add_variable("x", (0, 1))
    model.add_variable("y", (0, 1))
    return model


class HardConstraintTests(unittest.TestCase):
    def test_hard_constraints_always_enforced(self):
        model = bool_model()
        model.add_constraint(linear_constraint("sum1", {"x": 1, "y": 1}, "==", 1))
        model.add_soft("prefer_x1", ("x",), lambda a: a["x"] == 1, weight=3, level=0)
        result = Solver(model).solve()
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.assignment["x"] + result.assignment["y"], 1)
        self.assertEqual(result.objective, (0,))
        self.assertTrue(verify_result(model, result).ok)

    def test_infeasible_hard_conflict_cannot_be_bought(self):
        model = bool_model()
        model.add_constraint(linear_constraint("x_is_0", {"x": 1}, "==", 0))
        model.add_constraint(linear_constraint("x_is_1", {"x": 1}, "==", 1))
        model.add_soft("bait", ("x",), lambda a: a["x"] == 0, weight=10**9, level=0)
        result = Solver(model).solve()
        self.assertEqual(result.status, INFEASIBLE)
        self.assertIsNone(result.assignment)
        report = verify_result(model, result)
        self.assertTrue(report.ok, report.problems)
        self.assertIn("infeasibility confirmed", report.notes[0])

    def test_hard_constraint_rejects_nonzero_weight(self):
        with self.assertRaises(ValueError):
            Constraint("bad", ("x",), lambda a: True, hard=True, weight=5, level=0)

    def test_empty_domain_rejected(self):
        model = Model()
        with self.assertRaises(ValueError):
            model.add_variable("x", [])


class WeightPolicyTests(unittest.TestCase):
    def test_negative_weight_rejected(self):
        model = bool_model()
        with self.assertRaises(ValueError):
            model.add_soft("neg", ("x",), lambda a: True, weight=-1, level=0)

    def test_zero_weight_rejected(self):
        model = bool_model()
        with self.assertRaises(ValueError):
            model.add_soft("zero", ("x",), lambda a: True, weight=0, level=0)

    def test_non_integer_weight_rejected(self):
        model = bool_model()
        with self.assertRaises(TypeError):
            model.add_soft("float", ("x",), lambda a: True, weight=1.5, level=0)
        with self.assertRaises(TypeError):
            model.add_soft("bool", ("x",), lambda a: True, weight=True, level=0)

    def test_negative_level_rejected(self):
        model = bool_model()
        with self.assertRaises(ValueError):
            model.add_soft("lvl", ("x",), lambda a: True, weight=1, level=-1)


class LexicographicTests(unittest.TestCase):
    def test_high_level_not_offset_by_low_level_gains(self):
        model = Model()
        model.add_variable("x", (0, 1))
        model.add_soft("high", ("x",), lambda a: a["x"] == 1, weight=1, level=0)
        model.add_soft("low", ("x",), lambda a: a["x"] == 0, weight=1000, level=1)
        result = Solver(model).solve()
        self.assertEqual(result.assignment["x"], 1)
        self.assertEqual(result.objective, (0, 1000))
        self.assertTrue(verify_result(model, result).ok)

    def test_tie_broken_at_lower_level(self):
        model = bool_model()
        model.add_constraint(linear_constraint("one_only", {"x": 1, "y": 1}, "==", 1))
        model.add_soft("high_x", ("x",), lambda a: a["x"] == 1, weight=2, level=0)
        model.add_soft("high_y", ("y",), lambda a: a["y"] == 1, weight=2, level=0)
        model.add_soft("low", ("x",), lambda a: a["x"] == 1, weight=7, level=1)
        result = Solver(model).solve()
        self.assertEqual(result.objective, (2, 0))
        self.assertEqual(result.assignment, {"x": 1, "y": 0})

    def test_penalties_are_exact_integers(self):
        model = bool_model()
        model.add_soft("a", ("x",), lambda a: a["x"] == 1, weight=4, level=0)
        model.add_soft("b", ("y",), lambda a: a["y"] == 1, weight=6, level=0)
        model.add_constraint(linear_constraint("cap", {"x": 1, "y": 1}, "<=", 1))
        result = Solver(model).solve()
        self.assertIsInstance(result.objective[0], int)
        self.assertEqual(result.objective, (4,))
        self.assertEqual(result.assignment["y"], 1)


class ExplanationTests(unittest.TestCase):
    def test_violations_itemized(self):
        model = bool_model()
        model.add_constraint(linear_constraint("both1", {"x": 1, "y": 1}, "==", 1))
        model.add_soft("want_x0", ("x",), lambda a: a["x"] == 0, weight=5, level=0)
        model.add_soft("want_y0", ("y",), lambda a: a["y"] == 0, weight=9, level=1)
        result = Solver(model).solve()
        self.assertEqual(result.objective, (0, 9))
        self.assertEqual(len(result.violations), 1)
        v = result.violations[0]
        self.assertEqual((v.constraint, v.level, v.weight), ("want_y0", 1, 9))
        text = explain(model, result)
        self.assertIn("want_y0: violated, contributes +9", text)
        self.assertIn("level 0: total penalty = 0", text)
        self.assertIn("level 1: total penalty = 9", text)

    def test_lower_bound_tight_when_optimal(self):
        model = bool_model()
        model.add_soft("s", ("x",), lambda a: a["x"] == 1, weight=3, level=0)
        result = Solver(model).solve()
        self.assertEqual(result.status, OPTIMAL)
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.lower_bound, result.objective)
        self.assertIn("optimality proven", explain(model, result))

    def test_node_limit_gives_valid_lower_bound(self):
        model = Model()
        for i in range(4):
            model.add_variable(f"v{i}", range(3))
            model.add_soft(f"p{i}", (f"v{i}",), lambda a, i=i: a[f"v{i}"] == 2,
                           weight=i + 1, level=0)
        result = Solver(model, max_nodes=0).solve()
        self.assertEqual(result.status, UNKNOWN)
        self.assertEqual(result.lower_bound, (0,))

        limited = Solver(model, max_nodes=3).solve()
        self.assertIn(limited.status, (FEASIBLE, UNKNOWN))
        full = Solver(model).solve()
        self.assertEqual(full.objective, (0,))
        if limited.objective is not None:
            self.assertLessEqual(limited.lower_bound, limited.objective)
            self.assertLessEqual(limited.lower_bound, full.objective)


class CheckerTests(unittest.TestCase):
    def build(self):
        model = bool_model()
        model.add_constraint(linear_constraint("sum1", {"x": 1, "y": 1}, "==", 1))
        model.add_soft("prefer_x1", ("x",), lambda a: a["x"] == 1, weight=3, level=0)
        return model

    def test_valid_solution_passes(self):
        model = self.build()
        result = Solver(model).solve()
        report = verify_result(model, result)
        self.assertTrue(report.ok, report.problems)
        self.assertIn("optimality confirmed", report.notes[0])

    def test_hard_violation_detected(self):
        model = self.build()
        report = check_solution(model, {"x": 1, "y": 1})
        self.assertFalse(report.ok)
        self.assertTrue(any("hard constraint" in p for p in report.problems))

    def test_wrong_claimed_objective_detected(self):
        model = self.build()
        report = check_solution(model, {"x": 0, "y": 1}, claimed_objective=(0,))
        self.assertFalse(report.ok)
        self.assertTrue(any("objective" in p for p in report.problems))

    def test_out_of_domain_detected(self):
        model = self.build()
        report = check_solution(model, {"x": 5, "y": 0})
        self.assertFalse(report.ok)
        self.assertTrue(any("outside" in p for p in report.problems))

    def test_missing_and_extra_variables_detected(self):
        model = self.build()
        self.assertFalse(check_solution(model, {"x": 1}).ok)
        self.assertFalse(check_solution(model, {"x": 1, "y": 0, "z": 0}).ok)

    def test_forged_better_objective_detected(self):
        model = self.build()
        result = Solver(model).solve()
        forged = result.__class__(
            status=OPTIMAL,
            assignment=result.assignment,
            objective=(result.objective[0] - 1,),
            lower_bound=result.lower_bound,
            violations=result.violations,
            nodes_explored=result.nodes_explored,
            proven_optimal=True,
        )
        self.assertFalse(verify_result(model, forged).ok)


class ConstraintBuilderTests(unittest.TestCase):
    def test_all_different(self):
        model = Model()
        for name in ("a", "b", "c"):
            model.add_variable(name, (1, 2, 3))
        model.add_constraint(all_different("ad", ("a", "b", "c")))
        result = Solver(model).solve()
        self.assertEqual(result.status, OPTIMAL)
        values = sorted(result.assignment.values())
        self.assertEqual(values, [1, 2, 3])

    def test_all_different_infeasible(self):
        model = Model()
        for name in ("a", "b", "c"):
            model.add_variable(name, (1, 2))
        model.add_constraint(all_different("ad", ("a", "b", "c")))
        self.assertEqual(Solver(model).solve().status, INFEASIBLE)

    def test_unknown_variable_in_scope_rejected(self):
        model = bool_model()
        with self.assertRaises(ValueError):
            model.add_hard("bad", ("nope",), lambda a: True)

    def test_bad_sense_rejected(self):
        with self.assertRaises(ValueError):
            linear_constraint("bad", {"x": 1}, "=>", 0)


class SchedulingIntegrationTests(unittest.TestCase):
    def test_meeting_schedule(self):
        model = Model()
        for name in ("m1", "m2", "m3", "m4"):
            model.add_variable(name, range(4))
        model.add_constraint(all_different("room", ("m1", "m2", "m3", "m4")))
        model.add_constraint(
            linear_constraint("order", {"m2": 1, "m1": -1}, ">=", 1)
        )
        model.add_constraint(
            linear_constraint("m3_early", {"m3": 1}, "<=", 1,
                              hard=False, weight=5, level=0)
        )
        model.add_constraint(
            linear_constraint("m4_late", {"m4": 1}, ">=", 2,
                              hard=False, weight=5, level=0)
        )
        model.add_constraint(
            linear_constraint("m1_slot0", {"m1": 1}, "==", 0,
                              hard=False, weight=1, level=1)
        )
        model.add_constraint(
            linear_constraint("m2_slot1", {"m2": 1}, "==", 1,
                              hard=False, weight=1, level=1)
        )
        result = Solver(model).solve()
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.objective, (0, 1))
        a = result.assignment
        self.assertEqual(len(set(a.values())), 4)
        self.assertGreater(a["m2"], a["m1"])
        self.assertLessEqual(a["m3"], 1)
        self.assertGreaterEqual(a["m4"], 2)
        self.assertTrue(verify_result(model, result).ok)


if __name__ == "__main__":
    unittest.main()

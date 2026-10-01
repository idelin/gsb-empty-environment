import itertools
import os
import random
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from cspsolver import Model, Solver, Status, Variable, check_assignment


def small_model():
    """x, y in {0,1,2}; hard x != y.

    Soft goals conflict on x: level 0 wants x==1 (weight 1), level 1 wants
    x==0 (weight 5). A weighted sum (1 vs 5) would pick x=0; lexicographic
    optimization must pick x=1 despite the heavier low-level penalty.
    """
    m = Model()
    m.add_variable(Variable("x", (0, 1, 2)))
    m.add_variable(Variable("y", (0, 1, 2)))
    m.add_hard("neq", ("x", "y"), lambda a, b: a != b)
    m.add_soft("x_is_1", ("x",), lambda a: a == 1, weight=1, level=0)
    m.add_soft("x_is_0", ("x",), lambda a: a == 0, weight=5, level=1)
    return m


class TestHardConstraints(unittest.TestCase):
    def test_hard_unsat(self):
        m = Model()
        m.add_variable(Variable("x", (0, 1)))
        m.add_hard("is0", ("x",), lambda a: a == 0)
        m.add_hard("is1", ("x",), lambda a: a == 1)
        r = Solver(m).solve()
        self.assertIs(r.status, Status.UNSAT)
        self.assertIsNone(r.assignment)

    def test_hard_conflict_not_buyable(self):
        # A huge soft reward for x==1 cannot override the hard x==0.
        m = Model()
        m.add_variable(Variable("x", (0, 1)))
        m.add_hard("is0", ("x",), lambda a: a == 0)
        m.add_soft("likes1", ("x",), lambda a: a == 1, weight=10**9, level=0)
        r = Solver(m).solve()
        self.assertIs(r.status, Status.OPTIMAL)
        self.assertEqual(r.assignment, {"x": 0})
        self.assertEqual(r.objective, (10**9,))

    def test_empty_domain_rejected(self):
        with self.assertRaises(ValueError):
            Variable("x", [])

    def test_unknown_variable_rejected(self):
        m = Model()
        m.add_variable(Variable("x", (0,)))
        with self.assertRaises(ValueError):
            m.add_hard("bad", ("nope",), lambda a: True)


class TestSoftObjectives(unittest.TestCase):
    def test_lexicographic_priority(self):
        # Level-0 goal x==1 beats the weight-5 level-1 goal x==0.
        r = Solver(small_model()).solve()
        self.assertIs(r.status, Status.OPTIMAL)
        self.assertEqual(r.objective, (0, 5))
        self.assertEqual(r.assignment["x"], 1)

    def test_negative_weight_rejected(self):
        m = Model()
        m.add_variable(Variable("x", (0,)))
        with self.assertRaises(ValueError):
            m.add_soft("bad", ("x",), lambda a: True, weight=-1)

    def test_non_integer_weight_rejected(self):
        m = Model()
        m.add_variable(Variable("x", (0,)))
        for bad in (1.5, "2", True):
            with self.assertRaises(TypeError):
                m.add_soft("bad", ("x",), lambda a: True, weight=bad)

    def test_negative_level_rejected(self):
        m = Model()
        m.add_variable(Variable("x", (0,)))
        with self.assertRaises(ValueError):
            m.add_soft("bad", ("x",), lambda a: True, weight=1, level=-1)

    def test_empty_model(self):
        r = Solver(Model()).solve()
        self.assertIs(r.status, Status.OPTIMAL)
        self.assertEqual(r.assignment, {})
        self.assertEqual(r.objective, ())


class TestExplanationAndChecker(unittest.TestCase):
    def test_explanation_terms_and_bounds(self):
        r = Solver(small_model()).solve()
        self.assertTrue(r.checked)
        by_level = {ls.level: ls for ls in r.levels}
        self.assertEqual(by_level[0].penalty, 0)
        self.assertEqual(by_level[0].certified_lower_bound, 0)
        self.assertEqual(by_level[1].penalty, 5)
        self.assertEqual(by_level[1].certified_lower_bound, 5)
        terms = by_level[1].terms
        self.assertEqual(len(terms), 1)
        self.assertEqual(terms[0].constraint, "x_is_0")
        self.assertEqual(terms[0].weight, 5)
        self.assertEqual(terms[0].scope_values, {"x": 1})
        text = r.explain()
        self.assertIn("certified lower bound=5", text)
        self.assertIn("x_is_0", text)

    def test_checker_catches_hard_violation(self):
        m = small_model()
        res = check_assignment(m, {"x": 1, "y": 1})
        self.assertFalse(res.hard_ok)
        self.assertEqual(res.hard_failures, ["neq"])

    def test_checker_computes_penalties_independently(self):
        m = small_model()
        res = check_assignment(m, {"x": 0, "y": 2})
        self.assertTrue(res.hard_ok)
        self.assertEqual(res.penalties, (1, 0))

    def test_checker_rejects_out_of_domain(self):
        m = small_model()
        with self.assertRaises(ValueError):
            check_assignment(m, {"x": 0, "y": 99})


class TestBruteForceCrossCheck(unittest.TestCase):
    """Random small models: solver must match exhaustive enumeration."""

    def brute_force(self, m):
        names = list(m.variables)
        best = None
        for combo in itertools.product(*(m.variables[n].domain for n in names)):
            a = dict(zip(names, combo))
            if not all(
                c.predicate(*(a[v] for v in c.var_names)) for c in m.hard
            ):
                continue
            vec = [0] * m.num_levels
            for c in m.soft:
                if not c.predicate(*(a[v] for v in c.var_names)):
                    vec[c.level] += c.weight
            vec = tuple(vec)
            if best is None or vec < best:
                best = vec
        return best

    def random_model(self, rng):
        m = Model()
        names = [f"x{i}" for i in range(4)]
        for n in names:
            m.add_variable(Variable(n, (0, 1, 2)))
        for k in range(3):
            s1, s2 = rng.sample(names, 2)
            allowed = {
                (a, b)
                for a in range(3)
                for b in range(3)
                if rng.random() < 0.6
            }
            m.add_hard(f"h{k}", (s1, s2), lambda a, b, al=allowed: (a, b) in al)
        for k in range(6):
            scope = tuple(rng.sample(names, rng.choice((1, 2))))
            target = tuple(rng.randrange(3) for _ in scope)
            m.add_soft(
                f"s{k}",
                scope,
                lambda *vals, t=target: vals == t,
                weight=rng.randrange(1, 4),
                level=rng.randrange(3),
            )
        return m

    def test_matches_brute_force(self):
        for seed in range(40):
            rng = random.Random(seed)
            m = self.random_model(rng)
            expected = self.brute_force(m)
            r = Solver(m).solve()
            with self.subTest(seed=seed):
                if expected is None:
                    self.assertIs(r.status, Status.UNSAT)
                else:
                    self.assertIs(r.status, Status.OPTIMAL)
                    self.assertEqual(r.objective, expected)
                    self.assertTrue(r.checked)


if __name__ == "__main__":
    unittest.main()

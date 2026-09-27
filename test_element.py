"""Unittests for the element-constraint GAC propagator.

Focus areas (per requirements):
- index self-reference (index aliases an array element)
- duplicate element variables
- out-of-bounds pruning + backtracking with undo
- unique index => bidirectional propagation
- multiple candidate indices must NOT force all elements equal to value
- empty array is unsatisfiable
- every removed value carries a no-support explanation
- support sets cross-checked against brute-force enumeration of all tuples
- claimed solutions verified by the independent checker
"""

import unittest

from element import (ElementConstraint, Explanation, Solver, Variable,
                     check_element, enumerate_solutions)


def make(con, *extra):
    return Solver([con, *extra])


def domains(solver):
    return {v.name: sorted(solver.store.domain(v)) for v in solver.variables}


class TestBasicPropagation(unittest.TestCase):
    def test_constant_array(self):
        idx = Variable("idx", {0, 1, 2, 3, 9})
        val = Variable("val", {1, 3, 5, 7, 9})
        con = ElementConstraint(val, [3, 5, 7], idx)
        s = make(con)
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(idx)), [0, 1, 2])
        self.assertEqual(sorted(s.store.domain(val)), [3, 5, 7])

    def test_out_of_bounds_reasons(self):
        idx = Variable("idx", {-1, 0, 5})
        val = Variable("val", {8})
        con = ElementConstraint(val, [8], idx)
        s = make(con)
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(idx)), [0])
        reasons = {v: e.reason for (var, v), e in s.store.explanations.items()}
        self.assertIn("out of bounds", reasons[-1])
        self.assertIn("out of bounds", reasons[5])

    def test_every_removal_explained(self):
        idx = Variable("idx", {0, 1, 2, 3})
        val = Variable("val", {1, 2, 3, 4})
        x = Variable("x", {1, 2})
        con = ElementConstraint(val, [x, 2, 3], idx)
        s = make(con)
        s.propagate()
        for v in s.variables:
            for gone in v.initial - s.store.domain(v):
                expl = s.store.explanations.get((v, gone))
                self.assertIsNotNone(expl, f"missing explanation for {v.name}={gone}")
                self.assertIsInstance(expl, Explanation)
                self.assertTrue(expl.reason)

    def test_empty_array_unsatisfiable(self):
        idx = Variable("idx", {0})
        val = Variable("val", {1})
        s = make(ElementConstraint(val, [], idx))
        self.assertFalse(s.propagate())
        self.assertIsNone(s.solve())
        expl = s.store.explanations[(idx, 0)]
        self.assertIn("out of bounds", expl.reason)


class TestUniqueIndexBidirectional(unittest.TestCase):
    def test_value_to_element(self):
        idx = Variable("idx", {1})
        x = Variable("x", {0, 1, 2})
        val = Variable("val", {2, 5})
        s = make(ElementConstraint(val, [10, x, 30], idx))
        self.assertTrue(s.propagate())
        # value={2,5} intersect element-domain => x must become {2}
        self.assertEqual(sorted(s.store.domain(x)), [2])
        self.assertEqual(sorted(s.store.domain(val)), [2])

    def test_element_to_value(self):
        idx = Variable("idx", {0})
        x = Variable("x", {3, 4})
        val = Variable("val", {1, 2, 3, 4, 5})
        s = make(ElementConstraint(val, [x], idx))
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(val)), [3, 4])


class TestMultipleCandidates(unittest.TestCase):
    def test_not_all_forced_equal(self):
        # idx in {0,1}: x and y must NOT be unified with each other/value.
        idx = Variable("idx", {0, 1})
        x = Variable("x", {1, 2})
        y = Variable("y", {2, 3})
        val = Variable("val", {1, 2, 3})
        s = make(ElementConstraint(val, [x, y], idx))
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(x)), [1, 2])
        self.assertEqual(sorted(s.store.domain(y)), [2, 3])
        self.assertEqual(sorted(s.store.domain(val)), [1, 2, 3])
        # And there genuinely are solutions where x != y.
        _, sols = enumerate_solutions(val, [x, y], idx)
        self.assertTrue(any(a[x] != a[y] for a in sols))


class TestAliasing(unittest.TestCase):
    def test_index_is_array_element(self):
        # v = [i, 10][i]: i=0 -> v=i=0 ; i=1 -> v=10
        i = Variable("i", {0, 1})
        v = Variable("v", {0, 1, 10})
        s = make(ElementConstraint(v, [i, 10], i))
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(i)), [0, 1])
        self.assertEqual(sorted(s.store.domain(v)), [0, 10])  # 1 removed

    def test_self_reference_correlation_kept(self):
        # Restricting i must restrict v through the alias, not independently.
        i = Variable("i", {0, 1})
        v = Variable("v", {0, 10})
        s = make(ElementConstraint(v, [i, 10], i))
        s.propagate()
        mark = s.store.save()
        s.store.remove(i, 1, Explanation("test", "i", 1, "forced", {}))
        s.propagate()
        self.assertEqual(sorted(s.store.domain(v)), [0])  # i=0 => v=i=0
        s.store.restore(mark)
        s.store.remove(i, 0, Explanation("test", "i", 0, "forced", {}))
        s.propagate()
        self.assertEqual(sorted(s.store.domain(v)), [10])  # i=1 => v=10

    def test_self_reference_impossible_index(self):
        # v = [i][i]: i=1 is out of bounds; i=0 forces v=i=0.
        i = Variable("i", {0, 1})
        v = Variable("v", {0, 5})
        s = make(ElementConstraint(v, [i], i))
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(i)), [0])
        self.assertEqual(sorted(s.store.domain(v)), [0])
        expl = s.store.explanations[(i, 1)]
        self.assertIn("out of bounds", expl.reason)

    def test_value_aliases_array_element(self):
        # v = [v, 7][idx]: idx=1 forces v=7; v=3 kills idx=1.
        idx = Variable("idx", {0, 1})
        v = Variable("v", {3, 7})
        s = make(ElementConstraint(v, [v, 7], idx))
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(idx)), [0, 1])
        self.assertEqual(sorted(s.store.domain(v)), [3, 7])
        s.store.remove(v, 7, Explanation("test", "v", 7, "forced", {}))
        s.propagate()
        self.assertEqual(sorted(s.store.domain(idx)), [0])


class TestDuplicateElements(unittest.TestCase):
    def test_repeated_variable(self):
        idx = Variable("idx", {0, 1})
        x = Variable("x", {1, 2})
        val = Variable("val", {1, 2, 3})
        s = make(ElementConstraint(val, [x, x], idx))
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(val)), [1, 2])
        self.assertEqual(sorted(s.store.domain(x)), [1, 2])

    def test_repeated_variable_narrows(self):
        idx = Variable("idx", {0, 1})
        x = Variable("x", {1, 2})
        val = Variable("val", {2})
        s = make(ElementConstraint(val, [x, x], idx))
        self.assertTrue(s.propagate())
        self.assertEqual(sorted(s.store.domain(x)), [2])


class TestUndoAndBacktracking(unittest.TestCase):
    def test_restore_recovers_domains_and_drops_explanations(self):
        idx = Variable("idx", {0, 1, 2})
        val = Variable("val", {4, 5, 6})
        s = make(ElementConstraint(val, [4, 5, 6], idx))
        s.propagate()
        mark = s.store.save()
        snapshot = domains(s)
        n_expl = len(s.store.explanations)
        s.store.remove(idx, 0, Explanation("t", "idx", 0, "forced", {}))
        s.store.remove(idx, 1, Explanation("t", "idx", 1, "forced", {}))
        s.propagate()
        self.assertEqual(sorted(s.store.domain(val)), [6])
        s.store.restore(mark)
        self.assertEqual(domains(s), snapshot)
        self.assertEqual(len(s.store.explanations), n_expl)
        self.assertNotIn((idx, 0), s.store.explanations)

    def test_out_of_bounds_branch_backtracks(self):
        # Branching idx=1 on a size-1 array must fail and be undone.
        idx = Variable("idx", {0, 1})
        val = Variable("val", {5})
        s = make(ElementConstraint(val, [5], idx))
        sol = s.solve()
        self.assertIsNotNone(sol)
        self.assertEqual(sol[idx], 0)
        self.assertTrue(check_element(val, [5], idx, sol))
        # After failed branch idx=1 was explored, state is consistent:
        self.assertEqual(sorted(s.store.domain(idx)), [0])

    def test_unsatisfiable_after_branch_restores(self):
        idx = Variable("idx", {0, 1})
        x = Variable("x", {1})
        y = Variable("y", {2})
        val = Variable("val", {1, 2})
        s = make(ElementConstraint(val, [x, y], idx))
        s.propagate()
        mark = s.store.save()
        before = domains(s)
        # Force an impossible combination: val must be 3.
        s.store.remove(val, 1, Explanation("t", "val", 1, "forced", {}))
        s.store.remove(val, 2, Explanation("t", "val", 2, "forced", {}))
        self.assertFalse(s.propagate())
        s.store.restore(mark)
        self.assertEqual(domains(s), before)


class TestGroundTruthEnumeration(unittest.TestCase):
    """Cross-check GAC support sets against brute-force tuple enumeration."""

    SCENARIOS = []

    @staticmethod
    def scenarios():
        i = Variable("i", {0, 1, 2})
        v = Variable("v", {0, 1, 2, 10})
        x = Variable("x", {1, 2})
        y = Variable("y", {2, 3})
        return [
            ("const array", v, [1, 2, 10], i),
            ("var array", v, [x, y], i),
            ("self ref", v, [i, 10], Variable("i2", {0, 1, 2})),
            ("duplicates", v, [x, x], i),
        ]

    def test_support_sets_match_bruteforce(self):
        for name, val, arr, idx in self.scenarios():
            with self.subTest(name=name):
                con = ElementConstraint(val, arr, idx)
                s = make(con)
                s.propagate()
                _, sols = enumerate_solutions(val, arr, idx)
                for var in s.variables:
                    brute_support = {a[var] for a in sols}
                    self.assertEqual(s.store.domain(var), brute_support,
                                     f"{name}: domain of {var.name} != brute-force support")

    def test_solver_solution_in_bruteforce_set(self):
        for name, val, arr, idx in self.scenarios():
            with self.subTest(name=name):
                s = make(ElementConstraint(val, arr, idx))
                sol = s.solve()
                _, sols = enumerate_solutions(val, arr, idx)
                if not sols:
                    self.assertIsNone(sol)
                else:
                    self.assertIsNotNone(sol)
                    self.assertIn(sol, sols)
                    self.assertTrue(check_element(val, arr, idx, sol,
                                                  variables=s.variables))

    def test_checker_rejects_bad_assignments(self):
        idx = Variable("idx", {0, 1})
        val = Variable("val", {1, 2})
        x = Variable("x", {1, 2})
        arr = [x, 2]
        good = {idx: 1, val: 2, x: 1}
        self.assertTrue(check_element(val, arr, idx, good))
        self.assertFalse(check_element(val, arr, idx, {idx: 1, val: 1, x: 1}))
        self.assertFalse(check_element(val, arr, idx, {idx: 2, val: 2, x: 1}))
        self.assertFalse(check_element(val, arr, idx, {idx: -1, val: 2, x: 1}))
        self.assertFalse(check_element(val, arr, idx, {idx: 0, val: 2, x: 1}))
        # original-domain violation
        self.assertFalse(check_element(val, arr, idx, {idx: 0, val: 1, x: 99},
                                       variables=[idx, val, x]))


if __name__ == "__main__":
    unittest.main(verbosity=2)

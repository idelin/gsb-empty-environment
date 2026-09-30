import unittest

from muskit.cache import CachedSolver
from muskit.core import SUBSET_MINIMAL, extract_subset_minimal_core
from muskit.demo import build_problem
from muskit.exhaustive import (
    classify_all_subsets,
    recovery_consistency,
    verify_minimum_cardinality_core,
    verify_subset_minimal_core,
)
from muskit.hitting import find_min_cardinality_core


class ExhaustiveTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.problem = build_problem()
        cls.solver = CachedSolver(cls.problem)
        cls.ids = cls.problem.constraint_ids()
        cls.classification = classify_all_subsets(cls.problem, cls.solver.solve, cls.ids)

    def test_classification_ground_truth(self):
        c = self.classification
        self.assertEqual(len(c.unsat_sets) + len(c.sat_sets) + len(c.unknown_sets), 32)
        self.assertEqual(c.unknown_sets, [])
        minimal = sorted(sorted(s) for s in c.subset_minimal_unsat())
        self.assertEqual(minimal, [["a", "b"], ["c", "d", "e"]])
        self.assertEqual(c.minimum_cardinality(), 2)
        self.assertEqual([set(s) for s in c.minimum_cardinality_cores()], [{"a", "b"}])

    def test_deletion_core_category_verified(self):
        result = extract_subset_minimal_core(self.problem, self.solver.solve, self.ids)
        self.assertEqual(result.status, SUBSET_MINIMAL)
        ok, _ = verify_subset_minimal_core(result.core, self.classification)
        self.assertTrue(ok)
        ok, _ = verify_minimum_cardinality_core(result.core, self.classification)
        self.assertFalse(ok)  # subset-minimal != minimum-cardinality here

    def test_hitting_core_category_verified(self):
        mc = find_min_cardinality_core(self.problem, self.solver.solve, self.ids)
        ok, _ = verify_minimum_cardinality_core(mc.best_core, self.classification)
        self.assertTrue(ok)
        ok, _ = verify_subset_minimal_core(mc.best_core, self.classification)
        self.assertTrue(ok)  # minimum-cardinality cores are also subset-minimal

    def test_recovery_consistency(self):
        result = extract_subset_minimal_core(self.problem, self.solver.solve, self.ids)
        okp, msg = recovery_consistency(
            self.problem, self.solver.solve, result.core, self.ids,
            extract_subset_minimal_core)
        self.assertTrue(okp, msg)

    def test_every_subset_minimal_unsat_set_is_recoverable(self):
        # For each subset-minimal UNSAT set, extraction restricted to it must
        # return the set itself (fixed point), confirming category labels.
        for core in self.classification.subset_minimal_unsat():
            result = extract_subset_minimal_core(
                self.problem, self.solver.solve, sorted(core))
            self.assertEqual(set(result.core), set(core))
            self.assertEqual(result.status, SUBSET_MINIMAL)


if __name__ == "__main__":
    unittest.main()

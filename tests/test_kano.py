import itertools
import unittest

from kano.hierarchy import GeneralizationHierarchy
from kano.loss import LossModel
from kano.search import (
    Status,
    brute_force,
    equivalence_class_sizes,
    is_k_anonymous,
    search,
)
from kano.synth import default_hierarchies, generate_records
from kano.verify import verify_result


def tiny_hierarchies():
    age = GeneralizationHierarchy.from_nested(
        "age", {"*": {"young": {"a1", "a2"}, "old": {"a3", "a4"}}}
    )
    sex = GeneralizationHierarchy.from_nested("sex", {"*": {"m", "f"}})
    return [age, sex]


class HierarchyTest(unittest.TestCase):
    def test_levels_and_generalize(self):
        age, sex = tiny_hierarchies()
        self.assertEqual(age.max_level, 2)
        self.assertEqual(sex.max_level, 1)
        self.assertEqual(age.generalize("a1", 0), "a1")
        self.assertEqual(age.generalize("a1", 1), "young")
        self.assertEqual(age.generalize("a1", 2), "*")
        self.assertEqual(age.generalize("a3", 1), "old")

    def test_requires_single_root_and_rejects_dag_node(self):
        with self.assertRaises(ValueError):
            GeneralizationHierarchy("x", {"a": None, "b": None})
        with self.assertRaises(ValueError):
            GeneralizationHierarchy.from_nested(
                "x", {"*": {"p": {"q": {}}, "q": {}}}  # q reachable twice
            )

    def test_unknown_value_rejected(self):
        age, _ = tiny_hierarchies()
        with self.assertRaises(ValueError):
            age.check_value("nope")


class LossTest(unittest.TestCase):
    def test_bounds_and_monotonicity(self):
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        self.assertEqual(lm.value_loss(0, "a1"), 0.0)
        self.assertEqual(lm.value_loss(0, "*"), 1.0)
        self.assertLess(lm.value_loss(0, "young"), lm.value_loss(0, "*"))
        self.assertEqual(lm.value_loss(1, "*"), 1.0)

    def test_dataset_loss_monotone_in_levels(self):
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        records = generate_records(30, hs, seed=1)
        for a, b in itertools.product(range(3), range(2)):
            for da, db in ((1, 0), (0, 1)):
                if a + da <= 2 and b + db <= 1:
                    self.assertLessEqual(
                        lm.dataset_loss(records, (a, b)),
                        lm.dataset_loss(records, (a + da, b + db)),
                    )


class SearchTest(unittest.TestCase):
    def test_exact_matches_brute_force_on_small_instances(self):
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        for seed in range(5):
            records = generate_records(40, hs, seed=seed)
            for k in (2, 3, 5):
                exact = search(records, hs, lm, k)
                bf = brute_force(records, hs, lm, k)
                self.assertEqual(exact.status, bf.status)
                self.assertEqual(exact.loss, bf.loss)
                self.assertEqual(exact.levels, bf.levels)

    def test_pruning_reduces_visited_nodes(self):
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        records = generate_records(60, hs, seed=3)
        exact = search(records, hs, lm, k=3)
        bf = brute_force(records, hs, lm, k=3)
        self.assertLess(exact.nodes_visited, bf.nodes_visited)
        self.assertEqual(bf.nodes_visited, 3 * 2)  # full lattice product

    def test_converging_paths_deduplicated(self):
        # 2D lattice: (1,1) is reachable via (1,0) and (0,1); BFS must
        # evaluate each vector at most once, so visited <= lattice size.
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        records = generate_records(10, hs, seed=0)
        result = search(records, hs, lm, k=1)  # k=1: nothing pruned
        self.assertEqual(result.nodes_visited, 1)  # origin already k-anonymous
        result = search(records, hs, lm, k=11)  # forces full traversal
        self.assertLessEqual(result.nodes_visited, 6)

    def test_no_solution_distinguished_from_truncation(self):
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        records = generate_records(8, hs, seed=2)
        # k larger than any achievable class size -> exhaustive, no solution.
        r = search(records, hs, lm, k=100)
        self.assertEqual(r.status, Status.NO_SOLUTION)
        self.assertFalse(r.solved)
        self.assertTrue(r.exhaustive)
        # Tiny budget -> not exhaustive; must NOT report no_solution.
        r = search(records, hs, lm, k=100, budget=1)
        self.assertEqual(r.status, Status.TRUNCATED_NONE)
        self.assertFalse(r.exhaustive)

    def test_budget_truncation_statuses(self):
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        records = generate_records(50, hs, seed=4)
        full = search(records, hs, lm, k=4)
        self.assertEqual(full.status, Status.OPTIMAL)
        truncated = search(records, hs, lm, k=4, budget=full.nodes_visited - 1)
        self.assertIn(truncated.status, (Status.TRUNCATED_BEST, Status.TRUNCATED_NONE))
        self.assertFalse(truncated.exhaustive)
        if truncated.solved:
            self.assertGreaterEqual(truncated.loss, full.loss)

    def test_result_verification(self):
        hs = tiny_hierarchies()
        lm = LossModel(hs)
        records = generate_records(50, hs, seed=5)
        result = search(records, hs, lm, k=3)
        self.assertTrue(verify_result(records, hs, result, k=3))
        sizes = equivalence_class_sizes(records, hs, result.levels)
        self.assertEqual(sizes, result.equivalence_class_sizes)
        self.assertTrue(is_k_anonymous(sizes, 3))
        self.assertEqual(sum(sizes), len(records))

    def test_determinism(self):
        hs = default_hierarchies()
        lm = LossModel(hs)
        r1 = search(generate_records(80, hs, seed=9), hs, lm, k=3)
        r2 = search(generate_records(80, hs, seed=9), hs, lm, k=3)
        self.assertEqual(r1, r2)

    def test_default_hierarchies_exact_vs_brute_force(self):
        hs = default_hierarchies()
        lm = LossModel(hs)
        records = generate_records(120, hs, seed=7)
        exact = search(records, hs, lm, k=4)
        bf = brute_force(records, hs, lm, k=4)
        self.assertEqual(exact.status, Status.OPTIMAL)
        self.assertEqual(exact.loss, bf.loss)
        self.assertTrue(verify_result(records, hs, exact, k=4))


if __name__ == "__main__":
    unittest.main()

import math
import unittest

from hmm import NEG_INF, HMM, VersionedObservations, forward_backward


def model():
    return HMM(
        initial=[0.6, 0.4],
        transition=[[0.7, 0.3], [0.4, 0.6]],
        emission=[
            {"walk": 0.6, "shop": 0.3, "clean": 0.1},
            {"walk": 0.1, "shop": 0.4, "clean": 0.5},
        ],
    )


class TestVersionedObservations(unittest.TestCase):
    def setUp(self):
        self.model = model()
        self.obs = ["walk", "shop", "clean", "walk", "shop", "clean"]

    def test_base_version_matches_fresh_inference(self):
        store = VersionedObservations(self.model, self.obs)
        fresh = forward_backward(self.model, self.obs)
        self.assertAlmostEqual(store.log_evidence(), fresh.log_evidence, places=12)
        for got, want in zip(store.posteriors(), fresh.posteriors):
            for g, w in zip(got, want):
                self.assertAlmostEqual(g, w, places=12)

    def test_revision_matches_fresh_recomputation(self):
        store = VersionedObservations(self.model, self.obs)
        for pos, value in [(0, "clean"), (4, "walk"), (5, "shop"), (2, None)]:
            v = store.revise(pos, value)
            revised = list(store.observations(v))
            fresh = forward_backward(self.model, revised)
            self.assertTrue(fresh.feasible)
            self.assertAlmostEqual(
                store.log_evidence(v), fresh.log_evidence, places=10
            )
            for got, want in zip(store.posteriors(v), fresh.posteriors):
                for g, w in zip(got, want):
                    self.assertAlmostEqual(g, w, places=10)
            self.obs = revised

    def test_old_versions_remain_queryable(self):
        store = VersionedObservations(self.model, self.obs)
        before = store.posteriors(0)
        evidence_before = store.log_evidence(0)
        v1 = store.revise(2, "walk")
        self.assertNotEqual(v1, 0)
        after = store.posteriors(0)
        self.assertEqual(before, after)
        self.assertEqual(store.log_evidence(0), evidence_before)
        self.assertEqual(store.observations(0), tuple(self.obs))
        self.assertNotEqual(store.observations(v1), tuple(self.obs))

    def test_revision_to_impossible_observation_is_infeasible(self):
        store = VersionedObservations(self.model, self.obs)
        v = store.revise(3, "teleport")
        self.assertIsNone(store.posteriors(v))
        self.assertEqual(store.log_evidence(v), NEG_INF)
        # the parent version is unaffected
        self.assertIsNotNone(store.posteriors(0))

    def test_revision_can_restore_feasibility(self):
        store = VersionedObservations(self.model, self.obs)
        v_bad = store.revise(1, "teleport")
        self.assertIsNone(store.posteriors(v_bad))
        v_fixed = store.revise(1, "shop")
        self.assertEqual(store.observations(v_fixed), tuple(self.obs))
        fresh = forward_backward(self.model, self.obs)
        self.assertAlmostEqual(store.log_evidence(v_fixed), fresh.log_evidence, places=10)

    def test_revision_out_of_range_raises(self):
        store = VersionedObservations(self.model, self.obs)
        with self.assertRaises(IndexError):
            store.revise(len(self.obs), "walk")

    def test_empty_sequence_store(self):
        store = VersionedObservations(self.model, [])
        self.assertEqual(store.log_evidence(), 0.0)
        self.assertEqual(store.posteriors(), [])
        with self.assertRaises(IndexError):
            store.revise(0, "walk")


if __name__ == "__main__":
    unittest.main()

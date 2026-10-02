import tempfile
import unittest

from checker import BfsChecker, Status, replay_trace
from models import (BinaryTreeModel, CollisionChainModel, CounterModel,
                    GridModel, TokenRingModel)
from reference import reference_reach


def depth_of(checker, canonical):
    depth = 0
    c = canonical
    while checker.parents[c] is not None:
        c = checker.parents[c]
        depth += 1
    return depth


class TestViolations(unittest.TestCase):
    def test_initial_violation_zero_length_trace(self):
        model = CounterModel(5, bad_at=0)
        result = BfsChecker(model).run()
        self.assertEqual(result.status, Status.VIOLATED)
        self.assertEqual(result.trace, [0])
        self.assertEqual(result.trace_length, 0)
        self.assertTrue(replay_trace(model, result.trace))

    def test_unreachable_bad_state_is_safe(self):
        model = CounterModel(3, bad_at=10)
        result = BfsChecker(model).run()
        self.assertEqual(result.status, Status.SAFE)
        self.assertIsNone(result.trace)

    def test_shortest_trace_matches_reference(self):
        model = GridModel(5, 5, bad=(3, 2))
        result = BfsChecker(model).run()
        _, dist, first_bad = reference_reach(model)
        self.assertEqual(result.status, Status.VIOLATED)
        self.assertEqual(result.trace_length, dist[first_bad])
        self.assertEqual(result.trace[0], (0, 0))
        self.assertEqual(result.trace[-1], (3, 2))
        self.assertTrue(replay_trace(model, result.trace))

    def test_replay_rejects_tampered_trace(self):
        model = GridModel(5, 5, bad=(3, 2))
        result = BfsChecker(model).run()
        tampered = list(result.trace)
        tampered[1] = (4, 4)
        self.assertFalse(replay_trace(model, tampered))
        self.assertFalse(replay_trace(model, []))
        self.assertFalse(replay_trace(model, [(1, 1)] + result.trace[1:]))


class TestDedupAndCompression(unittest.TestCase):
    def test_hash_collisions_compare_full_states(self):
        model = CollisionChainModel(30, bad_at=30)
        checker = BfsChecker(model)
        result = checker.run()
        self.assertEqual(result.status, Status.VIOLATED)
        self.assertEqual(result.trace_length, 30)
        self.assertEqual(len(checker.store), 31)  # no false dedup on hash=42
        self.assertGreater(result.coverage.collisions, 0)
        self.assertTrue(replay_trace(model, result.trace))

    def test_collision_chain_safe_variant_matches_reference(self):
        model = CollisionChainModel(30)
        checker = BfsChecker(model)
        result = checker.run()
        reachable, _, _ = reference_reach(model)
        self.assertEqual(result.status, Status.SAFE)
        self.assertEqual(len(checker.store), len(reachable))

    def test_compression_keeps_recovery_map(self):
        model = TokenRingModel(k=4, tokens=(0, 2), bad_together=False)
        checker = BfsChecker(model)
        result = checker.run()
        reachable, dist, _ = reference_reach(model)
        self.assertEqual(result.status, Status.SAFE)
        self.assertEqual(len(reachable), 16)          # concrete states
        self.assertEqual(len(checker.store), 10)      # canonical states
        self.assertEqual(result.coverage.duplicates, 11)  # 20 generated, 9 new
        for canonical, concrete in checker.store.recovery.items():
            self.assertEqual(model.canonicalize(concrete), canonical)

    def test_compressed_model_trace_replayable(self):
        model = TokenRingModel(k=4, tokens=(0, 1), bad_together=True)
        result = BfsChecker(model).run()
        self.assertEqual(result.status, Status.VIOLATED)
        self.assertEqual(result.trace_length, 1)
        self.assertTrue(replay_trace(model, result.trace))

    def test_layer_depths_match_reference_distances(self):
        model = GridModel(4, 4)
        checker = BfsChecker(model)
        result = checker.run()
        _, dist, _ = reference_reach(model)
        self.assertEqual(result.status, Status.SAFE)
        for canonical in checker.store.canonicals():
            self.assertEqual(depth_of(checker, canonical), dist[canonical])


class TestTieredFrontier(unittest.TestCase):
    def test_spill_to_disk_preserves_results(self):
        model = BinaryTreeModel(depth=8, bad=510)  # bad = deepest leaf
        result = BfsChecker(model, memory_limit=4).run()
        self.assertEqual(result.status, Status.VIOLATED)
        self.assertEqual(result.trace_length, 8)
        self.assertGreater(result.coverage.spills, 0)
        self.assertTrue(replay_trace(model, result.trace))

    def test_small_memory_safe_run_matches_reference(self):
        model = BinaryTreeModel(depth=6)
        checker = BfsChecker(model, memory_limit=3)
        result = checker.run()
        reachable, _, _ = reference_reach(model)
        self.assertEqual(result.status, Status.SAFE)
        self.assertEqual(set(checker.store.canonicals()), reachable)
        self.assertGreater(result.coverage.spills, 0)


class TestBudget(unittest.TestCase):
    def test_state_budget_undecided_with_evidence(self):
        model = BinaryTreeModel(depth=12)
        result = BfsChecker(model, budget_states=50).run()
        cov = result.coverage
        self.assertEqual(result.status, Status.UNDECIDED)
        self.assertEqual(cov.states_explored, 50)
        self.assertGreater(cov.frontier_size, 0)
        self.assertGreater(len(cov.frontier_evidence), 0)
        self.assertTrue(cov.reason)

    def test_layer_budget_undecided(self):
        model = BinaryTreeModel(depth=10)
        result = BfsChecker(model, budget_layers=3).run()
        cov = result.coverage
        self.assertEqual(result.status, Status.UNDECIDED)
        self.assertEqual(cov.layers_completed, 3)
        self.assertEqual(cov.frontier_size, 8)  # 2^3 nodes at depth 3
        self.assertTrue(all(e["depth"] == 3 for e in cov.frontier_evidence))

    def test_undecided_never_reported_safe(self):
        model = CounterModel(100_000, bad_at=99_999)
        result = BfsChecker(model, budget_states=100).run()
        self.assertEqual(result.status, Status.UNDECIDED)
        self.assertIsNone(result.trace)


class TestCheckpoint(unittest.TestCase):
    def test_resume_matches_reference_no_dup_no_miss(self):
        model = BinaryTreeModel(depth=6)
        with tempfile.TemporaryDirectory() as ck:
            r1 = BfsChecker(model, checkpoint_dir=ck, budget_layers=3).run()
            self.assertEqual(r1.status, Status.UNDECIDED)
            checker2 = BfsChecker(model, checkpoint_dir=ck)
            r2 = checker2.run(resume=True)
            self.assertEqual(r2.status, Status.SAFE)
            reachable, _, _ = reference_reach(model)
            self.assertEqual(set(checker2.store.canonicals()), reachable)
            # coverage is cumulative across resume: every state expanded once
            self.assertEqual(r2.coverage.states_explored, len(reachable))
            self.assertEqual(r1.coverage.states_explored, 7)  # depths 0..2

    def test_resume_finds_violation_after_crash(self):
        model = GridModel(5, 5, bad=(4, 4))
        _, dist, first_bad = reference_reach(model)
        with tempfile.TemporaryDirectory() as ck:
            r1 = BfsChecker(model, checkpoint_dir=ck, budget_layers=2).run()
            self.assertEqual(r1.status, Status.UNDECIDED)
            r2 = BfsChecker(model, checkpoint_dir=ck).run(resume=True)
            self.assertEqual(r2.status, Status.VIOLATED)
            self.assertEqual(r2.trace_length, dist[first_bad])
            self.assertTrue(replay_trace(model, r2.trace))

    def test_fresh_run_ignores_checkpoint_without_resume(self):
        model = CounterModel(10)
        with tempfile.TemporaryDirectory() as ck:
            BfsChecker(model, checkpoint_dir=ck, budget_layers=2).run()
            checker = BfsChecker(model, checkpoint_dir=ck)
            result = checker.run()  # no resume -> start over
            self.assertEqual(result.status, Status.SAFE)
            self.assertEqual(len(checker.store), 11)


if __name__ == "__main__":
    unittest.main()

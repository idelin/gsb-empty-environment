import tempfile
import unittest

from modelcheck import Budget, MutexModel, run_bfs


class TestCheckpointResume(unittest.TestCase):
    def test_resume_safe_model_matches_uninterrupted(self):
        model = MutexModel(3, fixed=True)
        full = run_bfs(model, collect=True)
        self.assertEqual(full.status, "safe")
        with tempfile.TemporaryDirectory() as tmp:
            partial = run_bfs(model, budget=Budget(max_layers=1), run_dir=tmp,
                              collect=True)
            self.assertEqual(partial.status, "pending")
            resumed = run_bfs(model, run_dir=tmp, resume=True, collect=True)
        self.assertEqual(resumed.status, "safe")
        # no omissions, no duplicates: identical reachable sets and distances
        self.assertEqual(set(resumed.reachable), set(full.reachable))
        for key, (_, dist) in full.reachable.items():
            self.assertEqual(dist, resumed.reachable[key][1])
        self.assertEqual(resumed.stats.states_discovered, len(resumed.reachable))

    def test_resume_violated_model_same_shortest_trace(self):
        model = MutexModel(2, fixed=False)
        full = run_bfs(model)
        self.assertEqual(full.status, "violated")
        with tempfile.TemporaryDirectory() as tmp:
            partial = run_bfs(model, budget=Budget(max_layers=2), run_dir=tmp)
            self.assertEqual(partial.status, "pending")
            resumed = run_bfs(model, run_dir=tmp, resume=True)
        self.assertEqual(resumed.status, "violated")
        self.assertEqual(resumed.trace.states, full.trace.states)
        self.assertEqual(resumed.trace.actions, full.trace.actions)

    def test_resume_across_every_layer_boundary(self):
        # stop after each layer in turn; resuming must always reproduce the
        # uninterrupted reachable set (checkpoint recovery跨层不重不漏)
        model = MutexModel(2, fixed=True)
        full = run_bfs(model, collect=True)
        for cut in range(full.stats.layers_completed + 1):
            with tempfile.TemporaryDirectory() as tmp:
                run_bfs(model, budget=Budget(max_layers=cut), run_dir=tmp, collect=True)
                resumed = run_bfs(model, run_dir=tmp, resume=True, collect=True)
            self.assertEqual(resumed.status, "safe")
            self.assertEqual(set(resumed.reachable), set(full.reachable))

    def test_resume_requires_checkpoint(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(FileNotFoundError):
                run_bfs(MutexModel(1), run_dir=tmp, resume=True)


if __name__ == "__main__":
    unittest.main()

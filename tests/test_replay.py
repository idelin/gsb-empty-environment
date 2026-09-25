import unittest

from evidence import EvidenceStore, FactVersion, MutexRuleVersion, replay, solve_store


def build_store():
    store = EvidenceStore()
    store.add_fact_version(FactVersion("F1", 1, "sensorA", 1, 0, 5))
    store.add_fact_version(FactVersion("F1", 2, "sensorA", 4, 0, 6))
    store.add_fact_version(FactVersion("F2", 1, "sensorB", 2, 2, 9))
    store.add_fact_version(FactVersion("F3", 1, "manual", 3, 1, 7))
    store.add_rule_version(MutexRuleVersion("R1", 1, frozenset({"sensorB", "manual"}), 0))
    store.add_rule_version(MutexRuleVersion("R1", 2, frozenset({"sensorB", "manual"}), 5))
    return store


class ReplayTest(unittest.TestCase):
    def test_replay_same_digest(self):
        store = build_store()
        result = solve_store(store, observation=6)
        replayed = replay(store.events)
        again = solve_store(replayed, observation=6)
        self.assertEqual(result.digest(), again.digest())

    def test_event_ids_stable_and_versioned(self):
        store = build_store()
        ids = [e.event_id for e in store.events]
        self.assertEqual(ids, [f"evt-{i:04d}" for i in range(1, len(ids) + 1)])
        self.assertTrue(all(e.version == 1 for e in store.events))

    def test_evidence_carries_ids_and_versions(self):
        store = build_store()
        result = solve_store(store, observation=6)
        self.assertEqual(set(result.evidence.fact_refs),
                         {"F1@v2", "F2@v1", "F3@v1"})
        self.assertEqual(set(result.evidence.rule_refs), {"R1@v2"})
        # 每个可行段的覆盖选择都引用带版本的事实
        for seg in result.feasible_segments():
            for piece in seg.cover:
                self.assertTrue(piece.selection.fact_refs)
                for ref in piece.selection.fact_refs:
                    self.assertIn("@v", ref)

    def test_steps_have_stable_ids_and_version(self):
        store = build_store()
        result = solve_store(store, observation=6)
        ids = [s.step_id for s in result.trace]
        self.assertEqual(ids, [f"step-{i:04d}" for i in range(1, len(ids) + 1)])
        self.assertTrue(all(s.solver_version for s in result.trace))

    def test_deterministic_across_calls(self):
        self.assertEqual(solve_store(build_store(), 6).digest(),
                         solve_store(build_store(), 6).digest())


if __name__ == "__main__":
    unittest.main()

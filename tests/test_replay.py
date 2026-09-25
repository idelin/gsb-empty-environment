import unittest

from evidence import (
    FactVersion,
    IncrementalEngine,
    MutexRule,
    Query,
    replay_solve,
    solve,
)

RULES = [MutexRule("mutex-vendors", 1, frozenset({"vendorA", "vendorB"}))]
QUERY = Query("q-replay", 9, ("price", "rating"))


def base_facts():
    return [
        FactVersion("price-vendorA", 1, "price", "vendorA", 0, 10, recorded_at=1),
        FactVersion("price-vendorB", 1, "price", "vendorB", 4, 8, recorded_at=1),
        FactVersion("rating-manual", 1, "rating", "manual", 0, 6, recorded_at=2),
        FactVersion("rating-manual", 2, "rating", "manual", 0, 9, recorded_at=7),
        FactVersion("price-archive", 1, "price", "archive", 0, 9, recorded_at=100),
    ]


class ReplayTest(unittest.TestCase):
    def test_replay_matches_original_run(self):
        facts = base_facts()
        result = solve(facts, RULES, QUERY)
        self.assertTrue(replay_solve(facts, RULES, QUERY, result))

    def test_replay_detects_tampered_journal(self):
        facts = base_facts()
        result = solve(facts, RULES, QUERY)
        result.journal.steps[1]["payload"]["status"] = "tampered"
        self.assertFalse(replay_solve(facts, RULES, QUERY, result))

    def test_replay_detects_tampered_inputs(self):
        facts = base_facts()
        result = solve(facts, RULES, QUERY)
        changed = base_facts() + [
            FactVersion("extra", 1, "rating", "manual", 0, 9, recorded_at=3)
        ]
        self.assertFalse(replay_solve(changed, RULES, QUERY, result))

    def test_replay_with_budget(self):
        facts = base_facts()
        result = solve(facts, RULES, QUERY, max_atomic_segments=2)
        self.assertTrue(
            replay_solve(facts, RULES, QUERY, result, max_atomic_segments=2)
        )

    def test_engine_replay_after_updates(self):
        engine = IncrementalEngine(RULES, QUERY)
        engine.load(base_facts())
        engine.apply_update(
            FactVersion("price-vendorA", 2, "price", "vendorA", 3, 10, recorded_at=8)
        )
        engine.apply_update(
            FactVersion("rating-manual", 3, "rating", "manual", 0, 8, recorded_at=8)
        )
        self.assertTrue(engine.replay())

    def test_journal_steps_have_stable_ids_and_versions(self):
        result = solve(base_facts(), RULES, QUERY)
        for index, step in enumerate(result.journal.steps, start=1):
            self.assertEqual(f"step-{index:04d}", step["step_id"])
            self.assertEqual(1, step["version"])


if __name__ == "__main__":
    unittest.main()

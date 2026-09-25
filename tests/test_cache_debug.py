"""Permission-scoped cache isolation and projected debug output."""
import json
import unittest

from secexplain.cache import ExplanationCache
from secexplain.engine import Engine
from secexplain.model import PUBLIC, SECRET
from secexplain.projection import project, project_trace
from secexplain.sampledata import build_world


class CacheIsolationTest(unittest.TestCase):
    def test_entries_are_isolated_per_clearance(self):
        world = build_world()
        derivation = Engine(world.rules).derive(world.facts)
        expl_public = project(derivation, "approved", PUBLIC)
        expl_secret = project(derivation, "approved", SECRET)
        cache = ExplanationCache()
        cache.put(SECRET, "fd", "rd", "approved", expl_secret)
        cache.put(PUBLIC, "fd", "rd", "approved", expl_public)
        # Same digests and query, different clearance: no cross-service.
        self.assertIs(cache.get(SECRET, "fd", "rd", "approved"), expl_secret)
        self.assertIs(cache.get(PUBLIC, "fd", "rd", "approved"), expl_public)
        self.assertIsNone(cache.get(1, "fd", "rd", "approved"))
        self.assertEqual(cache.size(PUBLIC), 1)
        self.assertEqual(cache.size(SECRET), 1)
        self.assertEqual(cache.size(1), 0)

    def test_secret_entry_not_served_to_public_caller(self):
        cache = ExplanationCache()
        cache.put(SECRET, "fd", "rd", "q", "secret-explanation")
        self.assertIsNone(cache.get(PUBLIC, "fd", "rd", "q"))


class DebugProjectionTest(unittest.TestCase):
    def test_debug_trace_follows_projection(self):
        world = build_world(chain_len=3, extra_hidden=2)
        derivation = Engine(world.rules).derive(world.facts)
        trace = project_trace(derivation, PUBLIC)
        blob = json.dumps(trace)
        for token in ("risk_cleared", "F-SCREEN", "F-NOTE", "R-CHAIN"):
            self.assertNotIn(token, blob)
        # The visible firing of R-APPROVE-001 is kept, with hidden inputs
        # recorded only as a constant boolean.
        self.assertEqual(len(trace), 1)
        event = trace[0]
        self.assertEqual(event["rule_id"], "R-APPROVE-001")
        self.assertEqual(event["inputs"], ["F-INCOME-001", "F-ID-001"])
        self.assertTrue(event["withheld_inputs"])

    def test_full_clearance_trace_contains_hidden_events(self):
        world = build_world(chain_len=3)
        derivation = Engine(world.rules).derive(world.facts)
        trace = project_trace(derivation, SECRET)
        self.assertEqual(len(trace), 4)  # 3 chain steps + approval


if __name__ == "__main__":
    unittest.main()

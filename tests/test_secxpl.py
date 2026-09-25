"""Unit tests for secxpl.  Run: python3 -m unittest discover -s tests -v"""

from __future__ import annotations

import dataclasses
import unittest

from secxpl import ExplanationCache, explain, replay
from secxpl.naive import naive_explain
from secxpl.noninterference import find_violations, low_equivalent
from secxpl.sample import QUERIES, UNIVERSE, all_worlds, make_world

RESTRICTED_IDS = ("core-temp-critical", "sensor-raw-anomaly", "manual-override")

FULL = make_world(f.fact_id for f in UNIVERSE)
# Low-equivalent pair at clearance 0 (projection = {temp-reading-ok, power-ok}):
# the hidden support chain for 'shutdown-required' has length 1 vs 2.
WORLD_SHORT_CHAIN = make_world(["temp-reading-ok", "power-ok", "core-temp-critical"])
WORLD_LONG_CHAIN = make_world(["temp-reading-ok", "power-ok", "sensor-raw-anomaly"])


class TestProjectionAndPlaceholders(unittest.TestCase):
    def test_complete_explanation_when_fully_visible(self):
        result = explain(FULL, 1, "shutdown-required")
        self.assertEqual(result.status, "complete")
        self.assertEqual(result.counts["placeholders"], 0)
        self.assertIn("S-R2-shutdown-", result.transcript())
        self.assertIn("rule R2-shutdown@1", result.transcript())

    def test_restricted_placeholder_when_premises_hidden(self):
        result = explain(FULL, 0, "shutdown-required")
        self.assertEqual(result.status, "incomplete")
        text = result.transcript()
        self.assertIn("<restricted-support>", text)
        self.assertIn("incomplete", text)
        self.assertEqual(result.counts["placeholders"], 1)
        # Visible premises are still shown; hidden ones are not named.
        self.assertIn("system-nominal", text)
        for hidden in RESTRICTED_IDS:
            self.assertNotIn(hidden, text)

    def test_counts_carry_no_hidden_tallies(self):
        result = explain(FULL, 0, "shutdown-required")
        self.assertEqual(set(result.counts), {"facts", "steps", "placeholders", "depth"})

    def test_error_indistinguishable_for_hidden_and_unknown(self):
        hidden = explain(FULL, 0, "manual-override")
        unknown = explain(FULL, 0, "nonexistent-id")
        self.assertEqual(hidden.status, "error")
        self.assertEqual(unknown.status, "error")
        self.assertEqual(
            hidden.error.replace("manual-override", "<Q>"),
            unknown.error.replace("nonexistent-id", "<Q>"),
        )


class TestNoninterference(unittest.TestCase):
    def test_low_equivalence_relation(self):
        self.assertTrue(low_equivalent(WORLD_SHORT_CHAIN, WORLD_LONG_CHAIN, 0))
        self.assertFalse(low_equivalent(FULL, WORLD_SHORT_CHAIN, 1))

    def test_secure_renderer_identical_on_low_equivalent_pair(self):
        for query in QUERIES:
            self.assertEqual(
                explain(WORLD_SHORT_CHAIN, 0, query).transcript(),
                explain(WORLD_LONG_CHAIN, 0, query).transcript(),
                msg=f"query={query}",
            )

    def test_chain_length_does_not_leak_hidden_count(self):
        short = explain(WORLD_SHORT_CHAIN, 0, "shutdown-required")
        long = explain(WORLD_LONG_CHAIN, 0, "shutdown-required")
        self.assertEqual(short.counts, long.counts)
        self.assertEqual(short.counts["depth"], long.counts["depth"])

    def test_naive_renderer_leaks_on_same_pair(self):
        short = naive_explain(WORLD_SHORT_CHAIN, 0, "shutdown-required")
        long = naive_explain(WORLD_LONG_CHAIN, 0, "shutdown-required")
        self.assertNotEqual(short.transcript(), long.transcript())
        self.assertNotEqual(short.counts["steps_total"], long.counts["steps_total"])

    def test_finite_domain_secure_renderer_has_zero_violations(self):
        violations = find_violations(explain, all_worlds(), 0, QUERIES)
        self.assertEqual(violations, [])

    def test_finite_domain_naive_renderer_is_caught(self):
        violations = find_violations(naive_explain, all_worlds(), 0, QUERIES)
        self.assertGreater(len(violations), 0)

    def test_no_hidden_ids_in_any_public_output(self):
        for world in all_worlds():
            for query in ("temp-reading-ok", "power-ok", "system-nominal",
                          "shutdown-required", "nonexistent-id"):
                text = explain(world, 0, query).transcript()
                for hidden in RESTRICTED_IDS:
                    self.assertNotIn(hidden, text, msg=f"query={query}")


class TestDebugCacheEvidence(unittest.TestCase):
    def test_debug_follows_projection(self):
        short = explain(WORLD_SHORT_CHAIN, 0, "shutdown-required")
        long = explain(WORLD_LONG_CHAIN, 0, "shutdown-required")
        self.assertEqual(short.debug, long.debug)
        for line in short.debug:
            for hidden in RESTRICTED_IDS:
                self.assertNotIn(hidden, line)

    def test_cache_isolated_per_clearance(self):
        cache = ExplanationCache()
        explain(WORLD_SHORT_CHAIN, 0, "shutdown-required", cache=cache)
        self.assertEqual(cache.size(0), 1)
        self.assertEqual(cache.size(1), 0)
        explain(FULL, 1, "shutdown-required", cache=cache)
        self.assertEqual(cache.size(0), 1)
        self.assertEqual(cache.size(1), 1)

    def test_cache_shares_only_within_same_clearance(self):
        cache = ExplanationCache()
        first = explain(WORLD_SHORT_CHAIN, 0, "shutdown-required", cache=cache)
        second = explain(WORLD_LONG_CHAIN, 0, "shutdown-required", cache=cache)
        self.assertIs(first, second)  # low-equivalent at same clearance: safe hit
        third = explain(WORLD_SHORT_CHAIN, 1, "shutdown-required", cache=cache)
        self.assertIsNot(first, third)  # different clearance: isolated

    def test_evidence_replays_and_detects_tampering(self):
        result = explain(FULL, 0, "shutdown-required")
        self.assertIsNotNone(result.evidence)
        self.assertTrue(replay(result.evidence))
        tampered = dataclasses.replace(
            result.evidence, transcript_sha256="0" * 64
        )
        self.assertFalse(replay(tampered))

    def test_stable_ids_and_versions_across_runs(self):
        again = explain(FULL, 0, "shutdown-required")
        once = explain(FULL, 0, "shutdown-required")
        self.assertEqual(once.transcript(), again.transcript())
        self.assertEqual(once.evidence.transcript_sha256, again.evidence.transcript_sha256)
        complete = explain(FULL, 1, "shutdown-required")
        self.assertIn(" v1 ", complete.transcript())  # step version is rendered


if __name__ == "__main__":
    unittest.main()

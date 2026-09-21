from __future__ import annotations

import unittest
from itertools import permutations

from ab_pattern import Event, PatternMatcher
from reference import reference_matches


def e(kind: str, ts: int, seq: int, key: str = "k", source: str = "s", tie=None):
    return Event(kind, key, ts, source, seq, tie)


def statuses(matcher: PatternMatcher, events):
    result = []
    for event in events:
        result.extend(output.status for output in matcher.ingest(event))
    return result


def final_matches(matcher: PatternMatcher, watermark: int):
    matches = set()
    for output in matcher.advance_watermark(watermark):
        if output.status == "FINAL_MATCH":
            matches.add(output.match_id)
    return matches


class PatternSemanticsTest(unittest.TestCase):
    def test_required_example_and_correction(self):
        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=0)
        outputs = []
        outputs += matcher.ingest(e("A", 0, 1))
        outputs += matcher.ingest(e("A", 1, 2))
        outputs += matcher.ingest(e("B", 3, 3))

        self.assertEqual(
            [output.status for output in outputs],
            ["PROVISIONAL_MATCH", "PROVISIONAL_MATCH"],
        )

        revoked = matcher.ingest(e("C", 2, 4))
        self.assertEqual([output.status for output in revoked], ["REVOKED", "REVOKED"])

        matcher.ingest(e("B", 6, 5))
        finalized = matcher.advance_watermark(6)
        self.assertEqual(
            [output.status for output in finalized],
            ["NO_MATCH", "NO_MATCH"],
        )

    def test_b_six_matches_only_recent_a_without_c(self):
        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=0)
        matcher.ingest(e("A", 0, 1))
        matcher.ingest(e("A", 1, 2))
        matcher.ingest(e("B", 6, 3))
        finalized = matcher.advance_watermark(6)
        self.assertEqual(
            [output.status for output in finalized],
            ["NO_MATCH", "FINAL_MATCH"],
        )
        self.assertEqual(finalized[-1].a_id, ("s", 2))
        self.assertEqual(finalized[-1].b_id, ("s", 3))

    def test_inclusive_endpoint(self):
        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 0, 1))
        self.assertEqual(statuses(matcher, [e("B", 5, 2)]), ["PROVISIONAL_MATCH"])
        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 0, 1))
        self.assertEqual(statuses(matcher, [e("B", 6, 2)]), [])

    def test_same_timestamp_default_order_is_a_c_b(self):
        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 5, 1))
        matcher.ingest(e("C", 5, 2))
        self.assertEqual(statuses(matcher, [e("B", 5, 3)]), [])

    def test_explicit_tie_can_change_same_timestamp_order(self):
        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 5, 1, tie=1))
        matcher.ingest(e("C", 5, 2, tie=0))
        self.assertEqual(statuses(matcher, [e("B", 5, 3, tie=2)]), ["PROVISIONAL_MATCH"])

        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 5, 4, tie=0))
        matcher.ingest(e("C", 5, 5, tie=3))
        self.assertEqual(statuses(matcher, [e("B", 5, 6, tie=2)]), ["PROVISIONAL_MATCH"])

    def test_identity_deduplication(self):
        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 0, 1, key="one"))
        self.assertEqual(matcher.ingest(e("A", 0, 1, key="one")), [])
        self.assertEqual(matcher.stats["duplicates"], 1)
        with self.assertRaises(ValueError):
            matcher.ingest(e("B", 0, 1, key="one"))

    def test_a_retraction_before_and_after_match(self):
        matcher = PatternMatcher(gap_ms=5)
        matcher.retract_a("k", ("s", 1), "control", 10, 0)
        matcher.ingest(e("A", 0, 1))
        matcher.ingest(e("B", 3, 2))
        self.assertEqual(matcher.advance_watermark(5), [])

        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=2)
        matcher.ingest(e("A", 0, 3))
        matcher.ingest(e("B", 3, 4))
        before = matcher.advance_watermark(5)
        self.assertEqual([x.status for x in before], ["FINAL_MATCH"])
        after = matcher.retract_a("k", ("s", 3), "control", 11, 5)
        self.assertEqual([x.status for x in after], ["LATE_NO_MATCH"])

    def test_retraction_control_event_is_deduplicated(self):
        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 0, 1))
        matcher.ingest(e("B", 3, 2))
        first = matcher.retract_a("k", ("s", 1), "control", 20, 4)
        second = matcher.retract_a("k", ("s", 1), "control", 20, 4)
        self.assertEqual([x.status for x in first], ["REVOKED"])
        self.assertEqual(second, [])
        self.assertEqual(matcher.stats["duplicates"], 1)

    def test_late_c_revokes_final_match_but_out_of_scope_is_reported(self):
        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=2)
        matcher.ingest(e("A", 0, 1))
        matcher.ingest(e("B", 3, 2))
        self.assertEqual(
            [x.status for x in matcher.advance_watermark(5)],
            ["FINAL_MATCH"],
        )
        corrected = matcher.ingest(e("C", 3, 3))
        self.assertEqual([x.status for x in corrected], ["LATE_NO_MATCH"])

        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=1)
        matcher.ingest(e("A", 0, 4))
        matcher.ingest(e("B", 3, 5))
        matcher.advance_watermark(5)
        rejected = matcher.ingest(e("C", 3, 6))
        self.assertEqual([x.status for x in rejected], ["OUT_OF_SCOPE_LATE"])
        self.assertEqual(matcher.stats["final_match"], 1)

    def test_no_b_finalizes_and_state_is_bounded(self):
        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=1)
        matcher.ingest(e("A", 0, 1))
        self.assertEqual([x.status for x in matcher.advance_watermark(5)], ["NO_MATCH"])
        self.assertEqual(matcher.retained_events(), 1)
        self.assertEqual(matcher.advance_watermark(6), [])
        self.assertEqual(matcher.retained_events(), 1)
        self.assertEqual(matcher.advance_watermark(7), [])
        self.assertEqual(matcher.retained_events(), 0)

    def test_provisional_release_is_bounded_by_watermark(self):
        matcher = PatternMatcher(gap_ms=5)
        matcher.ingest(e("A", 0, 1))
        matcher.ingest(e("A", 1, 2))
        matcher.ingest(e("B", 3, 3))
        self.assertEqual(matcher.pending_match_count(), 2)

        matcher.advance_watermark(5)
        self.assertEqual(matcher.pending_match_count(), 1)
        self.assertEqual(matcher.stats["final_match"], 1)

        matcher.advance_watermark(6)
        self.assertEqual(matcher.pending_match_count(), 0)
        self.assertEqual(matcher.stats["final_match"], 2)
        self.assertEqual(matcher.stats["no_match"], 0)

        matcher.advance_watermark(8)
        self.assertEqual(matcher.retained_events(), 1)
        matcher.advance_watermark(9)
        self.assertEqual(matcher.retained_events(), 0)

    def test_reference_final_consistency_across_orders_and_chunks(self):
        scenarios = [
            [
                e("A", 0, 1),
                e("A", 1, 2),
                e("B", 3, 3),
                e("C", 2, 4),
                e("B", 6, 5),
            ],
            [
                e("A", 0, 1, key="x"),
                e("B", 5, 2, key="x"),
                e("B", 6, 3, key="x"),
                e("C", 4, 4, key="x"),
                e("A", 10, 5, key="x"),
                e("B", 10, 6, key="x"),
            ],
            [
                e("A", 7, 1, tie=1),
                e("B", 7, 2, tie=3),
                e("C", 7, 3, tie=0),
                e("A", 7, 4, tie=2),
            ],
        ]

        for scenario in scenarios:
            expected = reference_matches(scenario, set(), gap_ms=5)
            keys = [event.key for event in scenario if event.kind == "A"]
            for order in permutations(scenario):
                for chunks in range(1, len(order) + 1):
                    buffered = self._replay_buffered(list(order), chunks)
                    self.assertEqual(buffered, expected)

                    rolling = self._replay_rolling(list(order))
                    self.assertEqual(rolling, expected)

    def _replay_buffered(self, ordered_events, chunks):
        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=100)
        matches = set()
        size = (len(ordered_events) + chunks - 1) // chunks
        for start in range(0, len(ordered_events), size):
            for event in ordered_events[start : start + size]:
                for output in matcher.ingest(event):
                    if output.status in {"PROVISIONAL_MATCH", "LATE_MATCH"}:
                        matches.add(output.match_id)
                    elif output.status == "REVOKED":
                        matches.discard(output.match_id)
        max_ts = max(event.ts for event in ordered_events)
        for output in matcher.advance_watermark(max_ts + 6):
            if output.status == "FINAL_MATCH":
                matches.add(output.match_id)
            elif output.status == "NO_MATCH":
                pass
        return {item for item in matches if item is not None}

    def _replay_rolling(self, ordered_events):
        matcher = PatternMatcher(gap_ms=5, allowed_lateness_ms=100)
        matches = set()
        max_seen = None
        for event in ordered_events:
            for output in matcher.ingest(event):
                if output.status in {"PROVISIONAL_MATCH", "LATE_MATCH"}:
                    matches.add(output.match_id)
                elif output.status in {"REVOKED", "LATE_NO_MATCH"}:
                    matches.discard(output.match_id)
            max_seen = event.ts if max_seen is None else max(max_seen, event.ts)
            for output in matcher.advance_watermark(max_seen):
                if output.status == "FINAL_MATCH":
                    matches.add(output.match_id)
        for output in matcher.advance_watermark(max_seen + 6):
            if output.status == "FINAL_MATCH":
                matches.add(output.match_id)
        return {item for item in matches if item is not None}


if __name__ == "__main__":
    unittest.main()

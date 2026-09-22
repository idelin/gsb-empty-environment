from __future__ import annotations

import random
import unittest

from sample_alignment import (
    AlignmentOperation,
    AlignmentStore,
    AnchorAdded,
    AnchorRetracted,
    ChangeReason,
    SampleAdded,
    SampleRetracted,
    WatermarkAdvanced,
    reference_snapshot,
)


def current_views(store: AlignmentStore) -> dict[str, tuple]:
    return {
        anchor_id: (
            result.anchor_timestamp,
            result.as_table(),
            result.missing_sources,
            result.finalized,
        )
        for anchor_id, result in store.current_alignment().items()
    }


def reference_views(events, sources, tolerance) -> dict[str, tuple]:
    return {
        anchor_id: (
            result.anchor_timestamp,
            result.as_table(),
            result.missing_sources,
            result.finalized,
        )
        for anchor_id, result in reference_snapshot(events, sources, tolerance).items()
    }


class AlignmentTests(unittest.TestCase):
    def test_late_better_sample_replaces_earlier_equal_distance_choice(self):
        store = AlignmentStore(["alt"], 2)

        events = store.add_sample("s8", "alt", 8, 1)
        self.assertEqual(events, [])

        events = store.add_anchor("m10", 10, 1)
        self.assertEqual(events[0].operation, AlignmentOperation.UPSERT)
        self.assertEqual(events[0].result.sample_id("alt"), "s8")
        self.assertEqual(events[0].result.version, 1)

        events = store.add_sample("s12", "alt", 12, 2)
        self.assertEqual(events, [])

        events = store.add_sample("s9", "alt", 9, 3)
        self.assertEqual(events[0].operation, AlignmentOperation.UPSERT)
        self.assertEqual(events[0].reason, ChangeReason.SAMPLE_ADDED)
        self.assertEqual(events[0].result.sample_id("alt"), "s9")
        self.assertEqual(events[0].previous_version, 1)
        self.assertEqual(events[0].result.version, 2)

    def test_equal_distance_chooses_earlier_timestamp_then_smaller_seq(self):
        store = AlignmentStore(["alt"], 2)
        store.add_sample("later", "alt", 12, 1)
        store.add_sample("earlier", "alt", 8, 5)
        store.add_anchor("m10", 10, 1)
        self.assertEqual(store.current_alignment()["m10"].sample_id("alt"), "earlier")

        store.add_sample("earlier_tie_seq", "alt", 8, 2)
        self.assertEqual(
            store.current_alignment()["m10"].sample_id("alt"),
            "earlier_tie_seq",
        )

    def test_multiple_auxiliary_sources_and_missing_marker(self):
        store = AlignmentStore(["gps", "imu"], {"gps": 2, "imu": 1})
        store.add_sample("gps8", "gps", 8, 1)
        store.add_anchor("m10", 10, 1)

        result = store.current_alignment()["m10"]
        self.assertEqual(result.as_table(), {"gps": "gps8", "imu": None})
        self.assertEqual(result.missing_sources, ("imu",))

        store.add_sample("imu9", "imu", 9, 1)
        self.assertEqual(
            store.current_alignment()["m10"].as_table(),
            {"gps": "gps8", "imu": "imu9"},
        )
        self.assertEqual(store.current_alignment()["m10"].missing_sources, ())

    def test_one_auxiliary_sample_can_match_multiple_anchors(self):
        store = AlignmentStore(["alt"], 5)
        store.add_sample("shared", "alt", 10, 1)
        store.add_anchor("m8", 8, 1)
        store.add_anchor("m12", 12, 2)

        self.assertEqual(store.current_alignment()["m8"].sample_id("alt"), "shared")
        self.assertEqual(store.current_alignment()["m12"].sample_id("alt"), "shared")

    def test_sample_retraction_falls_back_to_second_best_candidate(self):
        store = AlignmentStore(["alt"], 4)
        store.add_sample("best", "alt", 9, 1)
        store.add_sample("fallback", "alt", 8, 2)
        store.add_anchor("m10", 10, 1)
        self.assertEqual(store.current_alignment()["m10"].sample_id("alt"), "best")

        events = store.retract_sample("best")
        self.assertEqual(events[0].operation, AlignmentOperation.UPSERT)
        self.assertEqual(events[0].reason, ChangeReason.SAMPLE_RETRACTED)
        self.assertEqual(events[0].result.sample_id("alt"), "fallback")

    def test_retraction_without_fallback_marks_source_missing(self):
        store = AlignmentStore(["alt"], 4)
        store.add_sample("only", "alt", 9, 1)
        store.add_anchor("m10", 10, 1)
        store.retract_sample("only")

        result = store.current_alignment()["m10"]
        self.assertEqual(result.sample_id("alt"), None)
        self.assertEqual(result.missing_sources, ("alt",))

    def test_sample_outside_tolerance_is_missing(self):
        store = AlignmentStore(["alt"], 2)
        store.add_sample("far", "alt", 7, 1)
        store.add_anchor("m10", 10, 1)

        result = store.current_alignment()["m10"]
        self.assertEqual(result.sample_id("alt"), None)
        self.assertEqual(result.missing_sources, ("alt",))
        self.assertEqual(store.retained_candidate_count, 1)

        store.advance_watermark(9)
        self.assertEqual(store.retained_candidate_count, 0)

    def test_anchor_retraction_emits_delete_and_removes_row(self):
        store = AlignmentStore(["alt"], 2)
        store.add_sample("s9", "alt", 9, 1)
        store.add_anchor("m10", 10, 1)

        events = store.retract_anchor("m10")
        self.assertEqual(events[0].operation, AlignmentOperation.DELETE)
        self.assertEqual(events[0].reason, ChangeReason.ANCHOR_RETRACTED)
        self.assertEqual(events[0].result.anchor_id, "m10")
        self.assertNotIn("m10", store.current_alignment())

    def test_watermark_finalizes_and_late_changes_cannot_rewrite(self):
        store = AlignmentStore(["alt"], 2)
        store.add_sample("s8", "alt", 8, 1)
        store.add_anchor("m10", 10, 1)

        finalized = store.advance_watermark(12)
        self.assertEqual(finalized[0].reason, ChangeReason.WATERMARK)
        self.assertTrue(finalized[0].result.finalized)

        self.assertEqual(store.process_event(SampleAdded("s9", "alt", 9, 2)), [])
        self.assertEqual(store.process_event(SampleRetracted("s8")), [])
        self.assertEqual(store.current_alignment()["m10"].sample_id("alt"), "s8")

    def test_finalized_anchor_cannot_be_retracted(self):
        store = AlignmentStore(["alt"], 2)
        store.add_anchor("m10", 10, 1)
        store.advance_watermark(12)

        self.assertEqual(store.retract_anchor("m10"), [])
        self.assertIn("m10", store.current_alignment())

    def test_watermark_retains_candidate_that_may_match_future_anchor(self):
        store = AlignmentStore(["alt"], 2)
        store.add_sample("s8", "alt", 8, 1)
        store.advance_watermark(8)
        self.assertEqual(store.retained_candidate_count, 1)

        store.add_anchor("m10", 10, 1)
        self.assertEqual(store.current_alignment()["m10"].sample_id("alt"), "s8")

        store.advance_watermark(12)
        self.assertEqual(store.retained_candidate_count, 0)

    def test_gc_retains_until_last_open_anchor_is_closed(self):
        store = AlignmentStore(["alt"], 5)
        store.add_sample("s10", "alt", 10, 1)
        store.add_anchor("a8", 8, 1)
        store.add_anchor("a14", 14, 2)
        self.assertEqual(store.retained_candidate_count, 1)

        store.retract_anchor("a8")
        self.assertEqual(store.retained_candidate_count, 1)

        store.advance_watermark(19)
        self.assertEqual(store.retained_candidate_count, 0)

    def test_event_log_replays_current_alignment_table(self):
        store = AlignmentStore(["alt"], 2)
        store.add_sample("s8", "alt", 8, 1)
        store.add_anchor("m10", 10, 1)
        store.add_sample("s12", "alt", 12, 2)
        store.add_sample("s9", "alt", 9, 3)
        store.advance_watermark(12)

        table = {}
        versions = {}
        for event in store.event_log:
            if event.operation == AlignmentOperation.DELETE:
                table.pop(event.result.anchor_id, None)
                versions.pop(event.result.anchor_id, None)
            else:
                table[event.result.anchor_id] = event.result
                versions[event.result.anchor_id] = event.result.version

        self.assertEqual(table, store.current_alignment())
        self.assertEqual(versions, {"m10": 3})

    def test_single_event_and_batch_interfaces_return_identical_events(self):
        events = [
            SampleAdded("s8", "alt", 8, 1),
            AnchorAdded("m10", 10, 1),
            SampleAdded("s9", "alt", 9, 2),
            WatermarkAdvanced(12),
        ]
        one_by_one = AlignmentStore(["alt"], 2)
        batched = AlignmentStore(["alt"], 2)

        first = one_by_one.process_events(events[:1])
        for event in events[1:]:
            first.extend(one_by_one.process_event(event))
        second = batched.process_events(events)

        self.assertEqual(first, second)
        self.assertEqual(current_views(one_by_one), current_views(batched))

    def test_random_arrival_order_matches_independent_scan(self):
        rng = random.Random(20260922)
        sources = ("gps", "imu")
        tolerance = {"gps": 2, "imu": 1}

        for iteration in range(160):
            events = []
            anchor_count = rng.randint(1, 7)
            sample_count = rng.randint(0, 12)
            retractions = []

            for index in range(anchor_count):
                events.append(AnchorAdded(f"a{index}", rng.randint(0, 12), index))

            for index in range(sample_count):
                source = sources[rng.randrange(len(sources))]
                event = SampleAdded(f"s{index}", source, rng.randint(0, 14), index)
                events.append(event)
                if rng.random() < 0.22:
                    retractions.append(SampleRetracted(event.sample_id))

            if rng.random() < 0.7:
                retractions.append(AnchorRetracted(f"a{rng.randrange(anchor_count)}"))

            events.extend(retractions)
            rng.shuffle(events)

            watermarks = []
            if rng.random() < 0.8:
                levels = rng.sample(range(2, 19), rng.randint(1, 3))
                watermarks = [WatermarkAdvanced(level) for level in sorted(levels)]

            if watermarks:
                positions = sorted(rng.sample(range(len(events) + 1), len(watermarks)))
                expanded = list(events)
                offset = 0
                for position, watermark in zip(positions, watermarks):
                    expanded.insert(position + offset, watermark)
                    offset += 1
                events = expanded

            incremental = AlignmentStore(sources, tolerance)
            emitted = incremental.process_events(events)
            batch = AlignmentStore(sources, tolerance)
            self.assertEqual(batch.process_events(events), emitted)

            expected = reference_views(events, sources, tolerance)
            actual = current_views(incremental)
            if actual != expected:
                raise AssertionError(
                    f"iteration {iteration}\nevents={events!r}\nactual={actual!r}\nexpected={expected!r}"
                )


if __name__ == "__main__":
    unittest.main()

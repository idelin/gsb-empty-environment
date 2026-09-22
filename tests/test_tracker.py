import random
import unittest

from occupancy_recovery import (
    DynamicIntervalIndex,
    EvidenceSegment,
    OccupancyTracker,
    PeakPeriod,
)
from occupancy_recovery.interval_index import _push


def brute_force_peak(intervals, lo, hi):
    deltas = {}
    for interval in intervals:
        start = max(interval.start, lo)
        end = min(interval.end, hi)
        if start < end:
            deltas.setdefault(start, []).append((1, interval.occupancy_id))
            deltas.setdefault(end, []).append((-1, interval.occupancy_id))

    coordinates = sorted({lo, hi, *deltas})
    active = set()
    evidence = []

    for point, next_point in zip(coordinates, coordinates[1:]):
        for delta, occupancy_id in sorted(deltas.get(point, []), key=lambda item: item[1]):
            if delta > 0:
                active.add(occupancy_id)
            else:
                active.remove(occupancy_id)

        if point < next_point:
            evidence.append(
                EvidenceSegment(
                    point,
                    next_point,
                    len(active),
                    tuple(sorted(active, key=repr)),
                )
            )

    peak_value = max((segment.occupancy for segment in evidence), default=0)
    peak_evidence = tuple(
        segment for segment in evidence if segment.occupancy == peak_value
    )
    periods = []
    for segment in peak_evidence:
        if periods and periods[-1].end == segment.start:
            old = periods[-1]
            periods[-1] = PeakPeriod(old.start, segment.end, old.occupancy)
        else:
            periods.append(PeakPeriod(segment.start, segment.end, segment.occupancy))

    return peak_value, tuple(periods), peak_evidence


class IntervalIndexTest(unittest.TestCase):
    def test_touching_half_open_intervals_do_not_overlap(self):
        index = DynamicIntervalIndex(seed=1)
        index.add_interval("A", 0, 5)
        index.add_interval("B", 5, 9)

        result = index.query(0, 10)

        self.assertEqual(result.peak, 1)
        self.assertEqual(result.periods, (PeakPeriod(0, 9, 1),))
        self.assertEqual(
            result.evidence_segments,
            (
                EvidenceSegment(0, 5, 1, ("A",)),
                EvidenceSegment(5, 9, 1, ("B",)),
            ),
        )

    def test_add_overlap_then_remove(self):
        index = DynamicIntervalIndex(seed=2)
        index.add_interval("A", 0, 5)
        index.add_interval("B", 5, 9)
        index.add_interval("C", 4, 6)

        result = index.query(0, 10)
        self.assertEqual(result.peak, 2)
        self.assertEqual(result.periods, (PeakPeriod(4, 6, 2),))

        index.remove_interval("C", 4, 6)
        result = index.query(0, 10)
        self.assertEqual(result.peak, 1)
        self.assertEqual(index.size, 3)

    def test_interval_inserted_inside_existing_segment_inherits_members(self):
        index = DynamicIntervalIndex(seed=3)
        index.add_interval("A", 0, 10)
        index.add_interval("B", 4, 6)
        index.remove_interval("B", 4, 6)

        result = index.query(0, 10)
        self.assertEqual(result.peak, 1)
        self.assertEqual(
            result.evidence_segments,
            (EvidenceSegment(0, 10, 1, ("A",)),),
        )
        self.assertEqual(index.size, 2)


class TrackerTest(unittest.TestCase):
    def test_end_before_start_is_then_completed(self):
        tracker = OccupancyTracker(seed=4)
        end_result = tracker.submit_end("B", 9)
        snapshot = tracker.snapshot()

        self.assertEqual(end_result.status, tracker.RECORDED)
        self.assertEqual(len(snapshot.unmatched_ends), 1)
        self.assertEqual(snapshot.unmatched_ends[0].occupancy_id, "B")

        start_result = tracker.submit_start("B", 5)
        self.assertEqual(start_result.status, tracker.COMPLETED)
        self.assertEqual(start_result.interval.occupancy_id, "B")
        self.assertEqual(tracker.snapshot().unmatched_ends, ())

    def test_duplicate_end_is_explicit_and_does_not_change_state(self):
        tracker = OccupancyTracker(seed=5)
        tracker.submit_start("A", 0)
        tracker.submit_end("A", 5)
        duplicate = tracker.submit_end("A", 5)

        self.assertEqual(duplicate.status, tracker.DUPLICATE)
        self.assertEqual(tracker.get_interval("A").end, 5)
        self.assertEqual(tracker.peak(0, 10).peak, 1)

    def test_endpoint_correction_moves_old_interval_outside_query(self):
        tracker = OccupancyTracker(seed=6)
        tracker.submit_start("A", 2)
        tracker.submit_end("A", 8)
        self.assertEqual(tracker.peak(0, 10).peak, 1)

        result = tracker.replace_interval("A", 12, 18)
        query = tracker.peak(0, 10)

        self.assertEqual(result.status, tracker.CORRECTED)
        self.assertEqual(result.previous_interval.start, 2)
        self.assertEqual(query.peak, 0)
        self.assertEqual(query.periods, (PeakPeriod(0, 10, 0),))
        self.assertEqual(tracker.peak(10, 20).peak, 1)
        self.assertEqual(tracker.index.size, 2)

    def test_withdraw_removes_interval_and_rejects_late_matching_records(self):
        tracker = OccupancyTracker(seed=7)
        tracker.submit_start("A", 0)
        tracker.submit_end("A", 5)
        self.assertEqual(tracker.withdraw("A").status, tracker.REVOKED)

        self.assertEqual(tracker.peak(0, 10).peak, 0)
        self.assertEqual(tracker.submit_end("A", 8).status, tracker.RECORD_REVOKED)
        self.assertEqual(tracker.peak(0, 10).peak, 0)
        self.assertEqual(tracker.index.size, 0)

    def test_invalid_end_before_start_does_not_destroy_existing_interval(self):
        tracker = OccupancyTracker(seed=8)
        tracker.submit_start("A", 0)
        tracker.submit_end("A", 5)
        rejected = tracker.submit_end("A", -2)

        self.assertEqual(rejected.status, tracker.INVALID_REJECTED)
        self.assertEqual(tracker.get_interval("A").end, 5)
        self.assertEqual(tracker.peak(0, 10).peak, 1)

    def test_unmatched_invalid_pair_is_listed_separately_then_can_recover(self):
        tracker = OccupancyTracker(seed=9)
        tracker.submit_end("X", 1)
        invalid = tracker.submit_start("X", 3)

        self.assertEqual(invalid.status, tracker.INVALID_RECORDED)
        self.assertEqual(len(tracker.snapshot().invalid_records), 1)
        self.assertEqual(tracker.peak(0, 10).peak, 0)

        recovered = tracker.submit_start("X", 0)
        self.assertEqual(recovered.status, tracker.COMPLETED)
        self.assertEqual(tracker.snapshot().invalid_records, ())
        self.assertEqual(tracker.peak(0, 10).peak, 1)

    def test_returns_all_separated_peak_periods(self):
        tracker = OccupancyTracker(seed=11)
        tracker.submit_start("A", 0)
        tracker.submit_end("A", 2)
        tracker.submit_start("B", 0)
        tracker.submit_end("B", 10)
        tracker.submit_start("C", 8)
        tracker.submit_end("C", 10)

        result = tracker.peak(0, 10)

        self.assertEqual(result.peak, 2)
        self.assertEqual(
            result.periods,
            (PeakPeriod(0, 2, 2), PeakPeriod(8, 10, 2)),
        )
        self.assertEqual(
            result.evidence_segments,
            (
                EvidenceSegment(0, 2, 2, ("A", "B")),
                EvidenceSegment(8, 10, 2, ("B", "C")),
            ),
        )

    def test_evidence_tracks_individual_contributing_ids(self):
        tracker = OccupancyTracker(seed=10)
        tracker.submit_start("A", 0)
        tracker.submit_end("A", 8)
        tracker.submit_start("B", 2)
        tracker.submit_end("B", 6)
        tracker.submit_start("C", 4)
        tracker.submit_end("C", 10)

        result = tracker.peak(0, 12)

        self.assertEqual(result.peak, 3)
        self.assertEqual(result.periods, (PeakPeriod(4, 6, 3),))
        self.assertEqual(
            result.evidence_segments,
            (EvidenceSegment(4, 6, 3, ("A", "B", "C")),),
        )


class RandomDifferentialTest(unittest.TestCase):
    class ModelRecord:
        def __init__(self):
            self.status = "missing"
            self.start = None
            self.end = None

        @property
        def interval(self):
            if self.status != "active":
                return None
            return self.start, self.end

    def model_submit_start(self, records, active, occupancy_id, start):
        record = records.setdefault(occupancy_id, self.ModelRecord())
        if record.status == "revoked":
            return
        if record.start == start and record.status != "open_end":
            return

        if record.status == "active":
            end = record.end
            if start < end:
                active.pop(occupancy_id)
                record.start = start
                active[occupancy_id] = (start, end)
            return

        if record.status == "open_end":
            end = record.end
            record.start = start
            if start < end:
                record.status = "active"
                active[occupancy_id] = (start, end)
            else:
                record.status = "invalid"
            return

        if record.status == "invalid":
            end = record.end
            record.start = start
            if start < end:
                record.status = "active"
                active[occupancy_id] = (start, end)
            return

        record.status = "open_start"
        record.start = start

    def model_submit_end(self, records, active, occupancy_id, end):
        record = records.setdefault(occupancy_id, self.ModelRecord())
        if record.status == "revoked":
            return
        if record.end == end and record.status != "open_start":
            return

        if record.status == "active":
            start = record.start
            if start < end:
                active.pop(occupancy_id)
                record.end = end
                active[occupancy_id] = (start, end)
            return

        if record.status == "open_start":
            start = record.start
            record.end = end
            if start < end:
                record.status = "active"
                active[occupancy_id] = (start, end)
            else:
                record.status = "invalid"
            return

        if record.status == "invalid":
            start = record.start
            record.end = end
            if start < end:
                record.status = "active"
                active[occupancy_id] = (start, end)
            return

        record.status = "open_end"
        record.end = end

    def model_replace(self, records, active, occupancy_id, start, end):
        record = records.get(occupancy_id)
        if record is None or record.status == "revoked":
            return
        if record.status == "active" and record.start == start and record.end == end:
            return
        if record.status == "invalid" and record.start == start and record.end == end:
            return

        if not start < end:
            if record.status == "active":
                return
            record.start = start
            record.end = end
            record.status = "invalid"
            return

        if record.status == "active":
            active.pop(occupancy_id)
        record.start = start
        record.end = end
        record.status = "active"
        active[occupancy_id] = (start, end)

    def model_withdraw(self, records, active, occupancy_id):
        record = records.get(occupancy_id)
        if record is None or record.status == "revoked":
            return
        if record.status == "active":
            active.pop(occupancy_id)
        record.status = "revoked"

    def assert_treap_invariants(self, root):
        if root is None:
            return

        def check(node, lower, upper):
            self.assertIsNotNone(node)
            _push(node)
            if lower is not None:
                self.assertLess(lower, node.key)
            if upper is not None:
                self.assertLess(node.key, upper)

            expected_size = 1
            expected_peak = len(node.members)
            expected_min = node.key
            expected_max = node.key

            if node.left is not None:
                check(node.left, lower, node.key)
                expected_size += node.left.size
                expected_peak = max(expected_peak, node.left.peak)
                expected_min = node.left.min_key
            if node.right is not None:
                check(node.right, node.key, upper)
                expected_size += node.right.size
                expected_peak = max(expected_peak, node.right.peak)
                expected_max = node.right.max_key

            self.assertEqual(node.size, expected_size)
            self.assertEqual(node.peak, expected_peak)
            self.assertEqual(node.min_key, expected_min)
            self.assertEqual(node.max_key, expected_max)

        check(root, None, None)


    def test_random_operations_match_endpoint_scan(self):
        for seed in range(500):
            rng = random.Random(1000 + seed)
            tracker = OccupancyTracker(seed=seed)
            records = {}
            active = {}
            ids = [f"id-{index}" for index in range(8)]

            for _ in range(260):
                occupancy_id = rng.choice(ids)
                action = rng.randrange(5)

                if action == 0:
                    start = rng.randrange(1, 13)
                    tracker.submit_start(occupancy_id, start)
                    self.model_submit_start(records, active, occupancy_id, start)
                elif action == 1:
                    end = rng.randrange(1, 13)
                    tracker.submit_end(occupancy_id, end)
                    self.model_submit_end(records, active, occupancy_id, end)
                elif action == 2:
                    start = rng.randrange(0, 14)
                    end = rng.randrange(0, 14)
                    tracker.replace_interval(occupancy_id, start, end)
                    self.model_replace(records, active, occupancy_id, start, end)
                elif action == 3:
                    tracker.withdraw(occupancy_id)
                    self.model_withdraw(records, active, occupancy_id)
                else:
                    lo = rng.randrange(0, 12)
                    hi = rng.randrange(lo + 1, 14)
                    intervals = [
                        type("Interval", (), {"occupancy_id": key, "start": value[0], "end": value[1]})()
                        for key, value in active.items()
                    ]
                    expected = brute_force_peak(intervals, lo, hi)
                    actual = tracker.peak(lo, hi)
                    self.assertEqual(actual.peak, expected[0])
                    self.assertEqual(actual.periods, expected[1])
                    self.assertEqual(actual.evidence_segments, expected[2])

                self.assert_treap_invariants(tracker.index._root)


if __name__ == "__main__":
    unittest.main()

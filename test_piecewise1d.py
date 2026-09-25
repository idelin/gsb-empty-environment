import math
import random
import unittest

from piecewise1d import (
    ChangeReport,
    DerivativeResult,
    Evidence,
    Feature,
    InverseSolution,
    InterpolationGapError,
    Measurement,
    Node,
    OutsideDomainError,
    PiecewiseModel,
    Snapshot,
)


def assert_close(testcase, actual, expected, tol=1e-10):
    testcase.assertTrue(math.isfinite(actual))
    testcase.assertLessEqual(abs(actual - expected), tol)


def dense_winners(nodes_by_x):
    result = {}
    for x, nodes in nodes_by_x.items():
        result[x] = min(nodes, key=lambda n: (-n.priority, -n.version, n.node_id))
    return result


def dense_value(winners, x):
    xs = sorted(winners)
    if x not in xs and (x < xs[0] or x > xs[-1]):
        raise OutsideDomainError("outside")
    if x in winners:
        node = winners[x]
        if node.measurement is None:
            raise InterpolationGapError("unknown")
        return node.measurement.lower, node.measurement.upper
    right_pos = next(i for i, value in enumerate(xs) if value > x)
    left = winners[xs[right_pos - 1]]
    right = winners[xs[right_pos]]
    if left.measurement is None or right.measurement is None or left.hard_break:
        raise InterpolationGapError("gap")
    t = (x - left.x) / (right.x - left.x)
    lo = left.measurement.lower * (1 - t) + right.measurement.lower * t
    hi = left.measurement.upper * (1 - t) + right.measurement.upper * t
    return min(lo, hi), max(lo, hi)


def dense_segment_slope_bounds(left, right):
    width = right.x - left.x
    slopes = [
        (right.measurement.lower - left.measurement.lower) / width,
        (right.measurement.lower - left.measurement.upper) / width,
        (right.measurement.upper - left.measurement.lower) / width,
        (right.measurement.upper - left.measurement.upper) / width,
    ]
    return min(slopes), max(slopes)


def dense_slopes(winners, x):
    xs = sorted(winners)
    if x in winners:
        index = xs.index(x)
        if winners[x].measurement is None:
            raise InterpolationGapError("unknown")
        out = []
        if index > 0 and winners[xs[index - 1]].measurement is not None:
            left = winners[xs[index - 1]]
            right = winners[x]
            if not left.hard_break and not right.hard_break:
                out.extend(dense_segment_slope_bounds(left, right))
        if index + 1 < len(xs) and winners[xs[index + 1]].measurement is not None:
            left = winners[x]
            right = winners[xs[index + 1]]
            if not left.hard_break and not right.hard_break:
                out.extend(dense_segment_slope_bounds(left, right))
        if not out:
            raise InterpolationGapError("no slope")
        return min(out), max(out)
    raise AssertionError("interior x used below")


class PiecewiseModelTests(unittest.TestCase):
    def test_small_enumeration_matches_dense_reference(self):
        coordinates = [0.0, 1.0, 2.0]
        statuses = [
            (Measurement(0.0, 1.0), False, 0),
            (Measurement(-1.0, 1.0), False, 0),
            (Measurement(2.0, 2.0), True, 0),
            (None, False, 0),
        ]
        for code in range(4 ** 3):
            model = PiecewiseModel(channel_count=1)
            by_x = {}
            for position, x in enumerate(coordinates):
                measurement, hard, priority = statuses[(code // (4 ** position)) % 4]
                node = Node(f"n{position}", 0, x, measurement, priority=priority, hard_break=hard)
                model.add_node(node)
                by_x[x] = [node]
            winners = dense_winners(by_x)
            for x in [0.0, 0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0]:
                x_index = max(0, min(2, int(math.ceil(x))))
                adjacent_hard = any(statuses[(code // (4 ** idx)) % 4][1] for idx in {max(0, x_index - 1), x_index})
                try:
                    expected = dense_value(winners, x)
                    if x not in coordinates and adjacent_hard:
                        raise InterpolationGapError("hard break")
                except (OutsideDomainError, InterpolationGapError):
                    expected_type = InterpolationGapError if (0.0 < x < 2.0 or x in coordinates) else OutsideDomainError
                    with self.assertRaises(expected_type):
                        model.value_at(0, x)
                else:
                    actual = model.value_at(0, x)
                    assert_close(self, actual.lower, expected[0])
                    assert_close(self, actual.upper, expected[1])
                if x in coordinates:
                    index = coordinates.index(x)
                    measurement, hard, priority = statuses[(code // (4 ** index)) % 4]
                    left_known = index > 0 and statuses[(code // (4 ** (index - 1))) % 4][0] is not None
                    right_known = index + 1 < len(coordinates) and statuses[(code // (4 ** (index + 1))) % 4][0] is not None
                    left_open = index > 0 and not hard and not statuses[(code // (4 ** (index - 1))) % 4][1]
                    right_open = index + 1 < len(coordinates) and not hard and not statuses[(code // (4 ** (index + 1))) % 4][1]
                    if measurement is None or not ((left_known and left_open) or (right_known and right_open)):
                        with self.assertRaises(InterpolationGapError):
                            model.derivative_at(0, x)
                    else:
                        expected_lo, expected_hi = dense_slopes(winners, x)
                        derivative = model.derivative_at(0, x)
                        assert_close(self, derivative.lower, expected_lo)
                        assert_close(self, derivative.upper, expected_hi)

    def test_endpoints_exact_and_no_extrapolation(self):
        model = PiecewiseModel(channel_count=1)
        model.add_node(Node("a", 0, 1.0, Measurement(2.0, 4.0)))
        model.add_node(Node("b", 0, 2.0, Measurement(6.0, 8.0)))
        left = model.value_at(0, 1.0)
        right = model.value_at(0, 2.0)
        self.assertEqual((left.lower, left.upper), (2.0, 4.0))
        self.assertEqual((right.lower, right.upper), (6.0, 8.0))
        self.assertIsNone(left.evidence_id)
        with self.assertRaises(OutsideDomainError):
            model.value_at(0, 0.999)
        with self.assertRaises(OutsideDomainError):
            model.value_at(0, 2.001)

    def test_unknown_measurement_is_gap_not_zero(self):
        model = PiecewiseModel(channel_count=2)
        model.add_node(Node("a", 1, 0.0, Measurement(0.0, 0.0)))
        model.add_node(Node("u", 1, 1.0, None))
        model.add_node(Node("b", 1, 2.0, Measurement(2.0, 2.0)))
        with self.assertRaises(InterpolationGapError):
            model.value_at(1, 1.0)
        with self.assertRaises(InterpolationGapError):
            model.value_at(1, 0.5)
        with self.assertRaises(InterpolationGapError):
            model.value_at(1, 1.5)

    def test_priority_versions_and_calibration_snapshot(self):
        model = PiecewiseModel(channel_count=1)
        model.add_node(Node("old", 0, 1.0, Measurement(0.0, 0.0), priority=5, version=1))
        model.add_node(Node("new", 0, 1.0, Measurement(5.0, 5.0), priority=5, version=2))
        before = model.snapshot
        self.assertEqual(model.value_at(0, 1.0).nominal, 5.0)
        model.calibrate("new", measurement=Measurement(9.0, 9.0), version=7)
        self.assertEqual(model.value_at(0, 1.0).nominal, 9.0)
        self.assertEqual(model.value_at(0, 1.0, before).nominal, 5.0)
        model.add_node(Node("higher", 0, 1.0, Measurement(3.0, 3.0), priority=6, version=1))
        self.assertEqual(model.value_at(0, 1.0).nominal, 3.0)

    def test_insert_revokes_old_interval_and_creates_new_evidence(self):
        model = PiecewiseModel(channel_count=2)
        model.add_node(Node("a", 0, 0.0, Measurement(0.0, 0.0)))
        model.add_node(Node("c", 0, 2.0, Measurement(2.0, 2.0)))
        model.add_node(Node("other", 1, 0.0, Measurement(8.0, 8.0)))
        old_eid = model.value_at(0, 1.0).evidence_id
        report = model.add_node(Node("b", 0, 1.0, Measurement(10.0, 10.0)))
        self.assertEqual(report.rebuilt_channels, (0,))
        self.assertEqual(report.revoked_evidence, (old_eid,))
        self.assertEqual(len(report.created_evidence), 2)
        self.assertTrue(model.evidence(old_eid).revoked)
        for evidence_id in report.created_evidence:
            self.assertFalse(model.evidence(evidence_id).revoked)
        self.assertEqual(model.value_at(0, 0.5).nominal, 5.0)

    def test_delete_restore_round_trip(self):
        model = PiecewiseModel(channel_count=1)
        model.add_node(Node("a", 0, 0.0, Measurement(0.0, 1.0)))
        model.add_node(Node("b", 0, 1.0, Measurement(2.0, 3.0)))
        model.add_node(Node("c", 0, 2.0, Measurement(4.0, 5.0)))
        before_entries = model.snapshot.entries
        before_values = [model.value_at(0, x) for x in (0.0, 1.0, 2.0)]
        deleted_report = model.delete_node("b")
        self.assertEqual(deleted_report.revoked_evidence, (1, 2))
        self.assertEqual(len(deleted_report.created_evidence), 1)
        self.assertEqual(model.value_at(0, 0.5).lower, 1.0)
        model.restore_node("b")
        after_values = [model.value_at(0, x) for x in (0.0, 1.0, 2.0)]
        self.assertEqual(model.snapshot.entries, before_entries)
        for left, right in zip(before_values, after_values):
            self.assertEqual((left.lower, left.upper), (right.lower, right.upper))
            self.assertEqual(tuple(s.node_id for s in left.sources),
                             tuple(s.node_id for s in right.sources))

    def test_hard_break_blocks_cross_interpolation(self):
        model = PiecewiseModel(channel_count=1)
        model.add_node(Node("a", 0, 0.0, Measurement(0.0, 0.0)))
        model.add_node(Node("b", 0, 1.0, Measurement(1.0, 1.0), hard_break=True))
        model.add_node(Node("c", 0, 2.0, Measurement(2.0, 2.0)))
        self.assertEqual(model.value_at(0, 1.0).nominal, 1.0)
        with self.assertRaises(InterpolationGapError):
            model.value_at(0, 0.5)
        with self.assertRaises(InterpolationGapError):
            model.value_at(0, 1.5)
        with self.assertRaises(InterpolationGapError):
            model.derivative_at(0, 1.0)


    def test_interval_sampling_truth_is_contained(self):
        rng = random.Random(20260925)
        model = PiecewiseModel(channel_count=1)
        xs = [0.0, 1.0, 2.0, 3.0]
        nodes = []
        for x in xs:
            center = rng.uniform(-2.0, 2.0)
            radius = rng.uniform(0.05, 0.4)
            node = Node(f"n{x}", 0, x, Measurement(center - radius, center + radius))
            nodes.append((node, center))
            model.add_node(node)
        for _ in range(80):
            x = rng.uniform(0.0, 3.0)
            index = min(int(math.floor(x)), len(nodes) - 2)
            left_node, left_true = nodes[index]
            right_node, right_true = nodes[index + 1]
            t = x - left_node.x
            truth = left_true * (1.0 - t) + right_true * t
            result = model.value_at(0, x)
            self.assertLessEqual(result.lower - 1e-12, truth)
            self.assertLessEqual(truth, result.upper + 1e-12)

    def test_nonmonotone_inverse_has_multiple_solutions(self):
        model = PiecewiseModel(channel_count=1)
        for node_id, x, y in [("a", 0.0, 0.0), ("b", 1.0, 2.0), ("c", 2.0, 0.0)]:
            model.add_node(Node(node_id, 0, x, Measurement(y, y)))
        solutions = model.inverse_at(0, 1.0, include_uncertain=False)
        self.assertEqual([(round(item.lower, 10), round(item.upper, 10), item.kind) for item in solutions],
                         [(0.5, 0.5, "point"), (1.5, 1.5, "point")])

    def test_flat_platform_returns_interval(self):
        model = PiecewiseModel(channel_count=1)
        model.add_node(Node("a", 0, 0.0, Measurement(3.0, 3.0)))
        model.add_node(Node("b", 0, 2.0, Measurement(3.0, 3.0)))
        solutions = model.inverse_at(0, 3.0, include_uncertain=False)
        self.assertEqual(len(solutions), 1)
        self.assertEqual((solutions[0].lower, solutions[0].upper, solutions[0].kind),
                         (0.0, 2.0, "plateau"))
        self.assertEqual(tuple(source.node_id for source in solutions[0].sources), ("a", "b"))

    def test_interval_inverse_does_not_clamp_independent_constraints(self):
        model = PiecewiseModel(channel_count=1)
        model.add_node(Node("a", 0, 0.0, Measurement(0.0, 0.0)))
        model.add_node(Node("b", 0, 1.0, Measurement(1.0, 1.0)))
        self.assertEqual(model.inverse_at(0, 2.0), ())
        self.assertEqual(model.inverse_at(0, -1.0), ())

    def test_uncertain_inverse_labels_whole_range(self):
        model = PiecewiseModel(channel_count=1)
        model.add_node(Node("a", 0, 0.0, Measurement(0.0, 10.0)))
        model.add_node(Node("b", 0, 1.0, Measurement(0.0, 10.0)))
        solutions = model.inverse_at(0, 5.0)
        self.assertEqual(len(solutions), 1)
        self.assertEqual((solutions[0].lower, solutions[0].upper, solutions[0].kind),
                         (0.0, 1.0, "uncertain_range"))

    def test_unrelated_channel_is_not_rebuilt(self):
        model = PiecewiseModel(channel_count=3)
        model.add_node(Node("a0", 0, 0.0, Measurement(0.0, 0.0)))
        model.add_node(Node("a1", 0, 1.0, Measurement(1.0, 1.0)))
        model.add_node(Node("b0", 1, 0.0, Measurement(2.0, 2.0)))
        model.add_node(Node("b1", 1, 1.0, Measurement(3.0, 3.0)))
        model.add_node(Node("c0", 2, 0.0, Measurement(4.0, 4.0)))
        model.add_node(Node("c1", 2, 1.0, Measurement(5.0, 5.0)))
        report = model.add_node(Node("a2", 0, 0.5, Measurement(0.5, 0.5)))
        self.assertEqual(report.rebuilt_channels, (0,))
        self.assertEqual(model.snapshot.segments[1][0].evidence_id, 2)
        self.assertEqual(model.snapshot.segments[2][0].evidence_id, 3)

    def test_explicit_dimensions_are_validated(self):
        with self.assertRaises(ValueError):
            PiecewiseModel(channel_count=0)
        model = PiecewiseModel(channel_count=1)
        with self.assertRaises(IndexError):
            model.value_at(1, 0.0)
        with self.assertRaises(IndexError):
            model.add_node(Node("bad", 1, 0.0, Measurement(0.0, 0.0)))


if __name__ == "__main__":
    unittest.main()

from fractions import Fraction
import random
import unittest

from drift_revision import (
    DriftConfig,
    SampleKey,
    DriftDetector,
    DriftStatus,
    Operation,
    OperationKind,
    assert_replay_matches,
)


class DriftDetectorTest(unittest.TestCase):
    def test_extreme_value_marked_later_is_equivalent_to_never_included(self) -> None:
        config = DriftConfig(lookback=5, min_baseline=2, threshold=3)
        detector = DriftDetector(config)

        events = [
            (0, 10),
            (1, 10),
            (2, 1_000_000),  # 极端值，先纳入
            (3, 30),
            (4, 30),
            (5, 30),
        ]
        for seq, (time, value) in enumerate(events):
            detector.add_sample("sensor-a", seq, time, value)

        # 极端值先被纳入基线。
        before_extreme = detector.decision("sensor-a", 2)
        before_after_one = detector.decision("sensor-a", 3)
        self.assertEqual(before_extreme.status, DriftStatus.DRIFT)
        self.assertEqual(before_after_one.baseline_n, 3)
        self.assertEqual(before_after_one.mean, Fraction(333340, 1))
        self.assertEqual(before_after_one.status, DriftStatus.NORMAL)

        # 补录异常段：极端点本身排除，且后续基线必须像从未纳入它一样。
        report = detector.mark_anomaly("sensor-a", "late-extreme", 2, 2)
        self.assertEqual(report.updated_decisions, 4)

        revised_extreme = detector.decision("sensor-a", 2)
        revised_one = detector.decision("sensor-a", 3)
        revised_two = detector.decision("sensor-a", 4)
        revised_three = detector.decision("sensor-a", 5)
        self.assertEqual(revised_extreme.status, DriftStatus.EXCLUDED)
        self.assertEqual(revised_one.status, DriftStatus.DRIFT)
        self.assertEqual(revised_one.mean, Fraction(10, 1))
        self.assertEqual(revised_one.variance, Fraction(0, 1))
        self.assertEqual(revised_two.mean, Fraction(50, 3))
        self.assertEqual(revised_three.mean, Fraction(20, 1))
        self.assertNotIn(("sensor-a", 2), [tuple_key(key) for key in revised_one.included_keys])
        self.assertEqual(
            [tuple_key(key) for key in revised_one.included_keys],
            [("sensor-a", 0), ("sensor-a", 1)],
        )

        # 全量重放：从操作日志重建的增量状态/修订链必须完全一致。
        assert_replay_matches(detector)

        # “从未纳入该值”的参照实现：段从一开始就存在。
        reference = DriftDetector(config)
        reference.mark_anomaly("sensor-a", "late-extreme", 2, 2, note="预先存在")
        for seq, (time, value) in enumerate(events):
            reference.add_sample("sensor-a", seq, time, value)

        for seq in range(len(events)):
            actual = detector.decision("sensor-a", seq)
            expected = reference.decision("sensor-a", seq)
            self.assertEqual(
                (actual.status, actual.baseline_n, actual.mean, actual.variance,
                 actual.included_keys, actual.excluded_keys),
                (expected.status, expected.baseline_n, expected.mean, expected.variance,
                 expected.included_keys, expected.excluded_keys),
                msg=f"seq={seq} 补录后不等于从未纳入",
            )

    def test_all_baseline_excluded_returns_undecidable(self) -> None:
        detector = DriftDetector(DriftConfig(lookback=3, min_baseline=1))
        detector.add_sample("s", 0, 0, 1)
        detector.add_sample("s", 1, 1, 1)
        detector.add_sample("s", 2, 2, 1)
        detector.add_sample("s", 3, 3, 99)
        detector.mark_anomaly("s", "whole-window", 0, 2)

        decision = detector.decision("s", 3)
        self.assertEqual(decision.status, DriftStatus.UNDECIDABLE)
        self.assertEqual(decision.baseline_n, 0)
        self.assertIsNone(decision.mean)
        self.assertIsNone(decision.variance)
        assert_replay_matches(detector)

    def test_overlapping_segments_withdraw_one_and_then_both(self) -> None:
        detector = DriftDetector(DriftConfig(lookback=3, min_baseline=1))
        detector.add_sample("s", 0, 0, 10)
        detector.add_sample("s", 1, 1, 10)
        detector.add_sample("s", 2, 2, 10)
        detector.add_sample("s", 3, 3, 50)

        detector.mark_anomaly("s", "seg-a", 1, 1, note="A")
        detector.mark_anomaly("s", "seg-b", 0, 2, note="B")

        target = detector.decision("s", 3)
        self.assertEqual(target.included_keys, ())
        self.assertEqual(target.status, DriftStatus.UNDECIDABLE)
        self.assertEqual(
            target.excluded_baseline_segments,
            (
                (SampleKey("s", 0), ("seg-b",)),
                (SampleKey("s", 1), ("seg-a", "seg-b")),
                (SampleKey("s", 2), ("seg-b",)),
            ),
        )
        history = detector.decision_history("s", 3)
        self.assertEqual([item.version for item in history], [1, 2, 3])

        detector.withdraw_anomaly("s", "seg-a")
        # seg-a 撤回，但 t=1 仍由重叠的 seg-b 覆盖，因此 t=3 基线仍为空。
        self.assertEqual(detector.decision("s", 3).status, DriftStatus.UNDECIDABLE)
        self.assertEqual(
            detector.decision("s", 2).status, DriftStatus.EXCLUDED
        )

        detector.withdraw_anomaly("s", "seg-b")
        restored = detector.decision("s", 3)
        self.assertEqual(restored.status, DriftStatus.DRIFT)
        self.assertEqual(restored.baseline_n, 3)
        assert_replay_matches(detector)

    def test_duplicate_marker_is_idempotent_but_conflicting_id_rejected(self) -> None:
        detector = DriftDetector()
        detector.add_sample("s", 0, 0, 1)
        detector.add_sample("s", 1, 1, 2)
        first = detector.mark_anomaly("s", "same", 0, 0, note="x")
        second = detector.mark_anomaly("s", "same", 0, 0, note="x")
        self.assertEqual(first.updated_decisions, 2)
        self.assertEqual(second.updated_decisions, 0)
        self.assertEqual(len(detector.revision_log()), 4)

        with self.assertRaises(ValueError):
            detector.mark_anomaly("s", "same", 1, 1)
        assert_replay_matches(detector)

    def test_withdrawn_marker_reincludes_value_and_restores_revision_chain(self) -> None:
        detector = DriftDetector(DriftConfig(lookback=5, min_baseline=2))
        for seq, value in enumerate([10, 10, 100, 20]):
            detector.add_sample("s", seq, seq, value)

        detector.mark_anomaly("s", "bad", 2, 2)
        excluded = detector.decision("s", 2)
        self.assertEqual(excluded.status, DriftStatus.EXCLUDED)
        self.assertEqual(detector.decision("s", 3).baseline_n, 2)

        detector.withdraw_anomaly("s", "bad")
        restored = detector.decision("s", 2)
        self.assertEqual(restored.status, DriftStatus.DRIFT)
        self.assertEqual(restored.version, 3)
        self.assertEqual(detector.decision("s", 3).baseline_n, 3)
        chain = detector.revisions_for_key("s", 2)
        self.assertEqual([(revision.before_version, revision.after_version) for revision in chain],
                         [(None, 1), (1, 2), (2, 3)])
        assert_replay_matches(detector)

    def test_boundary_equality_is_not_drift_and_exact_rational_determines_critical_case(self) -> None:
        # 两个基线点 0、2：mean=1, population variance=1。
        # x=4 => delta=3 => delta²=9 = 9*variance；严格判据 => NORMAL。
        # x=5 => delta=4 => 16 > 9 => DRIFT。
        detector = DriftDetector(DriftConfig(lookback=5, min_baseline=2, threshold=3))
        detector.add_sample("s", 0, 0, "0")
        detector.add_sample("s", 1, 1, "2")
        detector.add_sample("s", 2, 2, "4")
        detector.add_sample("s", 3, 2, "5")
        self.assertEqual(detector.decision("s", 2).status, DriftStatus.NORMAL)
        self.assertEqual(detector.decision("s", 3).status, DriftStatus.DRIFT)

        # 一个极小但确定的偏移越过边界；不用 float，临界不由舍入决定。
        detector.add_sample("s", 4, 2, Fraction(4, 1) + Fraction(1, 10**30))
        self.assertEqual(detector.decision("s", 4).status, DriftStatus.DRIFT)
        with self.assertRaises(TypeError):
            detector.add_sample("bad-float", 0, 0, 1.0)
        assert_replay_matches(detector)

    def test_sources_are_independent(self) -> None:
        detector = DriftDetector(DriftConfig(lookback=10))
        detector.add_sample("a", 0, 0, 100)
        detector.add_sample("b", 0, 0, 1)
        detector.add_sample("a", 1, 1, 101)
        detector.add_sample("b", 1, 1, 1)
        detector.mark_anomaly("a", "only-a", 0, 0)
        self.assertEqual(detector.decision("a", 1).status, DriftStatus.UNDECIDABLE)
        self.assertEqual(detector.decision("b", 1).status, DriftStatus.NORMAL)
        assert_replay_matches(detector)

    def test_random_operation_history_stepwise_replay(self) -> None:
        rng = random.Random(20260921)
        for trial in range(10):
            detector = DriftDetector(
                DriftConfig(
                    lookback=rng.choice([2, 3, 5, 8]),
                    min_baseline=rng.choice([1, 2, 3]),
                    threshold=Fraction(rng.choice([2, 3, 5]), 1),
                )
            )
            sources = ["a", "b"]
            seqs = {source: 0 for source in sources}
            times = {source: 0 for source in sources}
            active: dict[str, list[str]] = {source: [] for source in sources}
            withdrawn: dict[str, list[str]] = {source: [] for source in sources}

            for _ in range(20):
                source = rng.choice(sources)
                if rng.random() < 0.68:
                    seq = seqs[source]
                    time = times[source]
                    value = rng.choice([0, 1, 2, 5, 10, 100, -3, Fraction(1, 7)])
                    detector.add_sample(source, seq, time, value)
                    seqs[source] += 1
                    times[source] += rng.choice([0, 1, 2, 3])
                elif active[source] and rng.random() < 0.55:
                    segment_id = active[source].pop()
                    withdrawn[source].append(segment_id)
                    detector.withdraw_anomaly(source, segment_id)
                else:
                    marker_number = (
                        len(active[source])
                        + len(withdrawn[source])
                        + sum(1 for operation in detector.operations if operation.kind == OperationKind.ADD_SEGMENT and operation.segment and operation.segment.source == source)
                    )
                    segment_id = f"m-{source}-{marker_number}-{trial}"
                    start = rng.randrange(0, 8)
                    end = start + rng.choice([0, 1, 3])
                    detector.mark_anomaly(source, segment_id, start, end)
                    active[source].append(segment_id)
                assert_replay_matches(detector)


def tuple_key(key):
    return (key.source, key.seq)


if __name__ == "__main__":
    unittest.main(verbosity=2)

import unittest
from fractions import Fraction

from driftdet import DriftDetector, Judgment, to_exact


def make_detector(window=5, k=2, min_baseline=3):
    return DriftDetector(window=window, k=k, min_baseline=min_baseline)


def feed(detector, values, source="A", start=1):
    for i, v in enumerate(values, start):
        detector.add_sample(source, i, f"2026-09-21T10:{i:02d}:00", v)


NORMAL = ["10.0", "10.4", "9.6", "10.2", "9.8", "10.3"]
SPIKE = "25"
TAIL = ["10.1", "14.0", "10.0", "10.1"]  # 14.0 is a genuine drift


class ExactArithmeticTest(unittest.TestCase):
    def test_decimal_strings_are_exact(self):
        self.assertEqual(to_exact("0.1"), Fraction(1, 10))
        self.assertEqual(to_exact(0.1), Fraction(1, 10))  # not binary float
        self.assertEqual(to_exact("1/3"), Fraction(1, 3))

    def test_threshold_boundary_is_exact(self):
        # baseline {1/10, 3/10, 1/10}: mean = 1/5+... construct exactly:
        # window [0.1, 0.3, 0.1] -> mean = 1/6? use exact: (1+3+1)/10 /3 = 1/6
        # var = ((1/10-1/6)^2 + (3/10-1/6)^2 + (1/10-1/6)^2)/3
        #     = (1/900 + 4/225 + 1/900)/3 = (1/900+16/900+1/900)/3 = 18/900/3 = 1/150
        # k = 1 -> threshold sigma = 1/sqrt(150); squared: 1/150.
        # x = 0.1 + sqrt-free boundary: pick x so (x-mean)^2 == 1/150 exactly?
        # Simpler boundary: window [0, 2, 0, 2, 0] -> mean 4/5, var 24/25,
        # k=5/... keep it simple with integers: window [0,2,0] mean 2/3,
        # var = (4/9+16/9+4/9)/3 = 24/27 = 8/9. k = 3/... k^2*var with k=3:
        # 9 * 8/9 = 8. x = 2/3 + 2*sqrt(2) is irrational -> use rational
        # boundary instead: window [1,3,1,3,1] mean 9/5 var 24/25, k=5/2
        # -> k^2 var = 25/4 * 24/25 = 6. (x - 9/5)^2 = 6 has no rational
        # root. Use window [0, 6, 0, 6, 0]: mean 12/5, var =
        # (144/25*2 + 36/25*3)/5? compute: devs -12/5,18/5,-12/5,18/5,-12/5
        # squares: 144/25,324/25,144/25,324/25,144/25 sum=1080/25, /5=216/25
        # k=5/3 -> k^2 var = 25/9 * 216/25 = 24. (x-12/5)^2=24 irrational.
        # Easiest exact rational boundary: var must be a perfect square
        # ratio. window [1, 2]: mean 3/2, var 1/4, k=2 -> k^2 var = 1.
        # x = 5/2 -> (x-mean)^2 = 1, not > 1 -> OK (strict).
        # x = 5/2 + epsilon -> DRIFT. Float would risk flipping this.
        d = DriftDetector(window=2, k=2, min_baseline=2)
        d.add_sample("A", 1, 1, "1")
        d.add_sample("A", 2, 2, "2")
        # exactly on the boundary: |2.5 - 1.5| = 1 == k*sigma -> NOT drift
        self.assertEqual(d.add_sample("A", 3, 3, "2.5"), Judgment.OK)
        # one ten-thousandth beyond the boundary -> DRIFT
        d2 = DriftDetector(window=2, k=2, min_baseline=2)
        d2.add_sample("A", 1, 1, "1")
        d2.add_sample("A", 2, 2, "2")
        self.assertEqual(d2.add_sample("A", 3, 3, "2.5001"), Judgment.DRIFT)

    def test_zero_variance_window(self):
        d = DriftDetector(window=3, k=3, min_baseline=2)
        d.add_sample("A", 1, 1, "7")
        d.add_sample("A", 2, 2, "7")
        self.assertEqual(d.add_sample("A", 3, 3, "7"), Judgment.OK)
        self.assertEqual(d.add_sample("A", 4, 4, "7.000001"), Judgment.DRIFT)


class RetroactiveCorrectionTest(unittest.TestCase):
    def test_pollution_then_correction(self):
        d = make_detector()
        feed(d, NORMAL + [SPIKE] + TAIL)
        # spike is judged drift; the genuine drift at seq 9 is masked by
        # the polluted baseline
        self.assertEqual(d.judgment("A", 7), Judgment.DRIFT)
        self.assertEqual(d.judgment("A", 9), Judgment.OK)
        polluted_mean = d.result("A", 9).current.mean

        d.mark_anomaly("M1", "A", 7, 7, "sensor spike")
        self.assertEqual(d.judgment("A", 7), Judgment.EXCLUDED)
        # after correction the genuine drift is unmasked
        self.assertEqual(d.judgment("A", 9), Judgment.DRIFT)
        self.assertNotEqual(d.result("A", 9).current.mean, polluted_mean)

    def test_corrected_baseline_equals_never_included(self):
        # Detector A: spike included, then retroactively marked.
        a = make_detector()
        feed(a, NORMAL + [SPIKE] + TAIL)
        a.mark_anomaly("M1", "A", 7, 7)

        # Detector B: the spike was marked before it ever entered a baseline.
        b = make_detector()
        b.mark_anomaly("M1", "A", 7, 7)
        feed(b, NORMAL + [SPIKE] + TAIL)

        for seq in range(1, 12):
            ra, rb = a.result("A", seq).current, b.result("A", seq).current
            self.assertEqual(ra.judgment, rb.judgment, f"seq {seq}")
            self.assertEqual(ra.window_keys, rb.window_keys, f"seq {seq}")
            self.assertEqual(ra.mean, rb.mean, f"seq {seq}")
            self.assertEqual(ra.variance, rb.variance, f"seq {seq}")

    def test_all_excluded_is_undecidable(self):
        d = make_detector()
        feed(d, NORMAL)
        d.mark_anomaly("M1", "A", 1, 6)
        for seq in range(1, 7):
            self.assertEqual(d.judgment("A", seq), Judgment.EXCLUDED)
        d.add_sample("A", 7, 7, "10.0")
        self.assertEqual(d.judgment("A", 7), Judgment.UNDECIDABLE)

    def test_incremental_matches_replay_step_by_step(self):
        d = make_detector()
        ops = []
        for i, v in enumerate(NORMAL + [SPIKE] + TAIL, 1):
            ops.append(("add", i, v))
        ops += [
            ("mark", "M1", 7, 7),
            ("mark", "M2", 9, 10),
            ("retract", "M1"),
            ("retract", "M2"),
            ("mark", "M3", 1, 11),
            ("retract", "M3"),
        ]
        for op in ops:
            if op[0] == "add":
                d.add_sample("A", op[1], op[1], op[2])
            elif op[0] == "mark":
                d.mark_anomaly(op[1], "A", op[2], op[3])
            else:
                d.retract_mark(op[1])
            self.assertEqual(
                d.verify_against_replay(), [], f"mismatch after {op}"
            )


class SegmentSemanticsTest(unittest.TestCase):
    def test_overlapping_segments(self):
        d = make_detector()
        feed(d, NORMAL + TAIL)
        d.mark_anomaly("S1", "A", 3, 6)
        d.mark_anomaly("S2", "A", 5, 8)
        for seq in (3, 4, 5, 6, 7, 8):
            self.assertTrue(d.is_excluded("A", seq), seq)
        self.assertFalse(d.is_excluded("A", 2))
        self.assertFalse(d.is_excluded("A", 9))
        # retracting one segment leaves the other's coverage intact
        d.retract_mark("S1")
        for seq in (5, 6, 7, 8):
            self.assertTrue(d.is_excluded("A", seq), seq)
        for seq in (3, 4):
            self.assertFalse(d.is_excluded("A", seq), seq)
        self.assertEqual(d.verify_against_replay(), [])

    def test_duplicate_mark_is_idempotent(self):
        d = make_detector()
        feed(d, NORMAL)
        first = d.mark_anomaly("M1", "A", 2, 4)
        self.assertIsNotNone(first)
        events_before = len(d.events)
        chains_before = {
            k: len(d.revision_chain("A", k)) for k in range(1, 7)
        }
        self.assertIsNone(d.mark_anomaly("M1", "A", 2, 4))
        self.assertEqual(len(d.events), events_before)
        for seq in range(1, 7):
            self.assertEqual(len(d.revision_chain("A", seq)), chains_before[seq])

    def test_retract_unknown_or_inactive_is_noop(self):
        d = make_detector()
        feed(d, NORMAL)
        self.assertIsNone(d.retract_mark("nope"))
        d.mark_anomaly("M1", "A", 2, 4)
        self.assertIsNotNone(d.retract_mark("M1"))
        self.assertIsNone(d.retract_mark("M1"))

    def test_retract_reinstates_original_judgments(self):
        d = make_detector()
        feed(d, NORMAL + [SPIKE] + TAIL)
        original = {s: d.judgment("A", s) for s in range(1, 12)}
        d.mark_anomaly("M1", "A", 7, 7)
        self.assertEqual(d.judgment("A", 7), Judgment.EXCLUDED)
        d.retract_mark("M1")
        for seq in range(1, 12):
            self.assertEqual(d.judgment("A", seq), original[seq], f"seq {seq}")
        # re-included spike is judged drift again, chain grew to 3 versions
        chain = d.revision_chain("A", 7)
        self.assertEqual([r.judgment for r in chain],
                         [Judgment.DRIFT, Judgment.EXCLUDED, Judgment.DRIFT])
        self.assertEqual([r.version for r in chain], [1, 2, 3])
        self.assertEqual(d.verify_against_replay(), [])


class RevisionChainTest(unittest.TestCase):
    def test_only_dependent_results_are_revised(self):
        d = make_detector()
        feed(d, NORMAL + [SPIKE] + TAIL)
        event = d.mark_anomaly("M1", "A", 7, 7, "spike")
        # samples before the segment are untouched
        for seq in range(1, 7):
            self.assertEqual(len(d.revision_chain("A", seq)), 1, f"seq {seq}")
        # covered sample and dependent later results carry the cause
        rev7 = d.revision_chain("A", 7)[-1]
        self.assertEqual(rev7.cause_event, event.event_id)
        self.assertIn("M1", rev7.reason)
        rev9 = d.revision_chain("A", 9)[-1]
        self.assertEqual(rev9.cause_event, event.event_id)
        self.assertEqual(rev9.judgment, Judgment.DRIFT)

    def test_other_sources_unaffected(self):
        d = make_detector()
        feed(d, NORMAL, source="A")
        feed(d, NORMAL, source="B")
        d.mark_anomaly("M1", "A", 2, 4)
        for seq in range(1, 7):
            self.assertEqual(len(d.revision_chain("B", seq)), 1)
        self.assertEqual(d.verify_against_replay(), [])

    def test_event_log_is_complete_evidence(self):
        d = make_detector()
        feed(d, NORMAL)
        d.mark_anomaly("M1", "A", 2, 4, "backfilled")
        d.retract_mark("M1", "false alarm")
        kinds = [(e.kind, e.segment_id, e.reason) for e in d.events]
        self.assertEqual(kinds, [
            ("mark", "M1", "backfilled"),
            ("retract", "M1", "false alarm"),
        ])


if __name__ == "__main__":
    unittest.main()

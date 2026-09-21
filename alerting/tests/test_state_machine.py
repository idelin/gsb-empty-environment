import unittest

from alerting.domain import (
    FIRING, PENDING, RESOLVED, SUPPRESSED, Sample, SuppressionVersion,
    ThresholdVersion, evaluate,
)


def ep(samples, enter=10, exit_=10, duration=5, sups=(), vf=0):
    v = ThresholdVersion("r", 1, vf, "cpu", enter, exit_, duration)
    return evaluate(samples, [v], list(sups))


class TestCanonicalStateMachine(unittest.TestCase):
    def test_enter_duration_sparse_samples(self):
        # 0:11 enters; at 6:12 the incident has lasted 6 >= 5 => mature@5
        eps = ep([Sample(0, "cpu", 11), Sample(6, "cpu", 12)])
        self.assertEqual(len(eps), 1)
        e = eps[0]
        self.assertIsNone(e.end_t)
        self.assertEqual(e.mature_t, 5)
        self.assertEqual(e.first_open_t, 5)
        self.assertEqual(e.state_at(6), FIRING)

    def test_late_sample_retracts_the_fault(self):
        # same data + a backfilled t=3 value 8: episode ends at 3 before 5
        eps = ep([Sample(0, "cpu", 11), Sample(3, "cpu", 8, seq=1),
                  Sample(6, "cpu", 12)])
        self.assertEqual(len(eps), 2)
        first = eps[0]
        self.assertEqual(first.end_t, 3)
        self.assertIsNone(first.mature_t)
        self.assertEqual(first.state_at(6), RESOLVED)
        self.assertEqual(eps[1].state_at(6), PENDING)

    def test_hysteresis_zone_does_not_resolve(self):
        eps = ep([Sample(0, "cpu", 11), Sample(2, "cpu", 11),
                  Sample(4, "cpu", 10.5)], enter=10, exit_=9, duration=5)
        self.assertEqual(len(eps), 1)
        self.assertIsNone(eps[0].end_t)

    def test_exit_below_exit_threshold_resolves(self):
        eps = ep([Sample(0, "cpu", 11), Sample(6, "cpu", 5)],
                 enter=10, exit_=9, duration=5)
        self.assertEqual(eps[0].end_t, 6)
        self.assertEqual(eps[0].state_at(6), RESOLVED)

    def test_suppression_states_and_no_resent_stale_incident(self):
        # mature at 5, open; suppression [4,10) exists from t=4 onward
        sup = SuppressionVersion("s", 1, valid_from=4, window_start=4,
                                 window_end=10, metric="cpu")
        samples = [Sample(0, "cpu", 11), Sample(6, "cpu", 12)]
        e = ep(samples, sups=[sup])[0]
        self.assertEqual(e.state_at(6), SUPPRESSED)
        self.assertIsNone(e.first_open_t)  # still suppressed at horizon 6
        # fault recovers at 8 while suppressed; window ending at 10 must NOT
        # resurface a dead incident
        samples = samples + [Sample(8, "cpu", 0), Sample(12, "cpu", 0)]
        e = ep(samples, sups=[sup])[0]
        self.assertEqual(e.end_t, 8)
        self.assertEqual(e.state_at(12), RESOLVED)
        self.assertIsNone(e.first_open_t)

    def test_fault_still_alive_when_suppression_lifts_opens_at_lift(self):
        sup = SuppressionVersion("s", 1, valid_from=4, window_start=4,
                                 window_end=10, metric="cpu")
        samples = [Sample(t, "cpu", 12) for t in range(0, 14)]
        e = ep(samples, sups=[sup])[0]
        self.assertEqual(e.state_at(13), FIRING)
        self.assertEqual(e.first_open_t, 10)


if __name__ == "__main__":
    unittest.main()

"""Slow clock drift + bursty network jitter: the two must not be conflated
into a permanent delay increase.

Scenario: source clock runs fast by 5000 ppm for the whole run; a jitter
burst (transit spread raised from +/-10 ms to +/-35 ms) happens on [5s, 9s].

Expected:
  * the clock estimator tracks the drift (slope ~ 1.005) and the playout
    rate absorbs it -> no drift-driven drops and no buffer overflow;
  * the drift-corrected transit estimate stays at the true network transit
    (~30 ms), it does not accumulate drift;
  * the headroom rises during the burst and falls back afterwards: the burst
    is not memorised as permanently added delay.
"""
import unittest

from playdelay import config
from playdelay.sim import Runner, Scenario

DRIFT = 5e-3


class ClockDriftVsJitterTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        sc = Scenario(duration=20.0, drift=DRIFT, base_transit=0.03,
                      jitter=0.01, bursts=((5.0, 9.0, 0.035),),
                      loss=0.0, dup=0.0)
        cls.runner = Runner(sc, seed=11)
        cls.runner.run()
        cls.player = cls.runner.player

    def test_clock_tracks_drift(self):
        slope = self.player.monitor[-1][4]
        self.assertAlmostEqual(slope, 1.0 + DRIFT,
                               delta=config.SLOPE_TEST_TOL)

    def test_jitter_estimate_not_poisoned_by_drift(self):
        # A naive (recv - send) transit estimate would drift by
        # 0.005 * 20 s = 100 ms over the run. The drift-corrected jitter
        # (EWMA of |transit - mean|, monitor column 5) must stay at the
        # true network jitter (~5 ms for +/-10 ms uniform) instead.
        mon = self.player.monitor
        pre = max(m[5] for m in mon if 3.0 <= m[0] < 4.9)
        post = max(m[5] for m in mon if m[0] >= 18.0)
        self.assertLess(pre, 0.012)
        self.assertLess(post, 0.012)

    def test_burst_not_memorised_as_permanent_delay(self):
        mon = self.player.monitor
        pre = max(m[3] for m in mon if 3.0 <= m[0] < 4.9)
        burst = max(m[3] for m in mon if 6.0 <= m[0] < 9.0)
        post = max(m[3] for m in mon if m[0] >= 18.0)
        # drift alone (pre-burst) does not inflate the target delay
        self.assertLess(pre, config.MIN_HEADROOM + 0.015)
        # the burst is tracked while it lasts
        self.assertGreater(burst, config.MIN_HEADROOM + 0.025)
        # ... and forgotten afterwards (no permanent delay increase)
        self.assertLess(post, config.MIN_HEADROOM + 0.02)

    def test_drift_absorbed_by_rate_not_by_drops(self):
        drops_pre = sum(rec.n_drop for rec in self.player.trajectory
                        if rec.t < 5.0)
        self.assertEqual(drops_pre, 0)
        self.assertEqual(self.player.stats.overflow, 0)
        self.assertEqual(self.player.stats.missing_packets, 0)

    def test_playout_position_monotonic(self):
        positions = [rec.src_pos for rec in self.player.trajectory]
        self.assertTrue(all(b >= a for a, b in zip(positions, positions[1:])))


if __name__ == '__main__':
    unittest.main()

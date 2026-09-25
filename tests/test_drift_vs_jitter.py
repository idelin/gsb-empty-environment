"""Slow clock drift + bursty jitter must not be conflated into a
permanent delay increase: drift is absorbed by the rate ratio, jitter
bursts only raise the target delay temporarily."""

import unittest

from playout_sim import tolerances as tol
from playout_sim.world import World

DRIFT_PPM = 150.0
BURSTS = [(10.0, 11.0), (14.0, 15.0)]
DURATION = 20.0


def make_world():
    return World({
        "duration": DURATION,
        "drift_ppm": DRIFT_PPM,
        "normal_jitter": (0.0, 0.002),
        "burst_windows": BURSTS,
        "burst_jitter": (0.01, 0.04),
        "capacity": 24,
        "seed": 11,
        "shuffle_seed": 3,
        "delivery": "batch",
    })


def record_at(trace, t):
    return min(trace, key=lambda r: abs(r[1] - t))


class TestDriftVsJitter(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # Run only slightly past the last send: after the source stops,
        # the drained tail is concealed by design and is not part of
        # this test's measurement.
        cls.world = make_world().run(DURATION + 0.05)
        cls.trace = cls.world.receiver.trace
        cls.started = [r for r in cls.trace if r[2] == 1]

    def test_playout_is_monotonic(self):
        positions = [r[3] for r in self.started]
        advances = [r[4] for r in self.started]
        self.assertTrue(all(b > a for a, b in zip(positions, positions[1:])),
                        "play_pos must be strictly increasing")
        self.assertTrue(all(a > 0.0 for a in advances),
                        "advance must always be positive")

    def test_drift_estimate_converges(self):
        est = self.world.estimator.drift_ppm
        print("\n[drift] true=%.1f ppm  estimated=%.1f ppm  (tol +/-%.0f)"
              % (DRIFT_PPM, est, tol.DRIFT_TOLERANCE_PPM))
        self.assertAlmostEqual(est, DRIFT_PPM, delta=tol.DRIFT_TOLERANCE_PPM)

    def test_burst_does_not_become_permanent_delay(self):
        fs = self.world.config["fs"]
        base = self.world.config["base_margin_s"]
        pre = record_at(self.trace, 9.5)[9] / fs
        during = max(r[9] for r in self.trace if BURSTS[0][0] <= r[1] <= BURSTS[0][1]) / fs
        end = record_at(self.trace, DURATION - 0.5)[9] / fs
        print("\n[delay] base=%.1f ms  pre-burst target=%.1f ms  "
              "burst peak target=%.1f ms  final target=%.1f ms"
              % (base * 1e3, pre * 1e3, during * 1e3, end * 1e3))
        # Drift alone must not inflate the target (it is rate-compensated).
        self.assertLessEqual(pre, base + tol.TARGET_PRE_BURST_MARGIN_S)
        # A jitter burst raises the target temporarily...
        self.assertGreaterEqual(during, base + tol.TARGET_BURST_RISE_S)
        # ...but it decays back: no permanent delay increase.
        self.assertLessEqual(end, pre + tol.DELAY_SETTLE_TOLERANCE_S)

    def test_concealment_stays_bounded(self):
        concealed = self.world.receiver.stats["conceal_missing_samples"]
        print("\n[loss] concealed samples during drift+bursts: %d" % concealed)
        self.assertLessEqual(concealed, tol.MAX_CONCEALED_SAMPLES_DRIFT_TEST)


if __name__ == "__main__":
    unittest.main()

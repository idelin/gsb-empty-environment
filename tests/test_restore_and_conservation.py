"""Save/restore and random delivery order produce the identical
schedule trace; true vs compensated samples are conserved."""

import json
import unittest

from playout_sim.report import conservation_report
from playout_sim.world import World


def cfg_base(**over):
    cfg = {
        "duration": 6.0,
        "drift_ppm": 80.0,
        "normal_jitter": (0.0, 0.008),
        "capacity": 24,
        "seed": 7,
        "shuffle_seed": 9,
        "delivery": "batch",
    }
    cfg.update(over)
    return cfg


class TestSaveRestore(unittest.TestCase):
    def test_snapshot_restore_is_bit_identical(self):
        cfg = cfg_base(burst_windows=[(3.5, 4.0)], burst_jitter=(0.01, 0.03))
        w1 = World(cfg).run(3.0)
        snap = json.loads(json.dumps(w1.snapshot()))  # JSON round-trip
        w1.run(6.6)
        w2 = World.from_state(snap).run(6.6)
        w3 = World(cfg).run(6.6)   # uninterrupted baseline
        print("\n[restore] ticks: continued=%d restored=%d baseline=%d"
              % (w1.receiver.tick_index, w2.receiver.tick_index,
                 w3.receiver.tick_index))
        self.assertEqual(w1.receiver.trace, w2.receiver.trace)
        self.assertEqual(w1.receiver.trace, w3.receiver.trace)
        self.assertEqual(w1.receiver.stats, w2.receiver.stats)


class TestRandomDeliveryOrder(unittest.TestCase):
    def test_delivery_order_does_not_change_schedule(self):
        runs = []
        for delivery, shuffle_seed in (("batch", 1), ("batch", 2),
                                       ("immediate", 1)):
            world = World(cfg_base(duration=5.0, seed=21,
                                   shuffle_seed=shuffle_seed,
                                   delivery=delivery)).run(5.05)
            runs.append(world)
        traces = [w.receiver.trace for w in runs]
        print("\n[reorder] traces identical=%s  concealed=%d  underrun-free"
              % (traces[0] == traces[1] == traces[2],
                 runs[0].receiver.stats["conceal_missing_samples"]))
        self.assertEqual(traces[0], traces[1])
        self.assertEqual(traces[0], traces[2])
        for world in runs:
            self.assertEqual(
                world.receiver.stats["conceal_missing_samples"], 0)


class TestConservation(unittest.TestCase):
    def test_true_and_compensated_samples_balance(self):
        world = World(cfg_base(
            duration=8.0,
            capacity=8,                      # small: forces overflow
            loss_prob=0.02,
            dup_prob=0.02,
            burst_windows=[(3.0, 4.0)],
            burst_jitter=(0.02, 0.08),
            seed=13,
        )).run(8.6)
        report = conservation_report(world)
        print("\n[conservation]")
        for key in ("sent_samples", "net_lost_samples", "arrived_samples",
                    "dup_injected_samples",
                    "accepted_samples", "duplicate_samples",
                    "overflow_samples", "late_samples",
                    "emitted_true_samples", "dropped_samples",
                    "buffered_now_samples", "emitted_synth_samples",
                    "conceal_missing_samples", "grow_synth_samples",
                    "preroll_synth_samples", "output_samples"):
            print("  %-24s %d" % (key, report[key]))
        for name, ok in report["checks"].items():
            print("  check: %-46s %s" % (name, "OK" if ok else "FAIL"))
        for name, ok in report["checks"].items():
            self.assertTrue(ok, name)
        # The scenario really exercised every loss path.
        self.assertGreater(report["net_lost_samples"], 0)
        self.assertGreater(report["duplicate_samples"], 0)
        self.assertGreater(report["overflow_samples"], 0)
        self.assertGreater(report["conceal_missing_samples"], 0)


if __name__ == "__main__":
    unittest.main()

"""Save/restore, random delivery order, and sample conservation.

* A run interrupted by a snapshot and restored (even twice) must reproduce
  the exact schedule trajectory of the uninterrupted reference run.
* Batched delivery with random within-batch order (seed-dependent) must
  produce the identical specified trajectory for every seed.
* Conservation: every generated source sample is accounted for
  (played-real / concealed / dropped), every delivered copy is classified,
  and emitted output = real + compensated (synthesized) samples.
"""
import unittest

from playdelay import config
from playdelay.sim import Runner, Scenario

P = config.PACKET_SAMPLES


def scenario_batched():
    return Scenario(duration=8.0, drift=0.0, batched=True, base_transit=0.03,
                    transit_pattern=(0.0, 0.03, 0.06), batch_period=0.1,
                    tail=1.0)


class SaveRestoreTest(unittest.TestCase):
    def test_snapshot_restore_identical(self):
        ref = Runner(scenario_batched(), seed=3)
        ref.run()

        r1 = Runner(scenario_batched(), seed=3)
        r1.run_until(3.0)
        data = r1.snapshot()
        r1.run()

        r2 = Runner.restore(data)
        r2.run()
        r3 = Runner.restore(data)   # the same snapshot restores twice
        r3.run()

        traj = ref.player.trajectory
        self.assertGreater(len(traj), 0)
        self.assertEqual(traj, r1.player.trajectory)
        self.assertEqual(traj, r2.player.trajectory)
        self.assertEqual(traj, r3.player.trajectory)
        self.assertEqual(ref.player.output, r2.player.output)
        self.assertEqual(ref.player.stats, r2.player.stats)


class RandomDeliveryOrderTest(unittest.TestCase):
    def test_random_delivery_order_same_trajectory(self):
        ref = Runner(scenario_batched(), seed=3)
        ref.run()
        traj = ref.player.trajectory
        orders = []
        for seed in (1, 2, 7, 42, 99):
            r = Runner(scenario_batched(), seed=seed)
            r.run()
            self.assertEqual(traj, r.player.trajectory)
            orders.append(tuple(r.arrival_log))
        # the delivery orders really are random and really reorder packets
        self.assertGreater(len(set(orders)), 1)
        for order in orders:
            self.assertNotEqual(list(order), sorted(order))


class ConservationTest(unittest.TestCase):
    def test_sample_conservation_report(self):
        sc = Scenario(duration=12.0, drift=5e-3, base_transit=0.03,
                      jitter=0.01, loss=0.03, dup=0.02, capacity=8,
                      stall=(0.0, 0.3), bursts=((6.0, 7.0, 0.12),),
                      tail=1.0)
        r = Runner(sc, seed=5)
        r.run()
        p, net, s = r.player, r.net, r.player.stats

        # network identity: every generated copy is lost or delivered
        self.assertEqual(net.generated + net.dup_created,
                         net.lost + net.delivered)

        # receiver identity: every delivered copy is classified exactly once
        buffered_end = p.buffer.level(p.playout_pos)
        self.assertEqual(buffered_end, 0)   # tail lets all deadlines pass
        self.assertEqual(
            net.delivered * P,
            s.real_samples + s.dropped_shrink_samples + s.late_samples
            + s.overflow_samples + s.duplicate_samples + s.old_epoch_samples
            + buffered_end)

        # source identity: every generated sample is played, concealed,
        # dropped or skipped (late/lost/overflowed packets were concealed
        # at their deadline)
        self.assertEqual(net.generated * P,
                         s.real_samples + s.missing_samples
                         + s.dropped_shrink_samples
                         + s.skipped_missing_samples)

        # output identity: emitted = real + compensated (synthesized)
        self.assertEqual(len(p.output),
                         s.real_samples + s.synth_samples)
        self.assertEqual(sum(p.output_flags), s.synth_samples)

        # all anomaly classes were exercised and traced
        self.assertGreater(s.missing_packets, 0)
        self.assertGreater(s.late, 0)
        self.assertGreater(s.duplicates, 0)
        self.assertGreater(s.overflow, 0)

        total = len(p.output)
        print("\n--- conservation report (scenario: loss/dup/burst/drift) ---")
        print(f"source packets generated : {net.generated}")
        print(f"network: lost={net.lost} dup_copies={net.dup_created} "
              f"delivered={net.delivered}")
        print(f"receiver: stored={s.stored} late={s.late} dup={s.duplicates} "
              f"overflow={s.overflow} old_epoch={s.rejected_old_epoch}")
        print(f"missing packets concealed: {s.missing_packets}")
        print(f"real samples played      : {s.real_samples}")
        print(f"compensated samples      : {s.synth_samples} "
              f"(conceal={s.missing_samples}, grow={s.grow_synth_samples})")
        print(f"dropped by shrink        : {s.dropped_shrink_samples}")
        print(f"skipped missing (shrink) : {s.skipped_missing_samples}")
        print(f"output samples           : {total} "
              f"= real {s.real_samples} + synth {s.synth_samples}")
        print(f"compensation ratio       : {s.synth_samples / total:.4%}")
        print(f"conservation: {net.generated * P} generated = "
              f"{s.real_samples} real + {s.missing_samples} concealed + "
              f"{s.dropped_shrink_samples} dropped + "
              f"{s.skipped_missing_samples} skipped")


if __name__ == '__main__':
    unittest.main()

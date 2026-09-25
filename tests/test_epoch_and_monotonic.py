"""Old-epoch rejection, late packets must not rewrite played history,
and delay shrink must never replay audio."""

import unittest

from playout_sim.fixtures import PACKET_SAMPLES, packet_samples
from playout_sim.packets import Packet
from playout_sim.world import World


def base_config(**over):
    cfg = {
        "duration": 4.0,
        "drift_ppm": 50.0,
        "normal_jitter": (0.0, 0.002),
        "capacity": 24,
        "seed": 5,
        "shuffle_seed": 5,
    }
    cfg.update(over)
    return cfg


class TestOldEpochRejected(unittest.TestCase):
    def test_old_epoch_packet_is_rejected_and_ignored(self):
        restart = (2.0, "restart", None)
        w1 = World(base_config(extra_events=[restart])).run(4.6)
        straggler = Packet(0, 5, 5 * PACKET_SAMPLES, 3.0,
                           packet_samples(5 * PACKET_SAMPLES))
        w2 = World(base_config(
            extra_events=[restart, (3.0, "arrive", straggler.to_list())]
        )).run(4.6)
        st1, st2 = w1.receiver.stats, w2.receiver.stats
        print("\n[epoch] old_epoch_packets=%d  epoch_resets=%d"
              % (st2["old_epoch_packets"], st2["epoch_resets"]))
        self.assertEqual(st2["old_epoch_packets"], 1)
        self.assertEqual(st1["old_epoch_packets"], 0)
        self.assertEqual(st2["epoch_resets"], 1)
        # The rejected packet changed nothing in the playout schedule.
        self.assertEqual(w1.receiver.trace, w2.receiver.trace)


class TestLatePacketCannotRewrite(unittest.TestCase):
    def test_late_packet_does_not_change_played_output(self):
        cfg = base_config(duration=3.0, loss_seqs=[(0, 40)])
        w1 = World(cfg).run(3.6)   # seq 40 simply never arrives
        late = Packet(0, 40, 40 * PACKET_SAMPLES, 2.5,
                      packet_samples(40 * PACKET_SAMPLES))
        cfg2 = base_config(duration=3.0, loss_seqs=[(0, 40)],
                           extra_events=[(2.5, "arrive", late.to_list())])
        w2 = World(cfg2).run(3.6)
        st2 = w2.receiver.stats
        print("\n[late] late_packets=%d late_samples=%d concealed=%d"
              % (st2["late_packets"], st2["late_samples"],
                 st2["conceal_missing_samples"]))
        self.assertEqual(st2["late_packets"], 1)
        self.assertEqual(st2["late_samples"], PACKET_SAMPLES)
        self.assertGreater(st2["conceal_missing_samples"], 0)
        # Played interval is not rewritten: identical schedules.
        self.assertEqual(w1.receiver.trace, w2.receiver.trace)


class TestDelayShrinkNoReplay(unittest.TestCase):
    def test_shrink_drops_forward_and_never_replays(self):
        world = World(base_config(
            duration=6.0,
            extra_events=[(4.0, "set_margin", 0.012)],
        )).run(6.6)
        trace = world.receiver.trace
        started = [r for r in trace if r[2] == 1]
        positions = [r[3] for r in started]
        advances = [r[4] for r in started]
        stats = world.receiver.stats
        fs = world.config["fs"]
        end = min(trace, key=lambda r: abs(r[1] - 5.9))
        print("\n[shrink] dropped_samples=%d  min_advance=%.1f  "
              "final target=%.1f ms  final level=%.1f ms"
              % (stats["dropped_samples"], min(advances),
                 end[9] / fs * 1e3, end[8] / fs * 1e3))
        self.assertGreater(stats["dropped_samples"], 0)
        self.assertTrue(all(a > 0.0 for a in advances))
        self.assertTrue(all(b > a for a, b in zip(positions, positions[1:])),
                        "delay shrink must not move play_pos backwards")
        # Level re-settles near the reduced target.
        self.assertLessEqual(abs(end[8] - end[9]), 600.0)


if __name__ == "__main__":
    unittest.main()

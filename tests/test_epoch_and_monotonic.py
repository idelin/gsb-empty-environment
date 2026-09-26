"""Epoch rejection, late-packet immutability, shrink-without-replay, and
traceability of duplicates / overflow / loss (direct engine API)."""
import pickle
import unittest

from playdelay import config
from playdelay.fixtures import source_block
from playdelay.model import Packet
from playdelay.player import PlayoutEngine

P = config.PACKET_SAMPLES
TRANSIT = 0.05


def make_pkt(epoch, seq):
    src = seq * P
    return Packet(epoch, seq, src, src / config.SAMPLE_RATE,
                  tuple(source_block(src, P)))


def recv_of(pkt):
    return pkt.send_time + TRANSIT


def drive(eng, packets, t_start, t_end):
    """Queue all packets, then run playout ticks over [t_start, t_end]."""
    for pkt in packets:
        eng.on_packet(pkt, recv_of(pkt))
    k = int(round(t_start / config.TICK))
    while k * config.TICK <= t_end:
        eng.tick(k * config.TICK)
        k += 1


def spans_ok(eng):
    """Real emission spans are disjoint and strictly forward-moving."""
    spans = eng.real_spans
    return all(b[0] >= a[0] + a[1] for a, b in zip(spans, spans[1:]))


class EpochTest(unittest.TestCase):
    def test_old_epoch_rejected(self):
        eng = PlayoutEngine()
        drive(eng, [make_pkt(0, i) for i in range(6)], 0.0, 0.4)
        self.assertGreater(eng.stats.real_samples, 0)
        # stream restarts at epoch 1
        drive(eng, [make_pkt(1, i) for i in range(4)], 0.42, 0.8)
        self.assertEqual(eng.stats.epoch_changes, 1)
        # a straggler from epoch 0 must be rejected and change nothing:
        # compare against a control engine that never sees the straggler
        ctl = pickle.loads(pickle.dumps(eng))
        eng.on_packet(make_pkt(0, 9), 0.9)
        k = 41
        while k * config.TICK <= 1.0:
            eng.tick(k * config.TICK)
            ctl.tick(k * config.TICK)
            k += 1
        self.assertEqual(eng.stats.rejected_old_epoch, 1)
        self.assertEqual(eng.stats.old_epoch_samples, P)
        self.assertEqual(eng.output, ctl.output)
        self.assertEqual(eng.trajectory, ctl.trajectory)


class LatePacketTest(unittest.TestCase):
    def test_late_packet_cannot_rewrite_played_interval(self):
        eng = PlayoutEngine()
        # packet 1 is never delivered on time -> its interval gets concealed
        pkts = [make_pkt(0, i) for i in (0, 2, 3, 4, 5, 6, 7)]
        drive(eng, pkts, 0.0, 0.4)
        self.assertGreaterEqual(eng.playout_pos, 2 * P)
        out_len = len(eng.output)
        prefix = eng.output.tobytes()
        pos_now = eng.playout_pos
        # the missing packet finally arrives, long after its deadline
        eng.on_packet(make_pkt(0, 1), 0.5)
        eng.tick(0.5)
        self.assertEqual(eng.stats.late, 1)
        self.assertEqual(eng.stats.late_samples, P)
        # keep playing; the already-emitted prefix must be untouched
        drive(eng, [make_pkt(0, i) for i in (8, 9)], 0.42, 0.7)
        self.assertEqual(eng.output.tobytes()[:out_len * 4], prefix)
        self.assertTrue(all(s[0] >= pos_now or s[0] + s[1] <= pos_now
                            for s in eng.real_spans))
        self.assertTrue(spans_ok(eng))


class ShrinkTest(unittest.TestCase):
    def test_delay_shrink_never_replays(self):
        eng = PlayoutEngine()
        # 12 packets arrive at once -> level far above target -> shrink
        drive(eng, [make_pkt(0, i) for i in range(12)], 0.0, 0.9)
        self.assertGreater(eng.stats.dropped_shrink_samples, 0)
        self.assertTrue(spans_ok(eng))
        positions = [rec.src_pos for rec in eng.trajectory]
        self.assertTrue(all(b >= a for a, b in zip(positions, positions[1:])))
        # no source position is ever emitted twice
        seen = set()
        for start, length in eng.real_spans:
            for pos in range(start, start + length):
                self.assertNotIn(pos, seen)
                seen.add(pos)


class TraceabilityTest(unittest.TestCase):
    def test_duplicate_traced(self):
        eng = PlayoutEngine()
        pkt = make_pkt(0, 0)
        eng.on_packet(pkt, recv_of(pkt))
        eng.on_packet(pkt, recv_of(pkt) + 0.004)
        eng.tick(0.1)
        self.assertEqual(eng.stats.stored, 1)
        self.assertEqual(eng.stats.duplicates, 1)
        self.assertEqual(eng.stats.duplicate_samples, P)

    def test_overflow_traced(self):
        eng = PlayoutEngine(capacity=4)
        drive(eng, [make_pkt(0, i) for i in range(7)], 0.0, 0.1)
        self.assertEqual(eng.stats.overflow, 3)
        self.assertEqual(eng.stats.overflow_samples, 3 * P)
        self.assertEqual(len(eng.buffer.packets), 4)

    def test_missing_concealed_and_marked(self):
        eng = PlayoutEngine()
        pkts = [make_pkt(0, i) for i in (0, 1, 2, 4, 5)]   # packet 3 lost
        drive(eng, pkts, 0.0, 0.27)
        self.assertEqual(eng.stats.missing_packets, 1)
        self.assertEqual(eng.stats.missing_samples, P)
        # no real sample was emitted for the concealed interval [3P, 4P)
        self.assertFalse(any(s < 4 * P and s + n > 3 * P
                             for s, n in eng.real_spans))
        # ... and the output carries a contiguous run of >= P synthesized
        # (marked) samples for it
        run = best = 0
        for f in eng.output_flags:
            run = run + 1 if f else 0
            best = max(best, run)
        self.assertGreaterEqual(best, P)
        # real playout resumes right after the gap
        self.assertIn((4 * P, P), eng.real_spans)
        # the lost packet arriving now is traced as late
        eng.on_packet(make_pkt(0, 3), 0.6)
        eng.tick(0.6)
        self.assertEqual(eng.stats.late, 1)


if __name__ == '__main__':
    unittest.main()

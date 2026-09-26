import unittest

from xsplice import Segment, compile_plan

SRC = {"s": [0.0] * 64, "u": [0.0] * 64}


class IntervalMappingTest(unittest.TestCase):
    def test_forward_interval(self):
        plan = compile_plan([Segment("a", "s", 4, 12, 3)], SRC, 48000)
        (iv,) = plan.intervals
        self.assertEqual((iv.out_start, iv.out_end), (3, 11))
        self.assertEqual((iv.src_id, iv.src_first, iv.step), ("s", 4, 1))
        self.assertEqual(iv.source_at(3), 4)
        self.assertEqual(iv.source_at(10), 11)

    def test_reversed_interval(self):
        plan = compile_plan([Segment("a", "s", 12, 4, 3)], SRC, 48000)
        (iv,) = plan.intervals
        self.assertEqual((iv.src_first, iv.step), (11, -1))
        self.assertEqual(iv.source_at(3), 11)
        self.assertEqual(iv.source_at(10), 4)

    def test_crossfade_region_exists_once_on_timeline(self):
        segs = [
            Segment("a", "s", 0, 16, 0, fade_out=6),
            Segment("b", "u", 0, 16, 10, fade_in=6),
        ]
        plan = compile_plan(segs, SRC, 48000)
        self.assertEqual(plan.total, 26)  # 16 + 16 - 6, overlap counted once
        a, b = plan.intervals
        self.assertEqual((a.out_start, a.out_end), (0, 16))
        self.assertEqual((b.out_start, b.out_end), (10, 26))
        # The shared region [10, 16) maps into both sources.
        for t in range(10, 16):
            hits = plan.output_to_source(t)
            self.assertEqual(len(hits), 2)
            self.assertEqual({h[2] for h in hits}, {"a", "b"})

    def test_output_to_source_single_and_none(self):
        plan = compile_plan([Segment("a", "s", 0, 8, 4)], SRC, 48000)
        self.assertEqual(plan.output_to_source(4), (("s", 0, "a"),))
        self.assertEqual(plan.output_to_source(11), (("s", 7, "a"),))
        with self.assertRaises(IndexError):
            plan.output_to_source(12)
        with self.assertRaises(IndexError):
            plan.output_to_source(-1)

    def test_output_to_source_reversed_indices(self):
        plan = compile_plan([Segment("a", "s", 8, 0, 0)], SRC, 48000)
        self.assertEqual(plan.output_to_source(0), (("s", 7, "a"),))
        self.assertEqual(plan.output_to_source(7), (("s", 0, "a"),))


class SourceToOutputTest(unittest.TestCase):
    def test_forward_roundtrip(self):
        plan = compile_plan([Segment("a", "s", 4, 12, 3)], SRC, 48000)
        for src_index in range(4, 12):
            t = plan.source_to_output("a", src_index)
            self.assertEqual(plan.output_to_source(t), (("s", src_index, "a"),))

    def test_reversed_roundtrip(self):
        plan = compile_plan([Segment("a", "s", 12, 4, 3)], SRC, 48000)
        for src_index in range(4, 12):
            t = plan.source_to_output("a", src_index)
            self.assertEqual(plan.output_to_source(t), (("s", src_index, "a"),))

    def test_outside_range_returns_none(self):
        plan = compile_plan([Segment("a", "s", 4, 12, 3)], SRC, 48000)
        self.assertIsNone(plan.source_to_output("a", 3))
        self.assertIsNone(plan.source_to_output("a", 12))

    def test_unknown_segment_raises(self):
        plan = compile_plan([Segment("a", "s", 4, 12, 3)], SRC, 48000)
        with self.assertRaises(KeyError):
            plan.source_to_output("nope", 5)


if __name__ == "__main__":
    unittest.main()

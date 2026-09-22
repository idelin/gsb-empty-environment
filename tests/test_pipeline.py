import unittest

from transducers import (
    BoundedBuffer,
    BufferFull,
    CollectSink,
    NotAccepted,
    Pipeline,
    PipelineError,
    RejectedInput,
    Transducer,
    compose,
    run_pipeline,
)

from .helpers import dedup, expander, parity_gate, terminator


def demo_stages():
    return [expander(), dedup(), terminator()]


class TestBoundedBuffer(unittest.TestCase):
    def test_capacity_enforced(self):
        buf = BoundedBuffer(2)
        buf.put("a")
        buf.put("b")
        with self.assertRaises(BufferFull):
            buf.put("c")
        self.assertEqual(buf.max_occupancy, 2)

    def test_capacity_must_be_positive(self):
        with self.assertRaises(ValueError):
            BoundedBuffer(0)


class TestConstruction(unittest.TestCase):
    def test_empty_pipeline_rejected(self):
        with self.assertRaises(ValueError):
            Pipeline([])

    def test_capacity_count_mismatch(self):
        with self.assertRaises(ValueError):
            Pipeline(demo_stages(), capacities=[1])

    def test_alphabet_mismatch_detected(self):
        wide = Transducer.mapper({"a"}, {"a", "x"}, lambda s: (s, "x"))
        narrow = Transducer.filter({"a"}, lambda s: True)
        with self.assertRaises(ValueError):
            Pipeline([wide, narrow])


class TestCompositionSemantics(unittest.TestCase):
    def test_pipeline_matches_reference(self):
        stages = demo_stages()
        data = list("aabbca")
        self.assertEqual(run_pipeline(stages, data), compose(stages, data))

    def test_epsilon_outputs_flow(self):
        # 'c' vanishes (epsilon) inside the first stage, duplicates in the
        # second; the pipeline must not emit spurious symbols for them.
        out = run_pipeline(demo_stages(), list("accb"))
        self.assertEqual(out, ["a", "x", "b", "END"])

    def test_one_to_many_order_preserved(self):
        # One upstream input produces several outputs; their relative order
        # must survive every downstream stage.
        fanout = Transducer.mapper({"a"}, {"p", "q", "r"}, lambda s: ("p", "q", "r"))
        echo = Transducer.filter({"p", "q", "r"}, lambda s: True)
        out = run_pipeline([fanout, echo], ["a", "a"], capacities=[1])
        self.assertEqual(out, ["p", "q", "r", "p", "q", "r"])

    def test_single_stage_no_buffers(self):
        out = run_pipeline([expander()], list("abc"))
        self.assertEqual(out, ["a", "x", "b"])

    def test_empty_input_still_finalizes(self):
        out = run_pipeline(demo_stages(), [])
        self.assertEqual(out, ["END"])


class TestBackpressure(unittest.TestCase):
    def test_buffer_occupancy_never_exceeds_capacity(self):
        for capacity in (1, 2, 3):
            pipe = Pipeline(demo_stages(), capacities=[capacity, capacity])
            pipe.feed_chunk(list("aabcaabbcaa"))
            pipe.close()
            for buf in pipe.buffers:
                self.assertLessEqual(buf.max_occupancy, capacity)
            self.assertGreaterEqual(pipe.buffers[0].max_occupancy, 1)

    def test_in_flight_data_is_bounded(self):
        stages = demo_stages()
        pipe = Pipeline(stages, capacities=[1, 1])
        pipe.feed_chunk(list("a" * 1000))
        pipe.close()
        bound = sum(t.max_outputs_per_step() for t in stages) + 2  # 2 buffers
        self.assertLessEqual(pipe.max_in_flight(), bound)

    def test_tightest_buffers_still_correct(self):
        stages = demo_stages()
        data = list("abcaacbbca")
        self.assertEqual(
            run_pipeline(stages, data, capacities=[1, 1]),
            compose(stages, data),
        )


class TestCancellation(unittest.TestCase):
    def test_downstream_stop_cancels_upstream(self):
        sink = CollectSink(limit=3)
        pipe = Pipeline(demo_stages(), sink=sink)
        consumed = pipe.feed_chunk("a" * 10_000)
        self.assertTrue(pipe.cancelled)
        self.assertEqual(sink.items, ["a", "x", "a"])
        # The source was cancelled: only a handful of the 10k symbols used.
        self.assertLess(consumed, 10)
        self.assertLessEqual(pipe.stats.source_consumed, consumed)

    def test_feed_after_cancel_is_refused(self):
        sink = CollectSink(limit=1)
        pipe = Pipeline([expander()], sink=sink)
        pipe.feed("a")
        self.assertTrue(pipe.cancelled)
        self.assertFalse(pipe.feed("b"))

    def test_close_after_cancel_is_safe(self):
        sink = CollectSink(limit=1)
        pipe = Pipeline([expander()], sink=sink)
        pipe.feed("a")
        pipe.close()  # must not raise, must not emit final outputs
        self.assertEqual(sink.items, ["a"])


class TestTermination(unittest.TestCase):
    def test_final_outputs_emitted_in_order(self):
        stages = demo_stages()
        out = run_pipeline(stages, list("ab"))
        self.assertEqual(out[-1], "END")
        self.assertEqual(out, compose(stages, list("ab")))

    def test_non_accepting_end_state_raises(self):
        pipe = Pipeline([parity_gate()])
        pipe.feed_chunk(["a", "b"])
        with self.assertRaises(NotAccepted):
            pipe.close()

    def test_accepting_end_state_ok(self):
        out = run_pipeline([parity_gate()], ["a", "b", "a", "a", "a"])
        self.assertEqual(out, ["a", "b", "a", "a", "a"])

    def test_close_is_idempotent(self):
        pipe = Pipeline(demo_stages())
        pipe.feed_chunk(list("ab"))
        pipe.close()
        first = pipe.output
        pipe.close()
        self.assertEqual(pipe.output, first)

    def test_feed_after_close_raises(self):
        pipe = Pipeline([expander()])
        pipe.close()
        with self.assertRaises(PipelineError):
            pipe.feed("a")

    def test_rejected_symbol_raises(self):
        pipe = Pipeline([expander()])
        with self.assertRaises(RejectedInput):
            pipe.feed("zzz")


if __name__ == "__main__":
    unittest.main()

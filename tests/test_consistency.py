"""Chunking invariance and randomized equivalence with the reference semantics."""
import random
import unittest

from transducers import CollectSink, Pipeline, Transducer, compose

from .helpers import dedup, expander, parity_gate, random_transducer, terminator


def feed_in_chunks(pipe, items, chunk_sizes):
    """Feed items using the given sequence of chunk sizes (cycled)."""
    index = 0
    step = 0
    sizes = list(chunk_sizes) or [1]
    while index < len(items):
        size = sizes[step % len(sizes)]
        pipe.feed_chunk(items[index : index + size])
        index += size
        step += 1


class TestChunkingInvariance(unittest.TestCase):
    def test_whole_equals_arbitrary_chunks(self):
        stages = [expander(), dedup(), terminator()]
        data = list("aabbcabcaacbbac")
        whole = Pipeline(stages)
        whole.feed_chunk(data)
        whole.close()
        expected = whole.output
        chunkings = [
            [1],
            [2, 3, 1, 5],
            [len(data)],
            [1, 1, 4, 1, 2, 1, 1, 3],
        ]
        for sizes in chunkings:
            with self.subTest(sizes=sizes):
                pipe = Pipeline([expander(), dedup(), terminator()],
                                capacities=[1, 2])
                feed_in_chunks(pipe, data, sizes)
                pipe.close()
                self.assertEqual(pipe.output, expected)

    def test_chunk_boundaries_inside_fanout(self):
        # Chunks of size 1 force every 'a' -> ('a','x') fan-out to be
        # flushed across buffer boundaries; result must not change.
        stages = [expander(), dedup()]
        data = list("ababba")
        reference = compose(stages, data)
        for size in (1, 2, 3):
            pipe = Pipeline([expander(), dedup()], capacities=[1])
            feed_in_chunks(pipe, data, [size])
            pipe.close()
            self.assertEqual(pipe.output, reference)


class TestRandomizedEquivalence(unittest.TestCase):
    def test_random_pipelines_match_reference(self):
        rng = random.Random(20260922)
        alphabet = ("a", "b", "c")
        trials = 60
        for trial in range(trials):
            n_stages = rng.randint(1, 4)
            blueprints = [
                random_transducer(rng, alphabet, rng.randint(1, 4), rng.randint(0, 3),
                                  name=f"t{trial}:{i}")
                for i in range(n_stages)
            ]
            data = [rng.choice(alphabet) for _ in range(rng.randint(0, 40))]
            expected = compose(blueprints, data)
            capacities = [rng.randint(1, 3) for _ in range(n_stages - 1)]

            def fresh():
                # Rebuild identical machines so each run gets fresh state.
                return [
                    Transducer(
                        states=b.states, alphabet_in=b.alphabet_in,
                        alphabet_out=b.alphabet_out, initial=b.initial,
                        finals=b.finals,
                        transitions={
                            k: (v[0], v[1]) for k, v in b._transitions.items()
                        },
                        final_outputs=dict(b._final_outputs), name=b.name,
                    )
                    for b in blueprints
                ]

            with self.subTest(trial=trial, n_stages=n_stages):
                whole = Pipeline(fresh(), capacities=capacities)
                whole.feed_chunk(data)
                whole.close()
                self.assertEqual(whole.output, expected)

                chunked = Pipeline(fresh(), capacities=capacities)
                feed_in_chunks(chunked, data, [rng.randint(1, 5) for _ in range(3)])
                chunked.close()
                self.assertEqual(chunked.output, expected)

    def test_random_machines_bounded_memory(self):
        rng = random.Random(7)
        alphabet = ("a", "b")
        for _ in range(20):
            stages = [
                random_transducer(rng, alphabet, 3, 2, name=f"m{i}")
                for i in range(3)
            ]
            capacities = [1, 1]
            pipe = Pipeline(stages, capacities=capacities)
            pipe.feed_chunk(rng.choice(alphabet) for _ in range(500))
            pipe.close()
            for buf, cap in zip(pipe.buffers, capacities):
                self.assertLessEqual(buf.max_occupancy, cap)
            for stage, t in zip(pipe._stages, stages):
                self.assertLessEqual(stage.max_pending, t.max_outputs_per_step())


class TestCancellationConsistency(unittest.TestCase):
    def test_prefix_property(self):
        # Cancelling after k outputs yields exactly the first k outputs of
        # the uncancelled run.
        stages = [expander(), dedup(), terminator()]
        data = list("abcaab")
        full = compose(stages, data)
        for k in range(1, len(full)):
            sink_limited = CollectSink(limit=k)
            pipe = Pipeline([expander(), dedup(), terminator()], sink=sink_limited)
            pipe.feed_chunk(data)
            pipe.close()
            self.assertEqual(sink_limited.items, full[:k])


if __name__ == "__main__":
    unittest.main()

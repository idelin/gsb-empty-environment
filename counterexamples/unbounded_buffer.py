"""Counterexample: stage-at-a-time composition needs unbounded memory.

Composing transducers by running each stage over the *whole* intermediate
string materialises every intermediate string in full; peak memory grows
with the input size.  A bounded streaming pipeline keeps a constant
footprint for the same input.  Run: python3 counterexamples/unbounded_buffer.py
"""
import _bootstrap  # noqa: F401  (sets sys.path)

from transducers import Pipeline, Transducer


def naive_stage_at_a_time(transducers, items):
    """BUG: buffers each whole intermediate string; peak grows with input."""
    peak = 0
    current = list(items)
    for transducer in transducers:
        state = transducer.initial
        nxt = []
        for symbol in current:
            state, outputs = transducer.step(state, symbol)
            nxt.extend(outputs)
            peak = max(peak, len(nxt))  # whole intermediate string in memory
        current = nxt
    return current, peak


def main():
    fanout = Transducer.mapper({"a"}, {"a", "x"}, lambda s: (s, "x"),
                               name="fanout")
    echo = Transducer.filter({"a", "x"}, lambda s: True, name="echo")
    stages = [fanout, echo]

    sizes = [1_000, 10_000, 100_000]
    print("naive stage-at-a-time composition:")
    naive_peaks = []
    for n in sizes:
        _, peak = naive_stage_at_a_time(stages, ["a"] * n)
        naive_peaks.append(peak)
        print(f"  input {n:>7} -> peak intermediate buffer {peak:>8}")
    assert naive_peaks[-1] > naive_peaks[0] * 10, "peak must grow with input"

    print("bounded streaming pipeline (capacity 1):")
    for n in sizes:
        pipe = Pipeline(
            [
                Transducer.mapper({"a"}, {"a", "x"}, lambda s: (s, "x")),
                Transducer.filter({"a", "x"}, lambda s: True),
            ],
            capacities=[1],
        )
        pipe.feed_chunk(iter(["a"] * n))
        pipe.close()
        print(f"  input {n:>7} -> max in-flight {pipe.max_in_flight():>8}")
        # bound = fan-out (2) + echo pending (1) + buffer capacity (1)
        assert pipe.max_in_flight() <= 4, "in-flight data must stay constant"

    print("COUNTEREXAMPLE REPRODUCED: unbounded intermediate buffers grow with "
          "input size; the bounded pipeline stays constant")


if __name__ == "__main__":
    main()

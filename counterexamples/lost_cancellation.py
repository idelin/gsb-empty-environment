"""Counterexample: an eager driver ignores downstream cancellation.

If the sink only wants the first few outputs but the driver keeps pulling
the source to exhaustion, work (and potentially an infinite stream) is
wasted.  A cancelling pipeline stops the upstream almost immediately.
Run: python3 counterexamples/lost_cancellation.py
"""
import _bootstrap  # noqa: F401  (sets sys.path)

from transducers import CollectSink, Pipeline, Transducer


def naive_eager_take(transducers, source, k):
    """BUG: consumes the entire source, then keeps only k outputs."""
    pulls = 0
    current = []
    for symbol in source:          # drains the whole (maybe infinite) source
        pulls += 1
        current.append(symbol)
    for transducer in transducers:
        state = transducer.initial
        nxt = []
        for symbol in current:
            state, outputs = transducer.step(state, symbol)
            nxt.extend(outputs)
        current = nxt
    return current[:k], pulls


def main():
    alphabet = ("a", "x")
    fanout = Transducer.mapper({"a"}, alphabet, lambda s: (s, "x"), name="fanout")
    echo = Transducer.filter(alphabet, lambda s: True, name="echo")
    n = 300_000
    keep = 3

    _, naive_pulls = naive_eager_take([fanout, echo], iter(["a"] * n), keep)
    print(f"naive eager driver : pulled {naive_pulls} source symbols to emit {keep}")

    sink = CollectSink(limit=keep)
    pipe = Pipeline(
        [
            Transducer.mapper({"a"}, alphabet, lambda s: (s, "x")),
            Transducer.filter(alphabet, lambda s: True),
        ],
        capacities=[1],
        sink=sink,
    )
    consumed = pipe.feed_chunk(iter(["a"] * n))
    print(f"cancelling pipeline: pulled {consumed} source symbols to emit {keep}")
    print(f"outputs            : {sink.items}")

    assert naive_pulls == n
    assert consumed <= 10, "upstream must be cancelled almost immediately"
    assert sink.items == ["a", "x", "a"]
    print("COUNTEREXAMPLE REPRODUCED: eager drivers cannot stop the upstream; "
          "cancellation keeps source consumption bounded")


if __name__ == "__main__":
    main()

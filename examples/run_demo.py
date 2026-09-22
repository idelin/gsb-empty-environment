"""End-to-end demo: a three-stage transducer pipeline.

Stage 1 ``expander``   : 'a' -> ('a','x')  'b' -> ('b',)  'c' -> epsilon
Stage 2 ``dedup``      : drops a symbol equal to the previously kept one
Stage 3 ``terminator`` : identity, appends 'END' at end-of-stream

Run directly:  python3 examples/run_demo.py
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from transducers import CollectSink, Pipeline, Transducer, compose, run_pipeline


def build_stages():
    expander = Transducer.mapper(
        {"a", "b", "c"},
        {"a", "b", "x"},
        lambda s: {"a": ("a", "x"), "b": ("b",), "c": ()}[s],
        name="expander",
    )
    alphabet = ("a", "b", "x")
    states = {"start"} | {f"seen:{s}" for s in alphabet}
    transitions = {}
    for state in states:
        for symbol in alphabet:
            if state == "start" or state != f"seen:{symbol}":
                transitions[(state, symbol)] = (f"seen:{symbol}", (symbol,))
            else:
                transitions[(state, symbol)] = (state, ())
    dedup = Transducer(
        states=states, alphabet_in=alphabet, alphabet_out=alphabet,
        initial="start", finals=states, transitions=transitions, name="dedup",
    )
    terminator = Transducer(
        states={"q"}, alphabet_in=alphabet, alphabet_out=alphabet + ("END",),
        initial="q", finals={"q"},
        transitions={("q", s): ("q", (s,)) for s in alphabet},
        final_outputs={"q": ("END",)},
        name="terminator",
    )
    return [expander, dedup, terminator]


def main():
    data = list("aabbcabca")

    print("== whole-string run vs non-streaming reference ==")
    stages = build_stages()
    whole = run_pipeline(build_stages(), data, capacities=[1, 1])
    reference = compose(build_stages(), data)
    print("input    :", "".join(data))
    print("pipeline :", whole)
    print("reference:", reference)
    assert whole == reference

    print("\n== arbitrary chunkings give identical results ==")
    for chunk_size in (1, 2, 3, 4, len(data)):
        out = run_pipeline(build_stages(), data, capacities=[1, 1],
                           chunk_size=chunk_size)
        print(f"chunk_size={chunk_size:>2}: {out}")
        assert out == whole

    print("\n== bounded buffers: high-water marks ==")
    pipe = Pipeline(build_stages(), capacities=[1, 2])
    pipe.feed_chunk(data * 1000)
    pipe.close()
    for buf in pipe.buffers:
        print(f"{buf.name}: capacity={buf.capacity} "
              f"max_occupancy={buf.max_occupancy}")
    print(f"max in-flight items: {pipe.max_in_flight()}")

    print("\n== downstream stop cancels the upstream ==")
    sink = CollectSink(limit=4)
    pipe = Pipeline(build_stages(), capacities=[1, 1], sink=sink)
    consumed = pipe.feed_chunk(iter(list("abca") * 100_000))
    print(f"sink took {len(sink.items)} items -> {sink.items}")
    print(f"cancelled={pipe.cancelled}, source symbols consumed={consumed}")

    print("\nall demo checks passed")


if __name__ == "__main__":
    main()

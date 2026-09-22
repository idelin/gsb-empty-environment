"""Counterexample: restarting automaton state at every chunk boundary.

A naive "streaming" runner that re-initialises each transducer at the start
of every chunk produces different output than a stateful streaming run.
Run directly:  python3 counterexamples/chunk_restart.py
"""
import _bootstrap  # noqa: F401  (sets sys.path)

from transducers import Pipeline, Transducer, run_transducer


def dedup():
    """Stateful filter: drop a symbol equal to the previously kept one."""
    alphabet = ("a", "b")
    states = {"start", "seen:a", "seen:b"}
    transitions = {}
    for state in states:
        for symbol in alphabet:
            if state == "start" or state != f"seen:{symbol}":
                transitions[(state, symbol)] = (f"seen:{symbol}", (symbol,))
            else:
                transitions[(state, symbol)] = (state, ())  # epsilon: drop
    return Transducer(
        states=states, alphabet_in=alphabet, alphabet_out=alphabet,
        initial="start", finals=states, transitions=transitions, name="dedup",
    )


def naive_chunked_run(transducer, chunks):
    """BUG: resets the machine to its initial state for every chunk."""
    out = []
    for chunk in chunks:
        state = transducer.initial
        for symbol in chunk:
            state, outputs = transducer.step(state, symbol)
            out.extend(outputs)
    return out


def main():
    chunks = [list("a"), list("a"), list("b")]
    whole = list("aab")

    naive = naive_chunked_run(dedup(), chunks)
    correct = run_transducer(dedup(), whole)

    print("input (whole) :", "".join(whole))
    print("naive chunks  :", naive, " (state restarted per chunk)")
    print("correct stream:", correct)
    assert naive != correct, "expected the naive runner to diverge"
    assert correct == ["a", "b"]

    pipe = Pipeline([dedup()])
    for chunk in chunks:
        pipe.feed_chunk(chunk)
    pipe.close()
    assert pipe.output == correct, "pipeline must be chunk-invariant"
    print("pipeline      :", pipe.output, " (chunk-invariant)")
    print("COUNTEREXAMPLE REPRODUCED: per-chunk state reset breaks streaming semantics")


if __name__ == "__main__":
    main()

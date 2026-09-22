"""Counterexample: a LIFO intermediate buffer reverses fan-out order.

When one upstream input expands to several outputs, buffering them on a
stack (LIFO) instead of a queue (FIFO) reverses their order before the
downstream stage sees them.  Run: python3 counterexamples/lifo_buffer.py
"""
import _bootstrap  # noqa: F401  (sets sys.path)

from transducers import Pipeline, Transducer, compose


def naive_lifo_run(upstream, downstream, items):
    """BUG: intermediate buffer is a stack, so fan-out order is reversed."""
    buffer = []
    out = []
    state_up = upstream.initial
    state_down = downstream.initial
    for symbol in items:
        state_up, outputs = upstream.step(state_up, symbol)
        buffer.extend(outputs)          # push p, q, r ...
        while buffer:
            item = buffer.pop()         # ... but pop r, q, p
            state_down, produced = downstream.step(state_down, item)
            out.extend(produced)
    return out


def main():
    fanout = Transducer.mapper({"a"}, {"p", "q", "r"}, lambda s: ("p", "q", "r"),
                               name="fanout")
    echo = Transducer.filter({"p", "q", "r"}, lambda s: True, name="echo")
    data = ["a", "a"]

    naive = naive_lifo_run(fanout, echo, data)
    correct = compose([fanout, echo], data)

    print("input         :", data)
    print("naive (LIFO)  :", naive)
    print("correct (FIFO):", correct)
    assert naive != correct, "expected LIFO order to diverge"
    assert correct == ["p", "q", "r", "p", "q", "r"]

    pipe = Pipeline([fanout, echo], capacities=[1])
    pipe.feed_chunk(data)
    pipe.close()
    assert pipe.output == correct
    print("pipeline      :", pipe.output)
    print("COUNTEREXAMPLE REPRODUCED: LIFO buffering reverses one-to-many outputs")


if __name__ == "__main__":
    main()

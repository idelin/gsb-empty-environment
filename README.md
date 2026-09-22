# Transducer Pipeline

Streaming composition of explicit finite-state **filter / transducer
automata** (deterministic GSMs) with epsilon outputs, bounded intermediate
buffers, backpressure, downstream cancellation and end-of-stream handling.
Pure Python 3.11+ standard library; no regex or automata packages.

## Layout

- `transducers/core.py` — `Transducer`: explicit states, input/output
  alphabets and transition table `(state, symbol) -> (next, outputs)`;
  `outputs == ()` is the epsilon output; optional per-state `final_outputs`
  emitted at end-of-stream; `finals` define acceptance.
- `transducers/pipeline.py` — `Pipeline`: left-to-right streaming
  composition over `BoundedBuffer`s with a single-threaded scheduler.
- `transducers/reference.py` — non-streaming reference semantics
  (`compose`): the definition the streaming pipeline is checked against.
- `examples/run_demo.py` — runnable end-to-end sample.
- `counterexamples/` — independently executable scripts, each reproducing
  one classic bug that the pipeline design rules out.
- `tests/` — `unittest` suite.

## Semantics and invariants

- **Composition**: `Pipeline([t1, ..., tn])` computes exactly
  `compose([t1, ..., tn])` — verified against `reference.py` by randomized
  property tests (`tests/test_consistency.py`).
- **Epsilon outputs**: a transition may emit zero symbols; epsilon never
  produces spurious items downstream.
- **One-to-many order**: outputs of a single upstream step are enqueued
  FIFO, so `a -> (p, q, r)` reaches downstream in order (see
  `counterexamples/lifo_buffer.py` for the failure mode).
- **Bounded buffers / backpressure**: every intermediate buffer has a hard
  capacity (default 1, the tightest possible). A stage only fires a new
  input when its pending-output slot is empty, so in-flight data is bounded
  by `sum(capacities) + sum(max_outputs_per_step)` — independent of input
  length (`counterexamples/unbounded_buffer.py` shows the unbounded
  alternative).
- **Cancellation**: when the sink declines an item (`offer` returns False,
  e.g. `CollectSink(limit=k)`), the pipeline is cancelled: in-flight items
  are dropped, upstream stages stop firing and the source is no longer
  consumed (`counterexamples/lost_cancellation.py`).
- **Termination**: `close()` flushes the pipeline, emits each stage's
  `final_outputs` in upstream-to-downstream order, and raises
  `NotAccepted` if any stage rests in a non-final state.
- **Chunking invariance**: automaton state lives in the stages, not in the
  driver, so feeding the input whole or in arbitrary chunks gives identical
  output (`counterexamples/chunk_restart.py` shows the buggy alternative).

## Run

```sh
python3 -m unittest discover -v          # full test suite
python3 examples/run_demo.py             # end-to-end sample
python3 counterexamples/chunk_restart.py # any counterexample, standalone
```

## Minimal usage

```python
from transducers import Transducer, run_pipeline

expander = Transducer.mapper({"a", "b"}, {"a", "b", "x"},
                             lambda s: (s, "x") if s == "a" else (s,))
drop_x   = Transducer.filter({"a", "b", "x"}, lambda s: s != "x")

print(run_pipeline([expander, drop_x], list("aba")))
# ['a', 'b', 'a']
```

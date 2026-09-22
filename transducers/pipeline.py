"""Streaming pipeline composition of transducers over bounded buffers.

Stages are connected by bounded FIFO buffers.  A single-threaded scheduler
pumps items downstream with strict backpressure:

* a stage only fires a new input symbol when its pending-output slot is
  empty, so in-flight data per stage is bounded by
  ``max_outputs_per_step`` plus the capacity of its input buffer;
* intermediate buffers never exceed their configured capacity
  (enforced by :class:`BoundedBuffer` and witnessed by ``max_occupancy``);
* when the sink stops accepting items the whole pipeline is *cancelled*:
  upstream stages stop firing and the source is no longer consumed.

Because every stage keeps its own current state, feeding the input as one
string or as arbitrary chunks yields identical results, and the overall
output equals the mathematical composition of the stage transductions
(see :mod:`transducers.reference` for the non-streaming definition).
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field

from .core import NotAccepted, RejectedInput, Transducer

__all__ = [
    "BoundedBuffer",
    "BufferFull",
    "CollectSink",
    "Pipeline",
    "PipelineError",
    "PipelineStats",
    "run_pipeline",
]


class PipelineError(Exception):
    """Base class for pipeline failures."""


class BufferFull(PipelineError):
    """Internal guard: a bounded buffer was asked to exceed its capacity."""


class BoundedBuffer:
    """FIFO buffer with a hard capacity; records its high-water mark."""

    __slots__ = ("capacity", "name", "_items", "max_occupancy")

    def __init__(self, capacity, name="buffer"):
        if capacity < 1:
            raise ValueError(f"{name}: capacity must be >= 1, got {capacity}")
        self.capacity = capacity
        self.name = name
        self._items = deque()
        self.max_occupancy = 0

    def put(self, item):
        if len(self._items) >= self.capacity:
            raise BufferFull(f"{self.name}: capacity {self.capacity} exceeded")
        self._items.append(item)
        if len(self._items) > self.max_occupancy:
            self.max_occupancy = len(self._items)

    def pop(self):
        return self._items.popleft()

    def peek(self):
        return self._items[0]

    def clear(self):
        self._items.clear()

    @property
    def space(self):
        return self.capacity - len(self._items)

    def __len__(self):
        return len(self._items)

    def __bool__(self):
        return bool(self._items)


class CollectSink:
    """Default sink: collects items, optionally stopping after ``limit``.

    ``offer`` returns True while more items are wanted.  Once the limit is
    reached the item is still delivered but False is returned, which cancels
    the pipeline (downstream stop propagates upstream).
    """

    def __init__(self, limit=None):
        self.items = []
        self.limit = limit
        self.stopped = False
        self.closed = False

    def offer(self, item):
        if self.stopped:
            return False
        self.items.append(item)
        if self.limit is not None and len(self.items) >= self.limit:
            self.stopped = True
            return False
        return True

    def close(self):
        self.closed = True


@dataclass
class PipelineStats:
    """Observable evidence for backpressure and cancellation."""

    source_consumed: int = 0
    delivered: int = 0
    fired: list = field(default_factory=list)


class _Stage:
    __slots__ = ("transducer", "index", "state", "pending", "max_pending")

    def __init__(self, transducer, index):
        self.transducer = transducer
        self.index = index
        self.state = transducer.initial
        self.pending = deque()
        self.max_pending = 0


class Pipeline:
    """A left-to-right streaming composition of transducers.

    ``capacities`` holds one capacity per intermediate buffer
    (``len(transducers) - 1`` entries); defaults to 1 everywhere, the
    tightest possible bound.
    """

    def __init__(self, transducers, *, capacities=None, sink=None):
        transducers = list(transducers)
        if not transducers:
            raise ValueError("pipeline needs at least one transducer")
        for t in transducers:
            if not isinstance(t, Transducer):
                raise TypeError(f"expected Transducer, got {type(t).__name__}")
        n = len(transducers)
        if capacities is None:
            capacities = [1] * (n - 1)
        capacities = list(capacities)
        if len(capacities) != n - 1:
            raise ValueError(
                f"expected {n - 1} buffer capacities, got {len(capacities)}"
            )
        for i in range(n - 1):
            leaked = transducers[i].output_symbols() - transducers[i + 1].alphabet_in
            if leaked:
                raise ValueError(
                    f"stage {i} may emit {sorted(leaked)!r} which stage {i + 1} "
                    "does not accept as input"
                )

        self._stages = [_Stage(t, i) for i, t in enumerate(transducers)]
        self._buffers = [
            BoundedBuffer(c, name=f"buffer[{i}]") for i, c in enumerate(capacities)
        ]
        self._source = BoundedBuffer(1, name="source")
        self._sink = sink if sink is not None else CollectSink()
        self._cancelled = False
        self._closed = False
        self.stats = PipelineStats(fired=[0] * n)

    # ------------------------------------------------------------ inspect
    @property
    def cancelled(self):
        return self._cancelled

    @property
    def closed(self):
        return self._closed

    @property
    def buffers(self):
        return list(self._buffers)

    @property
    def stages(self):
        return [s.transducer for s in self._stages]

    def max_in_flight(self):
        """High-water bound on items held inside the pipeline at any time."""
        total = sum(b.max_occupancy for b in self._buffers)
        total += sum(s.max_pending for s in self._stages)
        return total

    # -------------------------------------------------------------- input
    def feed(self, item):
        """Offer one source symbol.  Returns False once cancelled/closed."""
        if self._closed:
            raise PipelineError("feed() after close()")
        if self._cancelled:
            return False
        if item not in self._stages[0].transducer.alphabet_in:
            raise RejectedInput(
                f"stage 0 ({self._stages[0].transducer.name}): symbol {item!r} "
                "not in input alphabet"
            )
        self._source.put(item)
        self._pump()
        return not self._cancelled

    def feed_chunk(self, items):
        """Feed an iterable; stops early on cancel.  Returns items consumed."""
        consumed = 0
        for item in items:
            if self._cancelled:
                break
            self.feed(item)
            consumed += 1
        return consumed

    # ----------------------------------------------------------- scheduler
    def _pump(self):
        while not self._cancelled:
            progress = False
            for i in range(len(self._stages) - 1, -1, -1):
                stage = self._stages[i]
                if self._flush(stage, i):
                    progress = True
                if self._cancelled:
                    break
                source = self._source if i == 0 else self._buffers[i - 1]
                # Backpressure: only fire when the pending slot is empty, so
                # a stage never runs ahead of its output buffer by more than
                # max_outputs_per_step symbols.
                if source and not stage.pending:
                    symbol = source.peek()
                    nxt, outs = stage.transducer.step(stage.state, symbol)
                    source.pop()
                    stage.state = nxt
                    stage.pending.extend(outs)
                    if len(stage.pending) > stage.max_pending:
                        stage.max_pending = len(stage.pending)
                    self.stats.fired[i] += 1
                    if i == 0:
                        self.stats.source_consumed += 1
                    progress = True
                    if self._flush(stage, i):
                        pass
            if not progress:
                return

    def _flush(self, stage, i):
        """Move pending outputs into the output channel; returns progress."""
        moved = False
        last = i == len(self._stages) - 1
        while stage.pending:
            if last:
                item = stage.pending[0]
                if not self._sink.offer(item):
                    self._cancel()
                    return moved
                stage.pending.popleft()
                self.stats.delivered += 1
                moved = True
            else:
                buf = self._buffers[i]
                if buf.space <= 0:
                    break
                buf.put(stage.pending.popleft())
                moved = True
        return moved

    def _cancel(self):
        self._cancelled = True
        for stage in self._stages:
            stage.pending.clear()
        for buf in self._buffers:
            buf.clear()
        self._source.clear()

    # ---------------------------------------------------------- termination
    def close(self):
        """Signal end-of-stream: flush, emit final outputs, check acceptance."""
        if self._closed:
            return
        self._closed = True
        if not self._cancelled:
            for i, stage in enumerate(self._stages):
                self._pump()
                if self._cancelled:
                    break
                stage.pending.extend(stage.transducer.final_output(stage.state))
                if len(stage.pending) > stage.max_pending:
                    stage.max_pending = len(stage.pending)
            self._pump()
        if not self._cancelled:
            bad = [
                (i, s.state)
                for i, s in enumerate(self._stages)
                if not s.transducer.accepts(s.state)
            ]
            if bad:
                raise NotAccepted(
                    "non-accepting end state(s): "
                    + ", ".join(f"stage {i} in {state!r}" for i, state in bad)
                )
        close = getattr(self._sink, "close", None)
        if callable(close):
            close()

    # ---------------------------------------------------------- convenience
    @property
    def output(self):
        """Collected output items (requires a :class:`CollectSink`)."""
        if not isinstance(self._sink, CollectSink):
            raise PipelineError("output is only available with CollectSink")
        return list(self._sink.items)


def run_pipeline(transducers, items, *, capacities=None, chunk_size=None):
    """Run ``items`` through a fresh pipeline and return the output list.

    ``chunk_size`` splits the feeding into chunks to demonstrate that
    chunking does not affect the result.
    """
    pipe = Pipeline(transducers, capacities=capacities)
    items = list(items)
    if chunk_size is None:
        pipe.feed_chunk(items)
    else:
        for start in range(0, len(items), chunk_size):
            pipe.feed_chunk(items[start : start + chunk_size])
    pipe.close()
    return pipe.output

"""Explicit finite-state transducers and bounded pipeline composition."""

from __future__ import annotations

from collections import deque
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass
import threading
from typing import Any


EPSILON = object()


class AutomatonError(ValueError):
    """The explicit automaton description is malformed."""


class RejectedInputError(AutomatonError):
    """An input or complete-string condition was rejected."""


class AlphabetMismatchError(AutomatonError):
    """Two adjacent pipeline stages do not share the required alphabet."""


class PipelineClosedError(RuntimeError):
    """An operation occurred after a bounded channel was closed."""


class StopConsumption(Exception):
    """Raised by a consumer to stop a pipeline and cancel its source."""


@dataclass(frozen=True)
class Transition:
    state: Any
    outputs: tuple[Any, ...]


@dataclass(frozen=True)
class RunResult:
    outputs: tuple[Any, ...]
    consumed: int
    peak_buffers: tuple[int, ...]
    stopped: bool


def _is_hashable(value: Any) -> bool:
    try:
        hash(value)
    except TypeError:
        return False
    return True


def _checked_alphabet(values: Iterable[Any], label: str) -> tuple[Any, ...]:
    alphabet = tuple(values)
    seen: set[Any] = set()
    for symbol in alphabet:
        if not _is_hashable(symbol):
            raise AutomatonError(f"{label} symbols must be hashable")
        if symbol is EPSILON:
            raise AutomatonError(f"{label} must not contain EPSILON")
        if symbol in seen:
            raise AutomatonError(f"{label} contains a duplicate symbol")
        seen.add(symbol)
    return alphabet


def _checked_outputs(value: Any, output_alphabet: frozenset[Any]) -> tuple[Any, ...]:
    if isinstance(value, (str, bytes)) or not isinstance(value, Sequence):
        raise AutomatonError("transition outputs must be a tuple")
    outputs = tuple(value)
    for symbol in outputs:
        if symbol is EPSILON:
            continue
        if not _is_hashable(symbol) or symbol not in output_alphabet:
            raise AutomatonError(f"output {symbol!r} is not in output alphabet")
    return outputs


class FiniteTransducer:
    def __init__(
        self,
        *,
        states: Iterable[Any],
        input_alphabet: Iterable[Any],
        output_alphabet: Iterable[Any],
        transitions: dict[Any, dict[Any, tuple[Any, ...] | Transition]],
        initial: Any,
        accepting: Iterable[Any],
        finish_outputs: dict[Any, tuple[Any, ...]] | None = None,
        halting: Iterable[Any] = (),
        name: str = "transducer",
    ) -> None:
        state_values = tuple(states)
        state_set: set[Any] = set()
        for state in state_values:
            if not _is_hashable(state):
                raise AutomatonError("states must be hashable")
            if state in state_set:
                raise AutomatonError(f"duplicate state {state!r}")
            state_set.add(state)
        states_frozen = frozenset(state_values)
        if initial not in states_frozen:
            raise AutomatonError("initial state is not a declared state")

        self.states = states_frozen
        self.input_alphabet = frozenset(_checked_alphabet(input_alphabet, "input alphabet"))
        out_values = _checked_alphabet(output_alphabet, "output alphabet")
        self.output_alphabet = frozenset(out_values)
        self.initial = initial
        self.accepting = frozenset(accepting)
        if not self.accepting <= states_frozen:
            raise AutomatonError("accepting contains an undeclared state")
        self.halting = frozenset(halting)
        if not self.halting <= states_frozen:
            raise AutomatonError("halting contains an undeclared state")
        if self.halting & self.accepting:
            raise AutomatonError("a state cannot be both halting and accepting")
        if initial in self.halting:
            raise AutomatonError("initial state cannot be halting")
        self.name = name

        normalized: dict[Any, dict[Any, Transition]] = {}
        for state, row in transitions.items():
            if state not in states_frozen:
                raise AutomatonError(f"transition source {state!r} is not a state")
            if state in self.halting:
                raise AutomatonError(f"halting state {state!r} cannot have transitions")
            if not isinstance(row, dict):
                raise AutomatonError("each transition row must be a dict")
            normalized_row: dict[Any, Transition] = {}
            seen_inputs: set[Any] = set()
            for symbol, target in row.items():
                if symbol not in self.input_alphabet:
                    raise AutomatonError(f"input {symbol!r} is not in input alphabet")
                if symbol in seen_inputs:
                    raise AutomatonError(f"duplicate transition from {state!r} on {symbol!r}")
                seen_inputs.add(symbol)
                if isinstance(target, Transition):
                    next_state = target.state
                    outputs = _checked_outputs(target.outputs, self.output_alphabet)
                    target = Transition(next_state, outputs)
                else:
                    if not isinstance(target, tuple) or len(target) != 2:
                        raise AutomatonError("transition value must be (state, outputs)")
                    next_state, raw_outputs = target
                    outputs = _checked_outputs(raw_outputs, self.output_alphabet)
                    target = Transition(next_state, outputs)
                if target.state not in states_frozen:
                    raise AutomatonError(f"transition target {target.state!r} is not a state")
                normalized_row[symbol] = target
            missing = self.input_alphabet - seen_inputs
            if missing:
                raise AutomatonError(f"state {state!r} is missing transitions for {sorted(missing, key=repr)!r}")
            normalized[state] = normalized_row
        if frozenset(normalized) != states_frozen - self.halting:
            missing_states = states_frozen - self.halting - frozenset(normalized)
            raise AutomatonError(f"states missing transition rows: {sorted(missing_states, key=str)!r}")
        self._transitions = normalized

        finish = dict(finish_outputs or {})
        normalized_finish: dict[Any, tuple[Any, ...]] = {}
        for state, raw_outputs in finish.items():
            if state not in states_frozen:
                raise AutomatonError(f"finish source {state!r} is not a state")
            if state in self.halting:
                raise AutomatonError(f"halting state {state!r} cannot have finish outputs")
            normalized_finish[state] = _checked_outputs(raw_outputs, self.output_alphabet)
        self.finish_outputs = normalized_finish
        self.state = initial

    def spawn(self) -> FiniteTransducer:
        clone = object.__new__(FiniteTransducer)
        clone.__dict__.update(self.__dict__)
        clone.state = self.initial
        return clone

    def step(self, symbol: Any) -> tuple[Any, ...]:
        if symbol not in self.input_alphabet:
            raise RejectedInputError(f"{self.name}: input {symbol!r} is not in its alphabet")
        row = self._transitions.get(self.state)
        if row is None:
            raise PipelineClosedError(f"{self.name}: halting state {self.state!r} received {symbol!r}")
        edge = row[symbol]
        self.state = edge.state
        return tuple(output for output in edge.outputs if output is not EPSILON)

    def finish(self) -> tuple[Any, ...]:
        if self.state in self.halting:
            return ()
        if self.state not in self.accepting:
            raise RejectedInputError(f"{self.name}: end state {self.state!r} is not accepting")
        return tuple(
            output
            for output in self.finish_outputs.get(self.state, ())
            if output is not EPSILON
        )

    def is_halting(self) -> bool:
        return self.state in self.halting


def _symbols(chunks: Iterable[Any]) -> Iterable[Any]:
    for chunk in chunks:
        if isinstance(chunk, (str, bytes)):
            yield from chunk
        elif isinstance(chunk, Sequence):
            yield from chunk
        else:
            yield chunk


def transduce(
    transducer: FiniteTransducer,
    chunks: Iterable[Any],
    *,
    atomic: bool = False,
) -> tuple[Any, ...]:
    """Reference semantics for one complete string."""
    worker = transducer.spawn()
    outputs: list[Any] = []
    symbols: Iterable[Any] = chunks if atomic else _symbols(chunks)
    for symbol in symbols:
        outputs.extend(worker.step(symbol))
    outputs.extend(worker.finish())
    return tuple(outputs)


def compose(stages: Sequence[FiniteTransducer], chunks: Iterable[Any]) -> tuple[Any, ...]:
    """Pure reference composition; chunk boundaries cannot affect it."""
    current: Iterable[Any] = _symbols(chunks)
    for stage in stages:
        current = transduce(stage, current, atomic=True)
    return tuple(current)


_END = object()


class _Coordinator:
    def __init__(self) -> None:
        self.condition = threading.Condition()
        self.error: BaseException | None = None
        self.halted = False

    def abort(self, error: BaseException) -> None:
        with self.condition:
            if self.error is None:
                self.error = error
            self.condition.notify_all()

    def mark_halted(self) -> None:
        with self.condition:
            self.halted = True
            self.condition.notify_all()

class _Aborted(RuntimeError):
    def __init__(self, error: BaseException) -> None:
        super().__init__(str(error))
        self.error = error


class _BoundedChannel:
    def __init__(self, capacity: int, coordinator: _Coordinator) -> None:
        self.capacity = capacity
        self.coordinator = coordinator
        self.condition = coordinator.condition
        self.queue: deque[Any] = deque()
        self.writer_closed = False
        self.reader_closed = False
        self.peak = 0

    def put(self, item: Any) -> None:
        with self.condition:
            while True:
                if self.coordinator.error is not None:
                    raise _Aborted(self.coordinator.error)
                if self.reader_closed:
                    raise PipelineClosedError("reader stopped before item was written")
                if len(self.queue) < self.capacity:
                    self.queue.append(item)
                    self.peak = max(self.peak, len(self.queue))
                    self.condition.notify_all()
                    return
                self.condition.wait(timeout=0.05)

    def get(self) -> Any:
        with self.condition:
            while True:
                if self.coordinator.error is not None:
                    self.queue.clear()
                    raise _Aborted(self.coordinator.error)
                if self.queue:
                    return self.queue.popleft()
                if self.writer_closed:
                    return _END
                if self.reader_closed:
                    raise PipelineClosedError("reader stopped while waiting for input")
                self.condition.wait(timeout=0.05)

    def close_writer(self) -> None:
        with self.condition:
            self.writer_closed = True
            self.condition.notify_all()

    def close_reader(self) -> None:
        with self.condition:
            self.reader_closed = True
            self.queue.clear()
            self.condition.notify_all()


def _emit_all(stage: FiniteTransducer, outputs: Sequence[Any], out: _BoundedChannel) -> None:
    for symbol in outputs:
        if symbol is EPSILON:
            continue
        out.put(symbol)


def _run_stage(
    stage: FiniteTransducer,
    source: _BoundedChannel,
    target: _BoundedChannel,
    coordinator: _Coordinator,
    errors: list[BaseException],
) -> None:
    worker = stage.spawn()
    try:
        while True:
            item = source.get()
            if item is _END:
                _emit_all(worker, worker.finish(), target)
                return
            _emit_all(worker, worker.step(item), target)
            if worker.is_halting():
                coordinator.mark_halted()
                source.close_reader()
                return
    except _Aborted:
        return
    except PipelineClosedError:
        return
    except BaseException as error:
        coordinator.abort(error)
        errors.append(error)
    finally:
        source.close_reader()
        target.close_writer()


def _run_source(chunks: Iterable[Any], target: _BoundedChannel) -> None:
    try:
        for symbol in _symbols(chunks):
            target.put(symbol)
    except (_Aborted, PipelineClosedError):
        return
    finally:
        target.close_writer()


class Pipeline:
    def __init__(self, stages: Sequence[FiniteTransducer], *, buffer_size: int = 2) -> None:
        self.stages = tuple(stages)
        if not self.stages:
            raise AutomatonError("pipeline requires at least one stage")
        if not all(isinstance(stage, FiniteTransducer) for stage in self.stages):
            raise AutomatonError("all pipeline stages must be FiniteTransducer instances")
        if not isinstance(buffer_size, int) or buffer_size < 1:
            raise AutomatonError("buffer_size must be a positive integer")
        for left, right in zip(self.stages, self.stages[1:]):
            if not left.output_alphabet <= right.input_alphabet:
                raise AlphabetMismatchError(
                    f"{left.name} output alphabet is not contained in {right.name} input alphabet"
                )
        self.buffer_size = buffer_size

    def run(
        self,
        chunks: Iterable[Any],
        *,
        max_outputs: int | None = None,
    ) -> tuple[Any, ...]:
        return self._execute(chunks, max_outputs=max_outputs, consumer=None).outputs

    def run_with_stats(
        self,
        chunks: Iterable[Any],
        *,
        max_outputs: int | None = None,
    ) -> RunResult:
        return self._execute(chunks, max_outputs=max_outputs, consumer=None)

    def consume(
        self,
        chunks: Iterable[Any],
        consumer: Callable[[Any], None],
    ) -> RunResult:
        return self._execute(chunks, max_outputs=None, consumer=consumer)

    def _execute(
        self,
        chunks: Iterable[Any],
        *,
        max_outputs: int | None,
        consumer: Callable[[Any], None] | None,
    ) -> RunResult:
        if max_outputs is not None and (not isinstance(max_outputs, int) or max_outputs < 0):
            raise AutomatonError("max_outputs must be None or a non-negative integer")

        coordinator = _Coordinator()
        channels = [_BoundedChannel(self.buffer_size, coordinator) for _ in range(len(self.stages) + 1)]
        errors: list[BaseException] = []
        threads: list[threading.Thread] = []
        consumed = 0

        def counted_symbols() -> Iterable[Any]:
            nonlocal consumed
            for symbol in _symbols(chunks):
                consumed += 1
                yield symbol

        source_thread = threading.Thread(
            target=_run_source,
            args=(counted_symbols(), channels[0]),
            name="pipeline-source",
            daemon=True,
        )
        threads.append(source_thread)
        for index, stage in enumerate(self.stages):
            thread = threading.Thread(
                target=_run_stage,
                args=(stage, channels[index], channels[index + 1], coordinator, errors),
                name=f"stage-{index}-{stage.name}",
                daemon=True,
            )
            threads.append(thread)

        for thread in threads:
            thread.start()

        final_channel = channels[-1]
        outputs: list[Any] = []
        consumer_error: BaseException | None = None
        limited = False
        try:
            while max_outputs is None or len(outputs) < max_outputs:
                item = final_channel.get()
                if item is _END:
                    break
                if consumer is not None:
                    consumer(item)
                outputs.append(item)
            limited = max_outputs is not None and len(outputs) == max_outputs
        except StopConsumption as error:
            consumer_error = error
        except _Aborted:
            pass
        except BaseException as error:
            consumer_error = error
            coordinator.abort(error)

        final_channel.close_reader()
        for thread in threads:
            thread.join(timeout=5)
        alive = [thread.name for thread in threads if thread.is_alive()]
        if alive:
            error = RuntimeError(f"pipeline workers failed to stop: {', '.join(alive)}")
            coordinator.abort(error)
            for thread in threads:
                thread.join(timeout=1)

        fatal = errors[0] if errors else coordinator.error
        if fatal is not None and not isinstance(fatal, StopConsumption):
            raise fatal
        return RunResult(
            outputs=tuple(outputs),
            consumed=consumed,
            peak_buffers=tuple(channel.peak for channel in channels),
            stopped=coordinator.halted or consumer_error is not None or limited,
        )

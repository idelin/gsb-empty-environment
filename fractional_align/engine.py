"""Streaming multi-channel integer/fractional delay alignment engine.

All channels share one output time base. Each channel owns a piecewise-linear
delay trajectory (see schedule.py); the engine evaluates the input at
fractional positions with fixture-provided FIR coefficients, tracks the common
valid interval, marks zero-padded and missing-data-contaminated outputs, and
supports checkpoints with commit/retract semantics.

Retained per-channel history is bounded by
    uncommitted_span + ceil(max_delay) + kernel_length
samples, independent of the total stream length.
"""
from __future__ import annotations

import math
from collections import deque
from dataclasses import dataclass

from .missing import intervals_contain, normalize_intervals
from .schedule import DelaySchedule, ScheduleSnapshot

ZERO_PAD = 1      # output outside the valid input window; input read as zero
CONTAMINATED = 2  # output whose FIR window overlaps missing input data

__all__ = ["Aligner", "OutputBlock", "Checkpoint", "ZERO_PAD", "CONTAMINATED"]


@dataclass
class OutputBlock:
    """One block of aligned output on the common time base."""

    start: int
    data: dict[str, list[float]]
    flags: dict[str, list[int]]

    @property
    def length(self) -> int:
        if not self.data:
            return 0
        return len(next(iter(self.data.values())))


@dataclass
class _ChannelState:
    base: int          # absolute input index of history[0]
    history: deque     # retained input window (the FIR filter history)
    consumed: int      # inputs moved from pending into history so far
    pending: deque     # fed but not yet required inputs (caller-side backlog)
    finished: bool     # no more input will arrive
    missing: list      # normalized missing-input intervals
    first_valid: object   # first valid output position (int or None)
    last_valid: object    # last valid output position (int or None)
    contaminated: list # checkpointed contamination mask (output positions)


def _copy_state(state: _ChannelState) -> _ChannelState:
    return _ChannelState(
        base=state.base,
        history=deque(state.history),
        consumed=state.consumed,
        pending=deque(state.pending),
        finished=state.finished,
        missing=list(state.missing),
        first_valid=state.first_valid,
        last_valid=state.last_valid,
        contaminated=list(state.contaminated),
    )


class _Channel:
    __slots__ = ("schedule", "state")

    def __init__(self, schedule: DelaySchedule, state: _ChannelState):
        self.schedule = schedule
        self.state = state


@dataclass
class Checkpoint:
    """Restorable snapshot: filter history, contamination mask, delay version."""

    cursor: int
    committed: int
    states: dict[str, _ChannelState]
    schedules: dict[str, ScheduleSnapshot]


class Aligner:
    """Aligns multiple channels onto a common output time base."""

    def __init__(self, *, sample_rate, coefficients, kernel_length, max_delay):
        if kernel_length <= 0 or kernel_length % 2:
            raise ValueError("kernel_length must be a positive even integer")
        if max_delay < 0:
            raise ValueError("max_delay must be non-negative")
        self.sample_rate = float(sample_rate)
        self._coefficients = coefficients
        self._kernel_length = int(kernel_length)
        self._half = self._kernel_length // 2
        self._max_delay = float(max_delay)
        self._channels: dict[str, _Channel] = {}
        self._cursor = 0
        self._committed = 0
        self._checkpoints: list[Checkpoint] = []
        self._done = False

    # ------------------------------------------------------------------ setup
    def add_channel(self, name: str, *, initial_delay: float = 0.0) -> None:
        if name in self._channels:
            raise ValueError(f"duplicate channel {name!r}")
        schedule = DelaySchedule(initial_delay, self._max_delay)
        state = _ChannelState(0, deque(), 0, deque(), False, [], None, None, [])
        self._channels[name] = _Channel(schedule, state)

    def schedule(self, name: str) -> DelaySchedule:
        return self._channels[name].schedule

    def declare_delay(self, name: str, position: int, delay: float) -> None:
        """Declare a delay knot at an output position (sample-position driven)."""
        if position <= self._cursor:
            raise ValueError(
                "delay changes must start after the current output position"
            )
        schedule = self._channels[name].schedule
        schedule.freeze_at(self._cursor)  # keep continuity at the emit boundary
        schedule.declare(position, delay)

    def retract_delay_after(self, name: str, position: int) -> None:
        """Retract uncommitted delay knots after ``position`` (>= cursor)."""
        if position < self._cursor:
            raise ValueError(
                "cannot retract at or before the current output position; "
                "restore a checkpoint instead"
            )
        schedule = self._channels[name].schedule
        schedule.freeze_at(self._cursor)
        schedule.retract_after(position)

    def declare_missing(self, name: str, intervals) -> None:
        """Declare missing-input intervals [start, stop) before streaming."""
        if self._cursor > 0:
            raise ValueError("declare missing data before streaming starts")
        self._channels[name].state.missing = normalize_intervals(intervals)

    # ------------------------------------------------------------------ input
    def feed(self, name: str, samples) -> None:
        state = self._channels[name].state
        if state.finished:
            raise ValueError(f"channel {name!r} already finished")
        state.pending.extend(float(s) for s in samples)

    def finish(self, name: str) -> None:
        self._channels[name].state.finished = True

    def is_finished(self, name: str) -> bool:
        return self._channels[name].state.finished

    # -------------------------------------------------------------- properties
    @property
    def cursor(self) -> int:
        return self._cursor

    @property
    def committed(self) -> int:
        return self._committed

    @property
    def done(self) -> bool:
        return self._done

    # ------------------------------------------------------------- processing
    def process(self, block_size: int) -> OutputBlock | None:
        """Emit the next block on the common time base, or None if starved."""
        if block_size <= 0:
            raise ValueError("block_size must be positive")
        if self._done:
            return None
        if not self._channels:
            raise ValueError("no channels registered")
        start = self._cursor
        limit = block_size
        for channel in self._channels.values():
            state = channel.state
            if state.finished:
                continue
            total = state.consumed + len(state.pending)
            n = start
            end = start + limit
            while n < end and self._needed_end(channel, n) < total:
                n += 1
            limit = min(limit, n - start)
        if limit == 0:
            return None
        data = {name: [] for name in self._channels}
        flags = {name: [] for name in self._channels}
        for n in range(start, start + limit):
            for name, channel in self._channels.items():
                self._ensure_history(channel, n)
                value, flag = self._compute_sample(channel, n)
                data[name].append(value)
                flags[name].append(flag)
                state = channel.state
                if not flag & ZERO_PAD:
                    if state.first_valid is None:
                        state.first_valid = n
                    state.last_valid = n
                if flag & CONTAMINATED:
                    state.contaminated.append(n)
        self._cursor = start + limit
        self._prune()
        return OutputBlock(start, data, flags)

    def flush(self, block_size: int) -> list[OutputBlock]:
        """Drain the tail: finish all channels and emit every remaining output."""
        for channel in self._channels.values():
            channel.state.finished = True
        target = self._flush_target()
        blocks = []
        while self._cursor < target:
            block = self.process(min(block_size, target - self._cursor))
            if block is None:  # unreachable once every channel is finished
                break
            blocks.append(block)
        self._done = True
        return blocks

    def commit(self) -> None:
        """Finalize output up to the cursor; frees history and locks delays."""
        self._committed = self._cursor
        for channel in self._channels.values():
            channel.schedule.commit_up_to(self._cursor)
        self._checkpoints.clear()
        self._prune()

    # ------------------------------------------------------------ checkpointing
    def checkpoint(self) -> Checkpoint:
        """Snapshot filter history, contamination mask and delay versions."""
        cp = Checkpoint(
            cursor=self._cursor,
            committed=self._committed,
            states={name: _copy_state(ch.state) for name, ch in self._channels.items()},
            schedules={name: ch.schedule.snapshot()
                       for name, ch in self._channels.items()},
        )
        self._checkpoints.append(cp)
        return cp

    def restore(self, cp: Checkpoint) -> None:
        """Roll back to a checkpoint, retracting the uncommitted region."""
        if cp.cursor < self._committed:
            raise ValueError("checkpoint predates the committed position")
        self._cursor = cp.cursor
        self._committed = cp.committed
        for name, channel in self._channels.items():
            channel.state = _copy_state(cp.states[name])
            channel.schedule.restore(cp.schedules[name])
        self._checkpoints = [c for c in self._checkpoints if c.cursor <= cp.cursor]
        self._done = False

    # ------------------------------------------------------------ introspection
    def common_valid_interval(self):
        """Intersection of per-channel valid output intervals, or None."""
        firsts, lasts = [], []
        for channel in self._channels.values():
            state = channel.state
            if state.first_valid is None or state.last_valid is None:
                return None
            firsts.append(state.first_valid)
            lasts.append(state.last_valid)
        lo, hi = max(firsts), min(lasts)
        return (lo, hi) if lo <= hi else None

    def contamination_mask(self, name: str) -> list[int]:
        """Output positions flagged CONTAMINATED so far (checkpointed mask)."""
        return list(self._channels[name].state.contaminated)

    def retained_history(self, name: str) -> int:
        """Currently retained per-channel history length in samples."""
        return len(self._channels[name].state.history)

    def memory_bound(self, uncommitted_span: int) -> int:
        """Retained-history bound: span + ceil(max delay) + kernel length."""
        return int(uncommitted_span) + math.ceil(self._max_delay) + self._kernel_length

    # ----------------------------------------------------------------- internals
    def _needed_end(self, channel: _Channel, n: int) -> int:
        """Highest input index touched when emitting output n."""
        p = n - channel.schedule.delay_at(n)
        return math.floor(p) + self._half

    def _window_start(self, channel: _Channel, n: int) -> int:
        p = n - channel.schedule.delay_at(n)
        return math.floor(p) - self._half + 1

    def _ensure_history(self, channel: _Channel, n: int) -> None:
        state = channel.state
        need = self._needed_end(channel, n)
        while state.consumed <= need and state.pending:
            state.history.append(state.pending.popleft())
            state.consumed += 1

    def _compute_sample(self, channel: _Channel, n: int) -> tuple[float, int]:
        state = channel.state
        delay = channel.schedule.delay_at(n)
        p = n - delay
        m = math.floor(p)
        taps = self._coefficients(p - m)
        half = self._half
        total = state.consumed + len(state.pending)
        acc = 0.0
        valid = True
        contaminated = False
        for k in range(self._kernel_length):
            t = m - half + 1 + k
            if t < 0 or (state.finished and t >= total):
                valid = False
                continue
            if intervals_contain(state.missing, t):
                contaminated = True
                continue
            acc += taps[k] * state.history[t - state.base]
        flag = 0 if valid else ZERO_PAD
        if contaminated:
            flag |= CONTAMINATED
        return acc, flag

    def _prune(self) -> None:
        cursors = [self._committed] + [cp.cursor for cp in self._checkpoints]
        n_min = min(cursors)
        for channel in self._channels.values():
            state = channel.state
            p = n_min - channel.schedule.delay_at(n_min)
            keep_from = math.floor(p) - self._half + 1
            drop = min(max(0, keep_from - state.base), len(state.history))
            if drop:
                for _ in range(drop):
                    state.history.popleft()
                state.base += drop

    def _flush_target(self) -> int:
        """First output position at which no channel can be valid any more."""
        target = self._cursor
        for channel in self._channels.values():
            state = channel.state
            total = state.consumed + len(state.pending)
            lo = self._cursor
            hi = lo + total + self._half + math.ceil(self._max_delay) + 2
            while lo < hi:
                mid = (lo + hi) // 2
                if self._window_start(channel, mid) > total - 1:
                    hi = mid
                else:
                    lo = mid + 1
            target = max(target, lo)
        return target

"""Streaming multi-channel aligner with integer + fractional per-channel delays.

Model
-----
* One common output time base: output index n aligns to input index n.
* Each channel c applies a declared, possibly time-varying delay d_c(n):

      y_c[n] = sum_k h(frac)[k] * x_c[n - I - k]
      I    = floor(d_c(n) - group)
      frac = d_c(n) - group - I
      group = (K - 1) / 2

  so y_c[n] ~= x_c(n - d_c(n)).  The fractional-delay FIR coefficients h are
  supplied by the fixture; this module never designs its own filter.

* Delay changes are driven by absolute sample positions and ramp linearly
  over a declared transition length, so the read position n - d_c(n) never
  jumps, never repeats and never skips undeclared samples.

* Output is emitted in order; emitted output is *confirmed*.  A scheduled
  change whose position is still ahead of the emit frontier is tentative and
  can be retracted.

* Outputs that need unavailable input (before the stream start) are
  zero-filled and flagged; outputs whose convolution window touches input
  marked as missing are flagged contaminated.  Missing samples contribute
  zero to the convolution.

* Memory per channel is bounded by ceil(max_delay - group) + K samples of
  history; the bound is derived from the declared max delay and kernel
  length only.

* checkpoint()/load_checkpoint() snapshot and restore the filter history,
  the missing-data contamination mask and the delay version together, so a
  restored aligner continues bit-identically.
"""

import math
import pickle
from dataclasses import dataclass, field

import fixture


# ---------------------------------------------------------------------------
# delay trajectory


@dataclass
class DelayChange:
    """A smooth, sample-position-driven delay change."""
    position: int        # output sample index where the ramp starts
    target: float        # delay after the transition, in samples
    transition: int      # ramp length in samples (>= 1, smooth by contract)
    start_value: float   # delay value at `position` (from earlier changes)
    version: int         # delay-version stamp of the schedule operation


def evaluate_delay(base_delay: float, changes: list[DelayChange], n: int) -> float:
    """Delay trajectory at output index n (continuous, piecewise linear)."""
    d = base_delay
    for ch in changes:
        if n < ch.position:
            break
        if n >= ch.position + ch.transition:
            d = ch.target
        else:
            t = (n - ch.position) / ch.transition
            d = ch.start_value + (ch.target - ch.start_value) * t
    return d


def build_changes(base_delay: float, specs: list[tuple[int, float, int]]) -> list[DelayChange]:
    """Turn (position, target, transition) specs into DelayChange objects."""
    changes: list[DelayChange] = []
    for position, target, transition in sorted(specs):
        start_value = evaluate_delay(base_delay, changes, position)
        changes.append(DelayChange(position, target, transition, start_value, 0))
    return changes


# ---------------------------------------------------------------------------
# shared per-sample convolution (identical arithmetic in aligner + reference)


def convolve_at(get_sample, n: int, delay: float, kernel, group: float, klen: int):
    """One output sample.  get_sample(idx) -> (value, available, missing)."""
    i_int = math.floor(delay - group)
    frac = delay - group - i_int
    h = kernel(frac)
    acc = 0.0
    zero_filled = False
    contaminated = False
    for k in range(klen):
        value, available, missing = get_sample(n - i_int - k)
        if not available:
            zero_filled = True
        else:
            acc += h[k] * value
            if missing:
                contaminated = True
    return acc, zero_filled, contaminated


# ---------------------------------------------------------------------------
# output blocks


@dataclass
class OutputBlock:
    """Contiguous emitted output on the common time base."""
    start: int
    samples: list[list[float]]       # [channel][sample]
    zero_filled: list[list[bool]]    # touched unavailable input
    contaminated: list[list[bool]]   # touched missing input

    def __len__(self) -> int:
        return len(self.samples[0]) if self.samples else 0


# ---------------------------------------------------------------------------
# channel state


@dataclass
class _ChannelState:
    base_delay: float
    history: list[float] = field(default_factory=list)   # filter history
    missing: list[bool] = field(default_factory=list)    # contamination mask
    history_start: int = 0                               # absolute index of history[0]
    received: int = 0                                    # total input samples seen
    changes: list[DelayChange] = field(default_factory=list)


# ---------------------------------------------------------------------------
# the aligner


class MultiChannelAligner:
    def __init__(self, num_channels, base_delays,
                 max_delay=fixture.MAX_DELAY,
                 kernel=fixture.fractional_kernel,
                 kernel_length=fixture.KERNEL_LENGTH):
        if len(base_delays) != num_channels:
            raise ValueError("need one base delay per channel")
        self.num_channels = num_channels
        self.kernel = kernel
        self.kernel_length = kernel_length
        self.group = (kernel_length - 1) / 2
        self.max_delay = float(max_delay)
        for d in base_delays:
            self._check_delay(d)
        # memory bound derived from max delay and kernel length only
        self._i_max = math.ceil(self.max_delay - self.group)
        self.history_bound = self._i_max + kernel_length

        self._channels = [_ChannelState(base_delay=float(d)) for d in base_delays]
        self.emit_frontier = 0          # next output index to emit
        self.delay_version = 0          # bumped on every schedule/retract
        self.finished = False
        self.max_history_observed = 0

        # running statistics over emitted output
        self._valid_first = None
        self._valid_last = None
        self._valid_count = 0
        self.zero_filled_samples = 0    # per-channel-sample count
        self.contaminated_samples = 0

    # -- delay contract ----------------------------------------------------

    def _check_delay(self, delay: float) -> None:
        if not self.group <= delay <= self.max_delay:
            raise ValueError(
                f"delay {delay} outside declared range "
                f"[{self.group}, {self.max_delay}]"
            )

    def delay_at(self, channel: int, n: int) -> float:
        st = self._channels[channel]
        return evaluate_delay(st.base_delay, st.changes, n)

    def schedule_change(self, channel: int, position: int,
                        target: float, transition: int) -> int:
        """Schedule a smooth delay change; returns its delay-version stamp."""
        if self.finished:
            raise RuntimeError("stream finished")
        if position < self.emit_frontier:
            raise RuntimeError(
                f"position {position} already confirmed (frontier "
                f"{self.emit_frontier}); cannot change the past"
            )
        self._check_delay(target)
        if transition < 1:
            raise ValueError("transition must be >= 1 sample; "
                             "step changes would skip/repeat undeclared samples")
        start_value = self.delay_at(channel, position)
        if abs(target - start_value) > transition:
            raise ValueError(
                f"ramp slope {abs(target - start_value) / transition:.3f} > 1 "
                "sample/sample would repeat undeclared samples"
            )
        st = self._channels[channel]
        self.delay_version += 1
        change = DelayChange(position, float(target), int(transition),
                             start_value, self.delay_version)
        st.changes.append(change)
        st.changes.sort(key=lambda c: c.position)
        self._rebuild(channel)
        return change.version

    def retract(self, version: int) -> None:
        """Withdraw a tentative (not yet confirmed) scheduled change."""
        for ci, st in enumerate(self._channels):
            for ch in st.changes:
                if ch.version == version:
                    if ch.position < self.emit_frontier:
                        raise RuntimeError(
                            f"change at {ch.position} already confirmed "
                            f"(frontier {self.emit_frontier}); cannot retract"
                        )
                    st.changes.remove(ch)
                    self.delay_version += 1
                    self._rebuild(ci)
                    return
        raise KeyError(f"no scheduled change with version {version}")

    def _rebuild(self, channel: int) -> None:
        st = self._channels[channel]
        prefix: list[DelayChange] = []
        for ch in st.changes:
            ch.start_value = evaluate_delay(st.base_delay, prefix, ch.position)
            prefix.append(ch)

    # -- streaming -----------------------------------------------------------

    def process(self, chunks: list[list[float]],
                missing: list[list[bool]] | None = None) -> OutputBlock:
        """Append one input chunk per channel (common input index) and emit."""
        if self.finished:
            raise RuntimeError("stream finished")
        if len(chunks) != self.num_channels:
            raise ValueError("need one chunk per channel")
        n_new = len(chunks[0])
        if any(len(c) != n_new for c in chunks):
            raise ValueError("all channels must advance on the common time base")
        if missing is None:
            missing = [[False] * n_new for _ in range(self.num_channels)]
        if any(len(m) != n_new for m in missing):
            raise ValueError("missing mask must match chunk lengths")
        for st, chunk, miss in zip(self._channels, chunks, missing):
            st.history.extend(chunk)
            st.missing.extend(miss)
            st.received += n_new
        return self._drain()

    def finish(self) -> OutputBlock:
        """Flush the tail: emit everything computable, then close the stream."""
        block = self._drain()
        self.finished = True
        return block

    def _emittable(self, n: int) -> bool:
        for st in self._channels:
            d = evaluate_delay(st.base_delay, st.changes, n)
            i_int = math.floor(d - self.group)
            if n - i_int > st.received - 1:
                return False
        return True

    def _accessor(self, st: _ChannelState):
        def get(idx: int):
            if idx < 0:
                return 0.0, False, False
            off = idx - st.history_start
            if off < 0 or off >= len(st.history):
                raise AssertionError("history pruned too far")
            miss = st.missing[off]
            return (0.0 if miss else st.history[off]), True, miss
        return get

    def _drain(self) -> OutputBlock:
        start = self.emit_frontier
        cols = [[] for _ in range(self.num_channels)]
        zf = [[] for _ in range(self.num_channels)]
        ct = [[] for _ in range(self.num_channels)]
        n = self.emit_frontier
        while self._emittable(n):
            all_valid = True
            for ci, st in enumerate(self._channels):
                d = evaluate_delay(st.base_delay, st.changes, n)
                value, z, c = convolve_at(self._accessor(st), n, d,
                                          self.kernel, self.group,
                                          self.kernel_length)
                cols[ci].append(value)
                zf[ci].append(z)
                ct[ci].append(c)
                self.zero_filled_samples += z
                self.contaminated_samples += c
                all_valid = all_valid and not z and not c
            if all_valid:
                if self._valid_first is None:
                    self._valid_first = n
                self._valid_last = n
                self._valid_count += 1
            n += 1
        self.emit_frontier = n
        self._prune()
        return OutputBlock(start, cols, zf, ct)

    def _prune(self) -> None:
        keep_from = self.emit_frontier - self._i_max - (self.kernel_length - 1)
        for st in self._channels:
            drop = keep_from - st.history_start
            if drop > 0:
                del st.history[:drop]
                del st.missing[:drop]
                st.history_start += drop
            self.max_history_observed = max(self.max_history_observed,
                                            len(st.history))

    # -- introspection ---------------------------------------------------------

    def history_lengths(self) -> list[int]:
        return [len(st.history) for st in self._channels]

    def common_valid_interval(self):
        """(first, last) output index valid on every channel, or None."""
        if self._valid_first is None:
            return None
        return (self._valid_first, self._valid_last)

    # -- checkpointing ---------------------------------------------------------

    def checkpoint(self) -> bytes:
        """Snapshot filter history + contamination mask + delay version."""
        state = {
            "delay_version": self.delay_version,
            "emit_frontier": self.emit_frontier,
            "finished": self.finished,
            "max_history_observed": self.max_history_observed,
            "stats": (self._valid_first, self._valid_last, self._valid_count,
                      self.zero_filled_samples, self.contaminated_samples),
            "channels": [
                {
                    "base_delay": st.base_delay,
                    "history": list(st.history),          # filter history
                    "missing_mask": list(st.missing),     # contamination mask
                    "history_start": st.history_start,
                    "received": st.received,
                    "changes": [
                        (c.position, c.target, c.transition,
                         c.start_value, c.version)
                        for c in st.changes
                    ],
                }
                for st in self._channels
            ],
        }
        return pickle.dumps(state, protocol=4)

    def load_checkpoint(self, payload: bytes) -> None:
        state = pickle.loads(payload)
        if len(state["channels"]) != self.num_channels:
            raise ValueError("checkpoint channel count mismatch")
        self.delay_version = state["delay_version"]
        self.emit_frontier = state["emit_frontier"]
        self.finished = state["finished"]
        self.max_history_observed = state["max_history_observed"]
        (self._valid_first, self._valid_last, self._valid_count,
         self.zero_filled_samples,
         self.contaminated_samples) = state["stats"]
        for st, saved in zip(self._channels, state["channels"]):
            st.base_delay = saved["base_delay"]
            st.history = list(saved["history"])
            st.missing = list(saved["missing_mask"])
            st.history_start = saved["history_start"]
            st.received = saved["received"]
            st.changes = [DelayChange(*c) for c in saved["changes"]]

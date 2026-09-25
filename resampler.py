"""Rational-ratio polyphase FIR resampler with runtime rate switching.

Design notes
------------
* All time arithmetic uses ``fractions.Fraction`` (exact rationals), so
  channels that share one clock never drift, no matter how the input is
  chunked or how fast each channel is processed.
* Output sample times live on the source-sample time base.  A ratio switch
  is defined by a *source sample position*; it only re-phases the output
  grid, it never discards the filter tail: outputs after the switch still
  convolve the (new) kernel over pre-switch input samples.
* The FIR kernel is supplied by the caller (:class:`Kernel`).  Memory per
  channel is bounded by the widest kernel span in input samples plus a
  small margin, and the per-output cost is O(kernel_span) MACs.
* Gaps are marked per channel by pushing ``None`` instead of a sample.
  A gap is never replaced by a previous value: it contributes exactly 0.0
  to the convolution and every output whose kernel window overlaps a gap
  is flagged ``GAP_CONTAMINATED`` so the polluted range is explicit.

Declared latency
----------------
``Kernel.latency == (len(taps) - 1) / (2 * interp_rate)`` input samples
for a centered kernel.  An output at source-time ``t`` is emitted only
after every input sample up to ``floor(t + latency)`` has been pushed.

Declared numerical tolerance
----------------------------
``NUMERICAL_TOLERANCE = 1e-12`` (relative) against a direct reference
convolution evaluated at the same output times.  Chunked vs. one-shot
processing and checkpoint/restore are bit-identical.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from fractions import Fraction
from typing import Deque, Dict, List, Optional, Sequence, Tuple

NUMERICAL_TOLERANCE = 1e-12

VALID = 0
GAP_CONTAMINATED = 0b01
EDGE = 0b10


def _floor(x: Fraction) -> int:
    return x.numerator // x.denominator


def _ceil(x: Fraction) -> int:
    return -((-x.numerator) // x.denominator)


def _align(start: Fraction, last: Optional[Fraction], interp: int) -> Fraction:
    """First point of the 1/interp grid that is >= start and > last."""
    g = Fraction(_ceil(start * interp), interp)
    if last is not None:
        while g <= last:
            g += Fraction(1, interp)
    return g


class Kernel:
    """Caller-provided FIR interpolation kernel.

    ``taps`` are sampled at ``interp_rate`` times the input rate, i.e. tap
    ``i`` sits at source-time offset ``(i - center) / interp_rate``.
    Off-grid positions are evaluated by linear interpolation between taps;
    on-grid positions (the steady-state polyphase case) are exact.
    """

    __slots__ = ("taps", "interp_rate")

    def __init__(self, taps: Sequence[float], interp_rate: int):
        taps = [float(t) for t in taps]
        if not taps:
            raise ValueError("kernel must have at least one tap")
        if int(interp_rate) < 1:
            raise ValueError("interp_rate must be a positive integer")
        self.taps = taps
        self.interp_rate = int(interp_rate)

    @property
    def center(self) -> Fraction:
        """Center tap index in kernel-tap units."""
        return Fraction(len(self.taps) - 1, 2)

    @property
    def latency(self) -> Fraction:
        """Group delay in input-sample units (centered kernel)."""
        return self.center / self.interp_rate

    @property
    def span(self) -> Fraction:
        """Total kernel width in input-sample units."""
        return Fraction(len(self.taps) - 1, self.interp_rate)

    def value_at(self, index: Fraction) -> float:
        """Kernel value at a possibly fractional tap index."""
        last = len(self.taps) - 1
        if index < 0 or index > last:
            return 0.0
        i = _floor(index)
        frac = index - i
        if frac == 0:
            return self.taps[i]
        if i >= last:
            return self.taps[last]
        f = float(frac)
        return self.taps[i] + (self.taps[i + 1] - self.taps[i]) * f


@dataclass(frozen=True)
class Regime:
    """One ratio segment: valid for source positions >= ``start``."""

    start: Fraction
    interp: int
    decim: int
    kernel: Kernel

    @property
    def step(self) -> Fraction:
        """Output period in input-sample units."""
        return Fraction(self.decim, self.interp)


@dataclass(frozen=True)
class Output:
    index: int
    time: Fraction       # source-sample units, exact
    timestamp: float     # seconds: clock.origin + time / input_rate
    value: float
    flags: int


def _check_ratio(interp: int, decim: int, kernel: Kernel) -> None:
    if int(interp) < 1 or int(decim) < 1:
        raise ValueError("interp and decim must be positive integers")
    if kernel.interp_rate != int(interp):
        raise ValueError("kernel.interp_rate must equal interp")


class ResamplerClock:
    """Shared clock: owns the ratio schedule and the time origin.

    All channels created via :meth:`add_channel` derive their output time
    grid from this schedule with exact rational arithmetic, so channels
    processed at different speeds produce identical timestamps.
    """

    def __init__(self, input_rate: float, interp: int, decim: int,
                 kernel: Kernel, origin: float = 0.0):
        _check_ratio(interp, decim, kernel)
        self.input_rate = float(input_rate)
        self.origin = float(origin)
        self._regimes: List[Regime] = [Regime(Fraction(0), int(interp),
                                              int(decim), kernel)]

    @property
    def regimes(self) -> Tuple[Regime, ...]:
        return tuple(self._regimes)

    def set_ratio(self, interp: int, decim: int, *, at, kernel: Kernel) -> None:
        """Schedule a ratio switch at source sample position ``at``.

        ``at`` may be any rational position; outputs with time >= ``at``
        use the new ratio.  The first new-grid output is the smallest
        1/interp grid point >= ``at`` (and strictly after the previous
        output), so output times never repeat and never go backward.
        """
        _check_ratio(interp, decim, kernel)
        at = Fraction(at)
        if at <= self._regimes[-1].start:
            raise ValueError("switch point must be after the previous one")
        self._regimes.append(Regime(at, int(interp), int(decim), kernel))

    def add_channel(self) -> "Channel":
        return Channel(self)

    def snapshot(self) -> Dict:
        return {"input_rate": self.input_rate, "origin": self.origin,
                "regimes": list(self._regimes)}

    def restore(self, state: Dict) -> None:
        self.input_rate = float(state["input_rate"])
        self.origin = float(state["origin"])
        self._regimes = list(state["regimes"])


class Channel:
    """One resampling channel bound to a shared :class:`ResamplerClock`."""

    def __init__(self, clock: ResamplerClock):
        self._clock = clock
        self._hist: Deque[Tuple[float, bool]] = deque()
        self._hist_start = 0          # absolute index of _hist[0]
        self._received = 0            # total samples pushed
        self._regime_idx = 0
        self._next_time: Optional[Fraction] = None
        self._last_time: Optional[Fraction] = None
        self._out_index = 0

    # ------------------------------------------------------------------ I/O

    def push(self, samples) -> None:
        """Push input samples; ``None`` marks a gap (missing) sample."""
        for s in samples:
            if s is None:
                self._hist.append((0.0, True))
            else:
                self._hist.append((float(s), False))
            self._received += 1

    def poll(self) -> List[Output]:
        """Emit every output whose kernel window is fully covered."""
        out: List[Output] = []
        regimes = self._clock._regimes
        while True:
            if self._next_time is None:
                reg0 = regimes[self._regime_idx]
                self._next_time = _align(reg0.start, None, reg0.interp)
            # Adopt any scheduled regime whose start has been reached.
            while (self._regime_idx + 1 < len(regimes)
                   and self._next_time >= regimes[self._regime_idx + 1].start):
                self._regime_idx += 1
                reg = regimes[self._regime_idx]
                if self._last_time is not None and self._last_time >= reg.start:
                    raise RuntimeError(
                        "ratio switch point lies in this channel's past")
                self._next_time = _align(reg.start, self._last_time,
                                         reg.interp)
            reg = regimes[self._regime_idx]
            t = self._next_time
            kernel = reg.kernel
            right = kernel.latency                       # center / interp
            if _floor(t + right) > self._received - 1:
                break                                    # need more input
            left = kernel.span - right
            j_lo = _ceil(t - left)
            j_hi = _floor(t + right)
            taps = kernel.taps
            last_tap = len(taps) - 1
            interp = kernel.interp_rate
            # Split the kernel tap index (t - j) * interp + center into an
            # integer part plus one fractional offset per output, so the
            # inner loop runs on plain integers: exact and O(1) per tap.
            ti = _floor(t)
            cprime = (t - ti) * interp + kernel.center
            ci = _floor(cprime)
            cf = cprime - ci
            cff = float(cf)
            m = (ti - j_lo) * interp + ci
            acc = 0.0
            flags = VALID
            hist = self._hist
            base = self._hist_start
            hist_len = len(hist)
            for j in range(j_lo, j_hi + 1):
                if cf == 0:
                    h = taps[m] if 0 <= m <= last_tap else 0.0
                elif 0 <= m < last_tap:
                    h = taps[m] + (taps[m + 1] - taps[m]) * cff
                elif m == last_tap:
                    h = taps[last_tap]
                else:
                    h = 0.0
                m -= interp
                if j < 0:
                    flags |= EDGE                        # zero padding
                    continue
                k = j - base
                if k < 0 or k >= hist_len:
                    raise RuntimeError("internal: history underrun")
                v, gap = hist[k]
                if gap:
                    flags |= GAP_CONTAMINATED
                acc += v * h
            out.append(Output(
                self._out_index, t,
                self._clock.origin + float(t) / self._clock.input_rate,
                acc, flags))
            self._last_time = t
            self._next_time = t + reg.step
            self._out_index += 1
        self._prune()
        return out

    def process(self, samples) -> List[Output]:
        self.push(samples)
        return self.poll()

    # ------------------------------------------------------------- state

    @property
    def next_output_time(self) -> Optional[Fraction]:
        return self._next_time

    @property
    def output_index(self) -> int:
        return self._out_index

    @property
    def buffered_samples(self) -> int:
        return len(self._hist)

    @property
    def latency(self) -> Fraction:
        return self._clock._regimes[self._regime_idx].kernel.latency

    def _prune(self) -> None:
        if self._next_time is None:
            return
        regimes = self._clock._regimes
        max_span = max(r.kernel.span for r in regimes[self._regime_idx:])
        keep_from = _floor(self._next_time - max_span)
        drop = min(keep_from - self._hist_start, len(self._hist))
        for _ in range(max(0, drop)):
            self._hist.popleft()
            self._hist_start += 1

    # ---------------------------------------------------- checkpointing

    def snapshot(self) -> Dict:
        """Full recoverable state, including the fractional phase."""
        return {
            "hist": list(self._hist),
            "hist_start": self._hist_start,
            "received": self._received,
            "regime_idx": self._regime_idx,
            "next_time": self._next_time,
            "last_time": self._last_time,
            "out_index": self._out_index,
        }

    def restore(self, state: Dict) -> None:
        if state["regime_idx"] >= len(self._clock._regimes):
            raise ValueError("snapshot references an unknown regime")
        self._hist = deque(state["hist"])
        self._hist_start = state["hist_start"]
        self._received = state["received"]
        self._regime_idx = state["regime_idx"]
        self._next_time = state["next_time"]
        self._last_time = state["last_time"]
        self._out_index = state["out_index"]


def contaminated_ranges(outputs: Sequence[Output]
                        ) -> List[Tuple[Fraction, Fraction]]:
    """Merge consecutive GAP_CONTAMINATED outputs into (start, end) ranges."""
    ranges: List[Tuple[Fraction, Fraction]] = []
    start: Optional[Fraction] = None
    prev: Optional[Fraction] = None
    for o in outputs:
        if o.flags & GAP_CONTAMINATED:
            if start is None:
                start = o.time
            prev = o.time
        elif start is not None:
            ranges.append((start, prev))
            start = None
    if start is not None:
        ranges.append((start, prev))
    return ranges

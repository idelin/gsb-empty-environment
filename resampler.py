"""Rational-ratio polyphase FIR resampler.

Pure Python 3.11 (standard library only). No audio devices: sample arrays
and sample rates are supplied by local fixtures.

Design declarations
-------------------
* Latency: for a kernel of ``N`` taps and upsampling factor ``L`` the group
  delay is ``(N - 1) / (2L)`` source samples.  Output timestamps are
  delay-compensated by this amount, so early outputs carry negative
  (pre-ring) timestamps relative to the time origin.  ``latency_samples``
  and ``latency_seconds`` expose the declared value.
* Numeric tolerance: outputs whose fractional position lands exactly on the
  polyphase grid (the steady-state case for a fixed rational ratio) match a
  direct convolution reference to float rounding (~1e-12).  Off-grid
  positions (after a ratio switch at a non-integer phase) use linear
  interpolation between adjacent polyphase branches; tests use TOLERANCE.
* Bounded work: per output sample at most ``2*ceil((N+1)/L)`` multiply-adds;
  the delay line is trimmed to ``O(N/L + M/L)`` samples.
* Timestamps are exact ``fractions.Fraction`` values derived from a shared
  clock, so channels processed at different rates cannot drift apart.
* Gaps: missing source samples are zero-filled in the convolution (never
  replaced with a held previous value) and every output whose convolution
  window overlaps a gap is flagged FLAG_CONTAMINATED; the merged ranges are
  available as ``contaminated_ranges``.
* Filter tails are never dropped: ``flush()`` zero-pads the input end and
  emits outputs until the (delay-compensated) output time reaches the end of
  the input.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from fractions import Fraction

FLAG_NORMAL = 0
FLAG_CONTAMINATED = 1
FLAG_TAIL = 2

#: Declared numeric tolerance used by the test-suite and demo.
TOLERANCE = 1e-9


def _floor(fr: Fraction) -> int:
    return fr.numerator // fr.denominator


def _fr_dump(fr: Fraction):
    return [fr.numerator, fr.denominator]


@dataclass
class Output:
    """One resampled output sample."""

    index: int
    time: Fraction            # absolute, delay-compensated timestamp (seconds)
    value: float
    flags: int
    source_position: Fraction  # position on the source-sample grid

    @property
    def contaminated(self) -> bool:
        return bool(self.flags & FLAG_CONTAMINATED)

    @property
    def tail(self) -> bool:
        return bool(self.flags & FLAG_TAIL)


class SharedClock:
    """Time origin + input sample rate shared by any number of channels.

    All channel timestamps are computed as exact Fractions from this single
    origin, so channels resampled at different rates or fed with different
    chunk sizes stay on one drift-free time base.
    """

    def __init__(self, fs_in, origin=Fraction(0)):
        self.fs_in = Fraction(fs_in)
        self.origin = Fraction(origin)

    def channel(self, kernel, up, down) -> "Resampler":
        return Resampler(kernel, up, down, clock=self)


class Resampler:
    """Polyphase FIR resampler for one channel.

    Parameters
    ----------
    kernel : sequence of float
        Caller-provided FIR kernel (in the upsampled domain).
    up, down : int
        Rational ratio L/M (output rate = input rate * L / M).
    clock : SharedClock, optional
        Shared time base.  If omitted, ``fs_in`` (and optionally ``origin``)
        must be given and a private clock is created.
    """

    VERSION = 1

    def __init__(self, kernel, up, down, clock=None, fs_in=None,
                 origin=Fraction(0)):
        if clock is None:
            if fs_in is None:
                raise ValueError("provide either clock or fs_in")
            clock = SharedClock(fs_in, origin)
        kernel = [float(c) for c in kernel]
        if not kernel:
            raise ValueError("kernel must not be empty")
        up, down = int(up), int(down)
        if up < 1 or down < 1:
            raise ValueError("up/down must be >= 1")
        self.clock = clock
        self.h = kernel
        self.L = up
        self.M = down
        # --- streaming state (all captured by checkpoint()) ---
        self.t_next = Fraction(0)      # next output position (source samples)
        self.buf = deque()             # recent input samples (gaps zeroed)
        self.buf_start = 0             # absolute source index of buf[0]
        self.received = 0              # total source samples ingested
        self.gaps = []                 # merged [start, end) source ranges
        self.pending = []              # scheduled ratio switches
        self.out_index = 0
        self.last_pos = None
        self.last_time = None
        self.ended = False             # flush() has begun
        self.done = False              # tail fully emitted
        self._end_position = None      # t at which flush stops
        self.contaminated_ranges = []  # [[first_time, last_time], ...]
        self._contam_open = False

    # ------------------------------------------------------------------ #
    # declared latency                                                    #
    # ------------------------------------------------------------------ #
    def _gd(self) -> Fraction:
        """Group delay in source samples for the active kernel/ratio."""
        return Fraction(len(self.h) - 1, 2 * self.L)

    @property
    def latency_samples(self) -> Fraction:
        return self._gd()

    @property
    def latency_seconds(self) -> Fraction:
        return self._gd() / self.clock.fs_in

    @property
    def step(self) -> Fraction:
        """Source-sample advance per output sample."""
        return Fraction(self.M, self.L)

    # ------------------------------------------------------------------ #
    # runtime ratio switching                                             #
    # ------------------------------------------------------------------ #
    def set_ratio(self, up, down, at_source_index, kernel=None):
        """Schedule a ratio switch at a source-sample position.

        The switch point is defined on the source grid and may be
        fractional (non-integer phase).  Outputs before the point keep the
        old ratio; the first output at/after it is anchored exactly on the
        switch point and continues with the new ratio.  Output timestamps
        are guaranteed to stay strictly increasing.
        """
        entry = {
            "at": Fraction(at_source_index),
            "up": int(up),
            "down": int(down),
            "kernel": [float(c) for c in kernel] if kernel is not None else None,
        }
        if entry["up"] < 1 or entry["down"] < 1:
            raise ValueError("up/down must be >= 1")
        self.pending.append(entry)
        self.pending.sort(key=lambda e: e["at"])

    def _apply_pending(self):
        while self.pending and self.t_next >= self.pending[0]["at"]:
            sw = self.pending.pop(0)
            if sw["kernel"] is not None:
                self.h = sw["kernel"]
            self.L = sw["up"]
            self.M = sw["down"]
            step = self.step
            if self.last_pos is None or sw["at"] > self.last_pos:
                self.t_next = sw["at"]
            while self.last_pos is not None and self.t_next <= self.last_pos:
                self.t_next += step
            # group delay may change with L/kernel: never repeat or go back
            while (self.last_time is not None
                   and self._time(self.t_next) <= self.last_time):
                self.t_next += step

    # ------------------------------------------------------------------ #
    # processing                                                          #
    # ------------------------------------------------------------------ #
    def process(self, samples, gaps=()):
        """Consume a chunk of contiguous source samples.

        ``gaps`` is an iterable of ``(start, length)`` in absolute source
        indices inside this chunk.  Gap samples are zero-filled (never held
        at the previous value) and outputs whose convolution window touches
        them are flagged contaminated.  Empty chunks are legal no-ops.
        Returns the list of Output objects that became ready.
        """
        if self.ended:
            raise RuntimeError("cannot process() after flush()")
        samples = [float(s) for s in samples]
        start = self.received
        n = len(samples)
        if n:
            for g0, glen in gaps:
                ge = int(g0) + int(glen)
                if g0 < start or ge > start + n:
                    raise ValueError("gap outside chunk range")
                self.gaps.append([int(g0), ge])
                for i in range(int(g0), ge):
                    samples[i - start] = 0.0
            self.gaps.sort()
            self.buf.extend(samples)
            self.received += n
        outs = self._emit(flush_mode=False, limit=None)
        self._trim()
        return outs

    def flush(self, limit=None):
        """Emit the filter tail (zero-padded), at most ``limit`` outputs.

        May be called repeatedly; returns [] and sets ``done`` once the
        delay-compensated output time has reached the end of the input.
        """
        if not self.ended:
            self.ended = True
            self._end_position = Fraction(self.received) + self._gd()
        return self._emit(flush_mode=True, limit=limit)

    # ------------------------------------------------------------------ #
    # checkpoint / restore                                                #
    # ------------------------------------------------------------------ #
    def checkpoint(self) -> dict:
        """Snapshot the full resumable state (JSON/pickle friendly)."""
        return {
            "version": self.VERSION,
            "fs_in": _fr_dump(self.clock.fs_in),
            "origin": _fr_dump(self.clock.origin),
            "h": list(self.h),
            "L": self.L,
            "M": self.M,
            "t_next": _fr_dump(self.t_next),
            "buf": list(self.buf),
            "buf_start": self.buf_start,
            "received": self.received,
            "gaps": [list(g) for g in self.gaps],
            "pending": [
                {"at": _fr_dump(p["at"]), "up": p["up"], "down": p["down"],
                 "kernel": list(p["kernel"]) if p["kernel"] else None}
                for p in self.pending
            ],
            "out_index": self.out_index,
            "last_pos": _fr_dump(self.last_pos) if self.last_pos is not None else None,
            "last_time": _fr_dump(self.last_time) if self.last_time is not None else None,
            "ended": self.ended,
            "done": self.done,
            "end_position": (_fr_dump(self._end_position)
                             if self._end_position is not None else None),
            "contaminated_ranges": [[_fr_dump(a), _fr_dump(b)]
                                    for a, b in self.contaminated_ranges],
            "contam_open": self._contam_open,
        }

    @classmethod
    def restore(cls, state: dict) -> "Resampler":
        """Rebuild a resampler from checkpoint(); output continues bit-exact."""
        if state["version"] != cls.VERSION:
            raise ValueError("unsupported checkpoint version")
        clock = SharedClock(Fraction(*state["fs_in"]),
                            Fraction(*state["origin"]))
        r = cls(state["h"], state["L"], state["M"], clock=clock)
        r.t_next = Fraction(*state["t_next"])
        r.buf = deque(state["buf"])
        r.buf_start = state["buf_start"]
        r.received = state["received"]
        r.gaps = [list(g) for g in state["gaps"]]
        r.pending = [
            {"at": Fraction(*p["at"]), "up": p["up"], "down": p["down"],
             "kernel": list(p["kernel"]) if p["kernel"] else None}
            for p in state["pending"]
        ]
        r.out_index = state["out_index"]
        r.last_pos = (Fraction(*state["last_pos"])
                      if state["last_pos"] is not None else None)
        r.last_time = (Fraction(*state["last_time"])
                       if state["last_time"] is not None else None)
        r.ended = state["ended"]
        r.done = state["done"]
        r._end_position = (Fraction(*state["end_position"])
                           if state["end_position"] is not None else None)
        r.contaminated_ranges = [[Fraction(*a), Fraction(*b)]
                                 for a, b in state["contaminated_ranges"]]
        r._contam_open = state["contam_open"]
        return r

    # ------------------------------------------------------------------ #
    # internals                                                           #
    # ------------------------------------------------------------------ #
    def _time(self, t: Fraction) -> Fraction:
        return self.clock.origin + (t - self._gd()) / self.clock.fs_in

    def _emit(self, flush_mode, limit):
        outs = []
        while limit is None or len(outs) < limit:
            self._apply_pending()
            if flush_mode:
                if self.t_next > self._end_position:
                    self.done = True
                    break
            elif _floor(self.t_next) > self.received - 1:
                break
            outs.append(self._emit_one(flush_mode))
        return outs

    def _emit_one(self, flush_mode) -> Output:
        t = self.t_next
        k = _floor(t)
        value = self._convolve(t, k)
        time = self._time(t)
        flags = FLAG_NORMAL
        if self._is_contaminated(t, k):
            flags |= FLAG_CONTAMINATED
            if self._contam_open:
                self.contaminated_ranges[-1][1] = time
            else:
                self.contaminated_ranges.append([time, time])
                self._contam_open = True
        else:
            self._contam_open = False
        if flush_mode and k >= self.received:
            flags |= FLAG_TAIL
        out = Output(self.out_index, time, value, flags, t)
        self.out_index += 1
        self.last_pos = t
        self.last_time = time
        self.t_next = t + self.step
        return out

    def _convolve(self, t: Fraction, k: int) -> float:
        """y(t) = sum_j h_c(fL + jL) * x[k-j] with linear branch interp."""
        h, L, N = self.h, self.L, len(self.h)
        fl = (t - k) * L                      # fractional phase in [0, L)
        p = fl.numerator // fl.denominator
        alpha = float(fl - p)
        y = 0.0
        j = 0
        while True:
            i0 = p + j * L
            i1 = i0 + 1
            if i0 >= N and i1 >= N:
                break
            x = self._sample(k - j)
            if x:
                c0 = h[i0] if i0 < N else 0.0
                c1 = h[i1] if i1 < N else 0.0
                y += ((1.0 - alpha) * c0 + alpha * c1) * x
            j += 1
        return y

    def _sample(self, idx: int) -> float:
        if idx < self.buf_start or idx >= self.received:
            return 0.0
        return self.buf[idx - self.buf_start]

    def _is_contaminated(self, t: Fraction, k: int) -> bool:
        if not self.gaps:
            return False
        lo = _floor(t - Fraction(len(self.h), self.L)) + 1
        hi = k
        for gs, ge in self.gaps:
            if gs <= hi and ge > lo:
                return True
        return False

    def _trim(self):
        margin = self.M // self.L + 2
        lo = _floor(self.t_next - Fraction(len(self.h), self.L)) - margin
        while self.buf and self.buf_start < lo:
            self.buf.popleft()
            self.buf_start += 1

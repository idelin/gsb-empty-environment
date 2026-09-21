"""Correctable resampler: irregular samples -> fixed time grid.

Modes
-----
- ``step``   : right-continuous staircase. value(t) = value of the latest
               sample with time <= t.
- ``linear`` : linear interpolation between the two bracketing samples.
               No extrapolation outside the sampled range.

Hard rules
----------
- Explicit breakpoints and gaps larger than ``max_gap`` may never be
  interpolated (or stepped) across; affected grid points become *unknown*.
- Output times are exact grid positions (``Fraction``); values are quantized
  to 6 decimal places with ROUND_HALF_UP (fixed rounding rule).
- Late samples and retractions produce *revisions* (diff events per grid
  point). Same-time samples resolve by arrival order: the latest arrival
  wins; retracting it reveals the previous one.
- Each mutation call is one transaction: it bumps ``version`` once and
  returns the revision events it caused.
"""

from __future__ import annotations

import bisect
import itertools
import math
from dataclasses import dataclass
from decimal import Decimal, ROUND_HALF_UP, localcontext
from fractions import Fraction
from typing import Hashable, Optional

QUANTUM = Decimal("0.000001")
MODE_STEP = "step"
MODE_LINEAR = "linear"

REASON_OK = "ok"
REASON_NO_SAMPLE = "no-sample"
REASON_NO_BRACKET = "no-bracket"
REASON_GAP = "gap-exceeds-max"
REASON_BREAKPOINT = "breakpoint"


def to_time(t) -> Fraction:
    """Exact conversion of ints, floats, strings, Decimals to Fraction."""
    if isinstance(t, Fraction):
        return t
    if isinstance(t, bool):
        raise TypeError("invalid time")
    if isinstance(t, int):
        return Fraction(t)
    if isinstance(t, Decimal):
        return Fraction(t)
    if isinstance(t, float):
        return Fraction(str(t))
    if isinstance(t, str):
        return Fraction(t)
    raise TypeError(f"unsupported time type: {type(t)!r}")


def to_value(v) -> Decimal:
    if isinstance(v, Decimal):
        return v
    if isinstance(v, bool):
        raise TypeError("invalid value")
    return Decimal(str(v))


def quantize(fr: Fraction) -> Decimal:
    """Fixed output precision: 6 decimal places, ROUND_HALF_UP."""
    with localcontext() as ctx:
        ctx.prec = 60
        dec = Decimal(fr.numerator) / Decimal(fr.denominator)
    return dec.quantize(QUANTUM, rounding=ROUND_HALF_UP)


@dataclass(frozen=True)
class Sample:
    seq: int
    id: Hashable
    time: Fraction
    value: Decimal
    retracted: bool = False


@dataclass(frozen=True)
class GridPoint:
    index: int
    t: Fraction
    value: Optional[Decimal]
    valid: bool
    mode: str
    sample_ids: tuple
    reason: str

    def to_dict(self) -> dict:
        return {
            "index": self.index,
            "t": str(self.t),
            "t_float": float(self.t),
            "value": str(self.value) if self.value is not None else None,
            "valid": self.valid,
            "mode": self.mode,
            "sample_ids": list(self.sample_ids),
            "reason": self.reason,
        }


@dataclass(frozen=True)
class RevisionEvent:
    version: int
    index: int
    t: Fraction
    old: Optional[GridPoint]
    new: Optional[GridPoint]


class Resampler:
    def __init__(self, dt, mode: str, max_gap, t0=0):
        self.dt = to_time(dt)
        if self.dt <= 0:
            raise ValueError("dt must be positive")
        if mode not in (MODE_STEP, MODE_LINEAR):
            raise ValueError("mode must be 'step' or 'linear'")
        self.mode = mode
        self.max_gap = to_time(max_gap)
        if self.max_gap <= 0:
            raise ValueError("max_gap must be positive and finite")
        self.t0 = to_time(t0)

        self._seq = itertools.count(1)
        self._auto_id = itertools.count(1)
        self._by_time: dict[Fraction, list[Sample]] = {}
        self._times: list[Fraction] = []          # sorted active sample times
        self._by_id: dict[Hashable, Sample] = {}
        self._breaks: list[Fraction] = []         # sorted breakpoint times

        self._version = 0
        self._events: list[RevisionEvent] = []
        self._emitted: dict[int, GridPoint] = {}
        self._closed_until: Optional[Fraction] = None

    # ------------------------------------------------------------- mutations
    @property
    def version(self) -> int:
        return self._version

    def add_sample(self, t, value, sample_id=None) -> tuple[Hashable, list[RevisionEvent]]:
        t = to_time(t)
        self._check_open(t)
        if sample_id is None:
            sample_id = f"s{next(self._auto_id)}"
        if sample_id in self._by_id:
            raise ValueError(f"duplicate sample id: {sample_id!r}")
        sample = Sample(next(self._seq), sample_id, t, to_value(value))
        bucket = self._by_time.setdefault(t, [])
        if not any(not s.retracted for s in bucket):
            bisect.insort(self._times, t)
        bucket.append(sample)
        self._by_id[sample_id] = sample
        return sample_id, self._mutate(t)

    def retract(self, sample_id) -> list[RevisionEvent]:
        sample = self._by_id.get(sample_id)
        if sample is None:
            raise ValueError(f"unknown or released sample id: {sample_id!r}")
        if sample.retracted:
            raise ValueError(f"sample already retracted: {sample_id!r}")
        self._check_open(sample.time)
        fresh = Sample(sample.seq, sample.id, sample.time, sample.value, True)
        bucket = self._by_time[sample.time]
        bucket[bucket.index(sample)] = fresh
        self._by_id[sample_id] = fresh
        if not any(not s.retracted for s in bucket):
            del self._times[bisect.bisect_left(self._times, sample.time)]
        return self._mutate(sample.time)

    def add_breakpoint(self, t) -> list[RevisionEvent]:
        t = to_time(t)
        self._check_open(t)
        i = bisect.bisect_left(self._breaks, t)
        if i < len(self._breaks) and self._breaks[i] == t:
            raise ValueError(f"duplicate breakpoint at {t}")
        self._breaks.insert(i, t)
        return self._mutate(t)

    def remove_breakpoint(self, t) -> list[RevisionEvent]:
        t = to_time(t)
        self._check_open(t)
        i = bisect.bisect_left(self._breaks, t)
        if not (i < len(self._breaks) and self._breaks[i] == t):
            raise ValueError(f"no breakpoint at {t}")
        del self._breaks[i]
        return self._mutate(t)

    # ------------------------------------------------------------- queries
    def point_at(self, index: int) -> GridPoint:
        return self._eval_k(index)

    def grid_range(self, k0: int, k1: int) -> list[GridPoint]:
        """Inclusive grid-index range [k0, k1]; also baselines the consumer
        view so later revisions diff against what was actually read."""
        points = [self._eval_k(k) for k in range(k0, k1 + 1)]
        for p in points:
            self._emitted[p.index] = p
        return points

    def revisions_since(self, version: int) -> list[RevisionEvent]:
        return [e for e in self._events if e.version > version]

    # ------------------------------------------------------- lifecycle mgmt
    def close_until(self, t) -> dict:
        """Declare the interval (-inf, t] closed: the caller promises no more
        mutations at times <= t. Releases history that can no longer change
        any future grid point:

        - active samples older than the anchor (latest active sample at or
          before t) are dropped; the anchor is kept as the left edge for
          future step/linear evaluation;
        - retracted / superseded samples at or before t are dropped (their
          revisions were already emitted);
        - breakpoints at or before the anchor are dropped (they cannot
          block any derivation anchored at or after the anchor);
        - revision events for grid times <= t are dropped.

        After closing, mutations at times <= t are rejected.
        """
        t = to_time(t)
        i = bisect.bisect_right(self._times, t)
        anchor = self._times[i - 1] if i else None

        released_samples = 0
        if anchor is not None:
            for s in [x for x in self._times if x < anchor]:
                for sample in self._by_time.pop(s):
                    self._by_id.pop(sample.id, None)
                    released_samples += 1
            self._times = [x for x in self._times if x >= anchor]

            bucket = self._by_time[anchor]
            keep = [s for s in bucket if not s.retracted][-1:]
            for s in bucket:
                if s not in keep:
                    self._by_id.pop(s.id, None)
                    released_samples += 1
            self._by_time[anchor] = keep

        cut = anchor if anchor is not None else t
        before = len(self._breaks)
        self._breaks = [b for b in self._breaks if b > cut]
        released_breaks = before - len(self._breaks)

        before = len(self._events)
        self._events = [e for e in self._events if e.t > t]
        released_events = before - len(self._events)
        self._emitted = {k: p for k, p in self._emitted.items() if p.t > t}

        self._closed_until = t if self._closed_until is None else max(self._closed_until, t)
        return {
            "anchor": anchor,
            "released_samples": released_samples,
            "released_breakpoints": released_breaks,
            "released_events": released_events,
        }

    # ------------------------------------------------------------ internals
    def _check_open(self, t: Fraction) -> None:
        if self._closed_until is not None and t <= self._closed_until:
            raise ValueError(f"interval closed until {self._closed_until}; "
                             f"mutation at {t} rejected")

    def _active_at(self, t: Fraction) -> Optional[Sample]:
        for s in reversed(self._by_time.get(t, [])):
            if not s.retracted:
                return s
        return None

    def _has_break(self, lo: Fraction, hi: Fraction, hi_open=False) -> bool:
        i = bisect.bisect_right(self._breaks, lo)
        if i >= len(self._breaks):
            return False
        b = self._breaks[i]
        return b < hi if hi_open else b <= hi

    def _eval_k(self, k: int) -> GridPoint:
        t = self.t0 + k * self.dt
        if self.mode == MODE_STEP:
            return self._eval_step(k, t)
        return self._eval_linear(k, t)

    def _invalid(self, k, t, reason) -> GridPoint:
        return GridPoint(k, t, None, False, self.mode, (), reason)

    def _eval_step(self, k, t) -> GridPoint:
        i = bisect.bisect_right(self._times, t)
        if i == 0:
            return self._invalid(k, t, REASON_NO_SAMPLE)
        s = self._times[i - 1]
        if t - s > self.max_gap:
            return self._invalid(k, t, REASON_GAP)
        if self._has_break(s, t):  # breakpoint in (s, t]
            return self._invalid(k, t, REASON_BREAKPOINT)
        sample = self._active_at(s)
        return GridPoint(k, t, quantize(Fraction(sample.value)), True,
                         self.mode, (sample.id,), REASON_OK)

    def _eval_linear(self, k, t) -> GridPoint:
        i = bisect.bisect_left(self._times, t)
        if i < len(self._times) and self._times[i] == t:
            sample = self._active_at(t)
            return GridPoint(k, t, quantize(Fraction(sample.value)), True,
                             self.mode, (sample.id,), REASON_OK)
        s2 = self._times[i] if i < len(self._times) else None
        s1 = self._times[i - 1] if i > 0 else None
        if s1 is None or s2 is None:
            return self._invalid(k, t, REASON_NO_BRACKET)
        if s2 - s1 > self.max_gap:
            return self._invalid(k, t, REASON_GAP)
        if self._has_break(s1, s2, hi_open=True):  # breakpoint strictly inside
            return self._invalid(k, t, REASON_BREAKPOINT)
        a, b = self._active_at(s1), self._active_at(s2)
        fr = (Fraction(a.value)
              + (Fraction(b.value) - Fraction(a.value)) * (t - s1) / (s2 - s1))
        return GridPoint(k, t, quantize(fr), True, self.mode,
                         (a.id, b.id), REASON_OK)

    def _affected_interval(self, t: Fraction) -> tuple[Fraction, Fraction]:
        """Conservative closed interval of grid times whose derivation may
        change due to a mutation at time t (evaluated post-mutation)."""
        i = bisect.bisect_left(self._times, t)
        prev_lt = self._times[i - 1] if i else None
        j = bisect.bisect_right(self._times, t)
        prev_le = self._times[j - 1] if j else None
        nxt_gt = self._times[j] if j < len(self._times) else None
        lo = prev_lt if prev_lt is not None else t
        hi = t + self.max_gap
        if nxt_gt is not None:
            hi = max(hi, nxt_gt)
        if prev_le is not None:
            hi = max(hi, prev_le + self.max_gap)
        return lo, hi

    def _mutate(self, t: Fraction) -> list[RevisionEvent]:
        lo, hi = self._affected_interval(t)
        k_lo = math.ceil((lo - self.t0) / self.dt)
        k_hi = math.floor((hi - self.t0) / self.dt)
        self._version += 1
        events = []
        for k in range(k_lo, k_hi + 1):
            p = self._eval_k(k)
            old = self._emitted.get(k)
            if old != p:
                ev = RevisionEvent(self._version, k, p.t, old, p)
                self._events.append(ev)
                events.append(ev)
                self._emitted[k] = p
        return events


def compute_reference(samples, breakpoints, *, dt, mode, max_gap, t0, k0, k1,
                      retract_ids=()) -> list[GridPoint]:
    """Whole-segment reference: ingest everything in the given arrival order,
    apply retractions, then answer the grid range. Ground truth for chunked
    ingestion comparisons."""
    r = Resampler(dt=dt, mode=mode, max_gap=max_gap, t0=t0)
    for b in breakpoints:
        r.add_breakpoint(b)
    ids = []
    for s in samples:
        sid, _ = r.add_sample(*s)
        ids.append(sid)
    for sid in retract_ids:
        r.retract(sid)
    return r.grid_range(k0, k1)

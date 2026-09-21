"""Revisable token-bucket replay engine.

Replay semantics
----------------
* Requests are ordered by event time; ties break by ``seq``.
* Each admitted request consumes exactly one token.
* The bucket refills continuously at the ``rate`` of the active config
  segment and never exceeds that segment's ``capacity``.
* Config segments are half-open intervals ``[start, next_start)``.  A
  request exactly on a boundary is served under the *new* segment.  When a
  segment with a smaller capacity begins, the bucket is clamped down at the
  boundary, before any request sitting exactly on the boundary.
* The bucket starts full at the capacity active at the first request.
* All arithmetic uses ``fractions.Fraction``.  Floats are rejected at the
  API boundary so no binary floating-point drift can accumulate.

Revision semantics
------------------
The engine keeps one ``Decision`` per request.  Each decision carries a
``version``; the engine version is bumped once per accepted edit and only
decisions whose outcome actually changed receive the new version.  Every
edit returns a ``RevisionReport`` with:

* ``recomputed_range`` -- inclusive index range the engine actually
  re-evaluated (actual work done);
* ``changed_range`` -- minimal contiguous index range covering every
  decision whose outcome changed (the range downstream consumers must
  re-apply);
* ``changed_request_ids`` -- the requests inside that range.

Decisions outside ``changed_range`` are guaranteed untouched: same values,
same version, i.e. stable results keep their identity.

Early-stop rule
---------------
The only state carried between events is the bucket level (an exact
``Fraction``) and the previous event time.  Recomputation may therefore
stop at event ``i`` as soon as (1) the recomputed record at ``i`` is
value-identical to the stored one -- the level has reconverged -- and
(2) no input differences remain beyond ``i``: the remaining events are
unchanged and the new config is known to equal the old config on
``[t_i, inf)``.  For bounded config edits (2) becomes true past the last
differing segment; for edits touching the final open-ended segment it is
never true, so recomputation runs to the end of the log.
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Sequence, Tuple, Union

__all__ = [
    "Segment",
    "Config",
    "Request",
    "Decision",
    "RevisionReport",
    "ReplayEngine",
    "TOKEN_COST",
    "REASON_ALLOWED",
    "REASON_REJECTED",
]

Number = Union[int, str, Fraction]

TOKEN_COST = Fraction(1)
REASON_ALLOWED = "token_available"
REASON_REJECTED = "insufficient_tokens"

# Sentinel for "the config never becomes identical to the old one again",
# used when an edit touches the final open-ended segment.
_NEVER = float("inf")


def _frac(value: Number, name: str) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError(f"{name} must be an exact number, got bool")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(
        f"{name} must be int/Fraction/str, got {type(value).__name__}; "
        "floats are rejected to keep arithmetic exact"
    )


@dataclass(frozen=True)
class Segment:
    """One config segment: from ``start`` (inclusive) until the next start."""

    start: Fraction
    capacity: Fraction
    rate: Fraction  # tokens per unit time

    def __post_init__(self) -> None:
        object.__setattr__(self, "start", _frac(self.start, "start"))
        object.__setattr__(self, "capacity", _frac(self.capacity, "capacity"))
        object.__setattr__(self, "rate", _frac(self.rate, "rate"))
        if self.capacity < 0:
            raise ValueError("capacity must be >= 0")
        if self.rate < 0:
            raise ValueError("rate must be >= 0")


class Config:
    """Piecewise-constant token-bucket configuration."""

    def __init__(self, segments: Sequence[Union[Segment, tuple]]) -> None:
        segs = [s if isinstance(s, Segment) else Segment(*s) for s in segments]
        if not segs:
            raise ValueError("at least one config segment is required")
        segs.sort(key=lambda s: s.start)
        for prev, nxt in zip(segs, segs[1:]):
            if prev.start == nxt.start:
                raise ValueError(f"duplicate segment start {nxt.start}")
        self._segments = tuple(segs)
        self._starts = [s.start for s in segs]

    @property
    def segments(self) -> Tuple[Segment, ...]:
        return self._segments

    @property
    def first_start(self) -> Fraction:
        return self._starts[0]

    def segment_at(self, t: Fraction) -> Segment:
        idx = bisect.bisect_right(self._starts, t) - 1
        if idx < 0:
            raise ValueError(f"time {t} is before first segment start {self._starts[0]}")
        return self._segments[idx]

    def refill(self, level: Fraction, t0: Fraction, t1: Fraction) -> Fraction:
        """Exact bucket level at ``t1`` starting from ``level`` at ``t0``.

        Walks every segment boundary in ``(t0, t1]``: growth is capped at the
        active segment's capacity, and entering a segment with a smaller
        capacity clamps the level down at the boundary.
        """
        if t0 > t1:
            raise ValueError("t0 must be <= t1")
        cur = self.segment_at(t0)
        t = t0
        for seg in self._segments:
            boundary = seg.start
            if boundary <= t0:
                continue
            if boundary > t1:
                break
            level = min(level + cur.rate * (boundary - t), cur.capacity)
            if level > seg.capacity:  # capacity shrink clamps at the boundary
                level = seg.capacity
            cur = seg
            t = boundary
        return min(level + cur.rate * (t1 - t), cur.capacity)

    def diff(self, other: "Config") -> Tuple[Optional[Fraction], Union[Fraction, float]]:
        """Compare two configs as functions of time.

        Returns ``(diff_from, stable_from)``: the configs differ somewhere on
        ``[diff_from, stable_from)`` and are identical on
        ``[stable_from, inf)``.  ``stable_from`` is ``inf`` when the final
        open-ended segment differs.  ``diff_from`` is ``None`` when the
        configs are equivalent everywhere both are defined.
        """
        floor = max(self.first_start, other.first_start)
        points = [floor] + sorted(
            {s.start for s in self._segments + other._segments if s.start > floor}
        )
        differing = [
            p
            for p in points
            if (self.segment_at(p).capacity, self.segment_at(p).rate)
            != (other.segment_at(p).capacity, other.segment_at(p).rate)
        ]
        if not differing:
            return None, _NEVER
        last = differing[-1]
        later = [p for p in points if p > last]
        stable_from: Union[Fraction, float] = later[0] if later else _NEVER
        return differing[0], stable_from


@dataclass(frozen=True, order=True)
class Request:
    time: Fraction
    seq: int
    request_id: str


@dataclass(frozen=True)
class Decision:
    """Replay outcome for one request."""

    request_id: str
    time: Fraction
    seq: int
    allowed: bool
    reason: str
    bucket_before: Fraction
    bucket_after: Fraction
    version: int


@dataclass(frozen=True)
class RevisionReport:
    """What an edit actually did to the replayed log."""

    revision: int
    kind: str  # "insert" | "config"
    detail: str
    recomputed_range: Optional[Tuple[int, int]]  # inclusive indices, work done
    changed_range: Optional[Tuple[int, int]]  # minimal covering range of changes
    changed_request_ids: Tuple[str, ...]


class ReplayEngine:
    """Incremental, revisable replay of a request log against a config."""

    def __init__(
        self,
        segments: Sequence[Union[Segment, tuple]],
        requests: Sequence[Tuple[Number, int, str]] = (),
    ) -> None:
        self._config = Config(segments)
        self._events: List[Request] = []
        self._decisions: List[Optional[Decision]] = []
        self._ids: set = set()
        self._version = 1
        self.revisions: List[RevisionReport] = []
        for time, seq, request_id in requests:
            self._events.append(self._make_request(time, seq, request_id, self._ids))
            self._ids.add(str(request_id))
        self._events.sort()
        for prev, nxt in zip(self._events, self._events[1:]):
            if (prev.time, prev.seq) == (nxt.time, nxt.seq):
                raise ValueError(f"duplicate (time, seq)=({nxt.time}, {nxt.seq})")
        if self._events and self._events[0].time < self._config.first_start:
            raise ValueError("config does not cover the earliest request")
        self._decisions = [None] * len(self._events)
        self._recompute_from(0, config_stable_from=None)

    @staticmethod
    def _make_request(time: Number, seq: int, request_id: str, ids: set) -> Request:
        req = Request(_frac(time, "time"), int(seq), str(request_id))
        if req.request_id in ids:
            raise ValueError(f"duplicate request_id {req.request_id!r}")
        return req

    @property
    def version(self) -> int:
        return self._version

    @property
    def events(self) -> List[Request]:
        return list(self._events)

    @property
    def decisions(self) -> List[Decision]:
        return list(self._decisions)  # type: ignore[return-value]

    def insert_request(self, time: Number, seq: int, request_id: str) -> RevisionReport:
        """Backfill a (possibly historical) request and revise the log."""
        req = self._make_request(time, seq, request_id, self._ids)
        if req.time < self._config.first_start:
            raise ValueError("request time is before the first config segment")
        keys = [(e.time, e.seq) for e in self._events]
        pos = bisect.bisect_left(keys, (req.time, req.seq))
        if pos < len(self._events) and keys[pos] == (req.time, req.seq):
            raise ValueError(f"duplicate (time, seq)=({req.time}, {req.seq})")
        self._events.insert(pos, req)
        self._decisions.insert(pos, None)
        self._ids.add(req.request_id)
        self._version += 1
        # Pure insert: events after pos are unchanged and the config is
        # untouched, so recomputation may stop at the first identical record.
        recomputed, changed, ids = self._recompute_from(pos, config_stable_from=None)
        report = RevisionReport(
            self._version, "insert",
            f"inserted {req.request_id!r} at index {pos}",
            recomputed, changed, ids,
        )
        self.revisions.append(report)
        return report

    def update_config(self, segments: Sequence[Union[Segment, tuple]]) -> RevisionReport:
        """Replace the config, possibly retroactively, and revise the log."""
        new = Config(segments)
        if self._events and new.first_start > self._events[0].time:
            raise ValueError("new config does not cover the earliest request")
        diff_from, stable_from = self._config.diff(new)
        if diff_from is None:
            report = RevisionReport(
                self._version, "config", "no effective config change", None, None, ()
            )
            self.revisions.append(report)
            return report
        self._config = new
        self._version += 1
        if not self._events:
            report = RevisionReport(
                self._version, "config", "config changed; log is empty", None, None, ()
            )
            self.revisions.append(report)
            return report
        times = [e.time for e in self._events]
        start = bisect.bisect_left(times, diff_from)
        recomputed, changed, ids = self._recompute_from(
            start, config_stable_from=stable_from
        )
        report = RevisionReport(
            self._version, "config",
            f"config differs from t={diff_from}"
            + ("" if stable_from == _NEVER else f" to t={stable_from}"),
            recomputed, changed, ids,
        )
        self.revisions.append(report)
        return report

    def _compute(self, req: Request, level: Fraction, prev_time: Fraction):
        before = self._config.refill(level, prev_time, req.time)
        if before >= TOKEN_COST:
            return True, REASON_ALLOWED, before, before - TOKEN_COST
        return False, REASON_REJECTED, before, before

    def _recompute_from(
        self,
        start: int,
        config_stable_from: Union[Fraction, float, None],
    ) -> Tuple[Optional[Tuple[int, int]], Optional[Tuple[int, int]], Tuple[str, ...]]:
        """Recompute decisions from ``start`` forward, stopping early when safe.

        ``config_stable_from`` is the time from which the current config is
        known to equal the config that produced the stored decisions
        (``None`` = no constraint, ``inf`` = never).  A stored record that
        recomputes value-identically ends the scan as soon as stopping is
        allowed, because all later records would recompute identically too.
        """
        n = len(self._events)
        if start >= n:
            return None, None, ()
        if start == 0:
            level = self._config.segment_at(self._events[0].time).capacity
            prev_time = self._events[0].time
        else:
            prev = self._decisions[start - 1]
            assert prev is not None
            level = prev.bucket_after
            prev_time = self._events[start - 1].time

        recomputed_lo = recomputed_hi = None
        changed_lo = changed_hi = None
        changed_ids: List[str] = []
        i = start
        while i < n:
            req = self._events[i]
            allowed, reason, before, after = self._compute(req, level, prev_time)
            level = after
            prev_time = req.time
            recomputed_lo = i if recomputed_lo is None else recomputed_lo
            recomputed_hi = i
            old = self._decisions[i]
            if (
                old is not None
                and old.request_id == req.request_id
                and old.time == req.time
                and old.seq == req.seq
                and old.allowed == allowed
                and old.reason == reason
                and old.bucket_before == before
                and old.bucket_after == after
            ):
                if config_stable_from is None or req.time >= config_stable_from:
                    break  # reconverged: everything after i is identical too
                i += 1  # identical values, but config still differs ahead
                continue
            self._decisions[i] = Decision(
                req.request_id, req.time, req.seq,
                allowed, reason, before, after, self._version,
            )
            changed_lo = i if changed_lo is None else changed_lo
            changed_hi = i
            changed_ids.append(req.request_id)
            i += 1

        recomputed = (
            (recomputed_lo, recomputed_hi) if recomputed_lo is not None else None
        )
        changed = (changed_lo, changed_hi) if changed_lo is not None else None
        return recomputed, changed, tuple(changed_ids)

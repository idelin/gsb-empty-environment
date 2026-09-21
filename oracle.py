"""Independent full-replay oracle.

This module deliberately shares no code with ``token_bucket.ReplayEngine``.
It recomputes the entire log from scratch on every call, with its own
segment walk, so tests can cross-check the incremental engine against a
simple, obviously-correct reference after every edit.

Semantics (same contract as the engine):

* requests sorted by ``(time, seq)``;
* one token per admitted request;
* continuous refill at the active segment's rate, capped at its capacity;
* segments are half-open ``[start, next_start)``; entering a segment with a
  smaller capacity clamps the bucket at the boundary;
* the bucket starts full at the capacity active at the first request;
* exact ``Fraction`` arithmetic only.
"""

from __future__ import annotations

from collections import namedtuple
from fractions import Fraction

Outcome = namedtuple(
    "Outcome", ["request_id", "allowed", "reason", "bucket_before", "bucket_after"]
)

_ALLOWED = "token_available"
_REJECTED = "insufficient_tokens"


def _exact(value):
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not an exact number")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"floats are rejected to keep arithmetic exact: {value!r}")


def _segment_at(segments, t):
    """Last segment whose start is <= t (linear scan on purpose)."""
    chosen = None
    for seg in segments:
        if seg[0] <= t:
            chosen = seg
        else:
            break
    if chosen is None:
        raise ValueError(f"time {t} is before first segment start")
    return chosen


def _refill(segments, level, t0, t1):
    """Advance ``level`` from ``t0`` to ``t1`` across segment boundaries."""
    t = t0
    while t < t1:
        _, cap, rate = _segment_at(segments, t)
        upcoming = [s for (s, _, _) in segments if s > t]
        boundary = min(upcoming) if upcoming else None
        if boundary is None or boundary > t1:
            return min(level + rate * (t1 - t), cap)
        level = min(level + rate * (boundary - t), cap)
        next_cap = _segment_at(segments, boundary)[1]
        if level > next_cap:  # capacity shrink clamps at the boundary
            level = next_cap
        t = boundary
    return level


def full_replay(segments, requests):
    """Replay ``requests`` against ``segments`` from scratch.

    ``segments``: iterable of ``(start, capacity, rate)``.
    ``requests``: iterable of ``(time, seq, request_id)``.
    Returns a list of ``Outcome`` in replay order.
    """
    segs = sorted(
        (_exact(s), _exact(c), _exact(r)) for s, c, r in segments
    )
    if not segs:
        raise ValueError("at least one config segment is required")
    for prev, nxt in zip(segs, segs[1:]):
        if prev[0] == nxt[0]:
            raise ValueError(f"duplicate segment start {nxt[0]}")
    reqs = sorted(
        (_exact(t), int(q), str(rid)) for t, q, rid in requests
    )
    if not reqs:
        return []
    if reqs[0][0] < segs[0][0]:
        raise ValueError("config does not cover the earliest request")

    outcomes = []
    level = _segment_at(segs, reqs[0][0])[1]  # bucket starts full
    prev_time = reqs[0][0]
    for time, _seq, request_id in reqs:
        level = _refill(segs, level, prev_time, time)
        before = level
        if before >= 1:
            level = before - 1
            outcomes.append(Outcome(request_id, True, _ALLOWED, before, level))
        else:
            outcomes.append(Outcome(request_id, False, _REJECTED, before, before))
        prev_time = time
    return outcomes

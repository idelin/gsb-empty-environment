"""Interval algebra over composite (tuple) keys in encoded index space.

An Interval is [lo, hi] with per-end inclusivity.  All keys are full-length
tuples of encoded components (sentinels included), so plain tuple comparison
defines the total order.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Interval:
    lo: tuple
    lo_inc: bool
    hi: tuple
    hi_inc: bool

    def is_empty(self):
        if self.lo > self.hi:
            return True
        return self.lo == self.hi and not (self.lo_inc and self.hi_inc)

    def contains(self, key):
        if key < self.lo or (key == self.lo and not self.lo_inc):
            return False
        if key > self.hi or (key == self.hi and not self.hi_inc):
            return False
        return True


def _max_lower(a, b):
    """Tighter (larger) of two lower bounds, each (key, inclusive)."""
    if a[0] > b[0]:
        return a
    if b[0] > a[0]:
        return b
    return (a[0], a[1] and b[1])


def _min_upper(a, b):
    """Tighter (smaller) of two upper bounds, each (key, inclusive)."""
    if a[0] < b[0]:
        return a
    if b[0] < a[0]:
        return b
    return (a[0], a[1] and b[1])


def intersect(a: Interval, b: Interval):
    lo, lo_inc = _max_lower((a.lo, a.lo_inc), (b.lo, b.lo_inc))
    hi, hi_inc = _min_upper((a.hi, a.hi_inc), (b.hi, b.hi_inc))
    return Interval(lo, lo_inc, hi, hi_inc)


def touching(a: Interval, b: Interval):
    """True if a and b overlap or are adjacent with no gap between them
    (a assumed to start no later than b)."""
    if b.lo < a.hi:
        return True
    if b.lo == a.hi and (b.lo_inc or a.hi_inc):
        return True
    return False


def _merge_pair(a: Interval, b: Interval):
    """Merge two touching intervals (a starts no later than b)."""
    if b.hi > a.hi:
        hi, hi_inc = b.hi, b.hi_inc
    elif b.hi == a.hi:
        hi, hi_inc = a.hi, a.hi_inc or b.hi_inc
    else:
        hi, hi_inc = a.hi, a.hi_inc
    return Interval(a.lo, a.lo_inc, hi, hi_inc)


def normalize(intervals):
    """Sort and merge into a minimal list of pairwise disjoint, non-touching
    intervals.  Never drops coverage."""
    ivs = sorted(
        (iv for iv in intervals if not iv.is_empty()),
        key=lambda iv: (iv.lo, not iv.lo_inc),
    )
    out = []
    for iv in ivs:
        if out and touching(out[-1], iv):
            out[-1] = _merge_pair(out[-1], iv)
        else:
            out.append(iv)
    return out


def complement(intervals, lo_key, hi_key):
    """Complement of a normalized interval set within [lo_key, hi_key]."""
    ivs = normalize(intervals)
    out = []
    cur_key, cur_inc = lo_key, True
    for iv in ivs:
        gap = Interval(cur_key, cur_inc, iv.lo, not iv.lo_inc)
        if not gap.is_empty():
            out.append(gap)
        cur_key, cur_inc = iv.hi, not iv.hi_inc
    tail = Interval(cur_key, cur_inc, hi_key, True)
    if not tail.is_empty():
        out.append(tail)
    return out


def hull(intervals):
    """Smallest single interval covering all given intervals."""
    ivs = normalize(intervals)
    if not ivs:
        raise ValueError("hull of empty set")
    return Interval(ivs[0].lo, ivs[0].lo_inc, ivs[-1].hi, ivs[-1].hi_inc)


def _common_prefix_len(a, b):
    n = 0
    for x, y in zip(a, b):
        if x == y:
            n += 1
        else:
            break
    return n


def shrink_to_budget(intervals, budget):
    """Merge adjacent intervals (most similar boundary first) until at most
    `budget` intervals remain.  The result is a superset of the input;
    callers must keep the original predicate as a residual filter."""
    ivs = normalize(intervals)
    while len(ivs) > budget:
        best_i, best_score = 0, -1
        for i in range(len(ivs) - 1):
            score = _common_prefix_len(ivs[i].hi, ivs[i + 1].lo)
            if score > best_score:
                best_i, best_score = i, score
        merged = Interval(ivs[best_i].lo, ivs[best_i].lo_inc,
                          ivs[best_i + 1].hi, ivs[best_i + 1].hi_inc)
        ivs[best_i:best_i + 2] = [merged]
    return ivs

"""Canonical visible decomposition.

Given the live records of one key, compute the unique non-overlapping cover
of the business-time axis where, at every point, the visible record is the
one with the highest priority.  The overlap rule is explicit and total:

* higher ``priority`` wins;
* ties are broken by the *lower* ``record_id`` (earlier recording wins).
"""

from __future__ import annotations

from .model import LiveRecord, from_key, key_to_bound, to_key


def decompose(live: list[LiveRecord]):
    """Return ``[(valid_from, valid_to, LiveRecord), ...]``, non-overlapping."""
    live = list(live)
    if not live:
        return []
    bounds = sorted({from_key(r.valid_from) for r in live}
                    | {to_key(r.valid_to) for r in live})
    out = []
    for lo, hi in zip(bounds, bounds[1:]):
        winner = None
        for rec in live:
            if from_key(rec.valid_from) <= lo and to_key(rec.valid_to) >= hi:
                if (winner is None
                        or (rec.priority, -rec.record_id) > (winner.priority, -winner.record_id)):
                    winner = rec
        if winner is None:
            continue
        if out and out[-1][1] == lo and out[-1][2].record_id == winner.record_id:
            out[-1][1] = hi  # same winner on the adjacent segment: merge
        else:
            out.append([lo, hi, winner])
    return [(key_to_bound(lo), key_to_bound(hi), rec) for lo, hi, rec in out]

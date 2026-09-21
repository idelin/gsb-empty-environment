"""Retention and reclamation of old versions.

Retention rule
--------------
A version must be retained as long as there exists *some* query
(t_valid, t_tx) whose answer is exactly that version.  Because visibility
only grows with t_tx, the hardest observation moment for a version v is
its own birth t_tx = v.tx_from.  Therefore:

* v is **live** iff it wins at least one business instant at its birth
  moment (given the versions visible then);
* v is **reclaimable** (birth-dominated) iff every instant of
  [v.valid_from, v.valid_to) is already covered by a higher-priority
  version visible at v.tx_from.  With the (tx_from, seq) priority this
  means: same tx_from, higher seq.  Later corrections can never make an
  older version retroactively invisible at its own birth moment, so they
  do NOT by themselves make old versions reclaimable -- as-of queries at
  earlier observation times still need them.

Horizon compaction
------------------
If the application guarantees that no future query will use an observation
time earlier than a horizon H (a legal/audit retention decision), all
versions with tx_from <= H may be replaced by a compacted snapshot: the
resolved timeline as of H, re-emitted as synthetic versions with
tx_from = H.  Queries with t_tx >= H return identical evidence; queries
with t_tx < H become unanswerable, which is exactly the precondition.
"""

from __future__ import annotations

from typing import Iterable

from .model import INF, Version
from .reference import reference_timeline


def _intervals_cover(
    lo: float, hi: float, intervals: list[tuple[float, float]]
) -> bool:
    """True iff the union of ``intervals`` covers [lo, hi)."""
    cur = lo
    for a, b in sorted(intervals):
        if b <= cur:
            continue
        if a > cur:
            return False
        cur = b
        if cur >= hi:
            return True
    return cur >= hi


def birth_dominated(versions: Iterable[Version]) -> list[Version]:
    """Versions that can never be the answer to any query."""
    by_obj: dict[str, list[Version]] = {}
    for v in versions:
        by_obj.setdefault(v.object_id, []).append(v)
    dead: list[Version] = []
    for vs in by_obj.values():
        for v in vs:
            dominators = [
                (w.valid_from, w.valid_to)
                for w in vs
                if w.priority > v.priority and w.tx_from <= v.tx_from
            ]
            if dominators and _intervals_cover(v.valid_from, v.valid_to, dominators):
                dead.append(v)
    return dead


def gc(
    versions: Iterable[Version], horizon: float | None = None
) -> tuple[list[Version], list[Version]]:
    """Split ``versions`` into (kept, reclaimed).

    * horizon=None: drop only birth-dominated versions; every conceivable
      query still returns the same evidence.
    * horizon=H: additionally compact everything with tx_from <= H into a
      snapshot as of H.  Precondition: no future query with t_tx < H.
    """
    versions = list(versions)
    if horizon is None:
        dead = birth_dominated(versions)
        dead_ids = {id(v) for v in dead}
        return [v for v in versions if id(v) not in dead_ids], dead

    late = [v for v in versions if v.tx_from > horizon]
    old = [v for v in versions if v.tx_from <= horizon]
    max_seq = max((v.seq for v in versions), default=0)
    snapshot: list[Version] = []
    for obj in dict.fromkeys(v.object_id for v in old):
        for lo, hi, winner in reference_timeline(old, obj, horizon):
            if winner is None:
                continue
            max_seq += 1
            snapshot.append(
                Version(obj, lo, hi, horizon, max_seq, winner.kind, winner.value)
            )
    return late + snapshot, old

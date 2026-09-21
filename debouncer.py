"""Pausable/resumable debouncer driven entirely by explicit event time.

A source feeds observed values in non-decreasing event time.  A value becomes
*confirmed* only after it has stayed the candidate continuously for at least
``threshold`` time units.  An unknown observation interrupts the candidate
immediately and its stable-time counter is discarded.

Time semantics
--------------
* There is one monotonic global clock (``Debouncer(threshold, now=...)``).
  ``ingest``/``advance`` only move forward; going backwards raises ``ValueError``.
* Several events may share a timestamp.  They take effect in **ingestion order**
  (the order passed to ``ingest``): only the final value of that timestamp is
  the candidate afterwards, and if any event at that timestamp is unknown the
  batch ends with no candidate.
* A repeated/duplicate observation of the current candidate neither resets nor
  extends ``since``; the original anchor timestamp is kept.
* Maturity is ``now - since >= threshold``.  It is evaluated after every ingest
  (for that source) and after every ``advance`` (all sources, in source
  creation order, which is also the tie-break when several sources mature at
  the same instant).
* Confirmation is an *edge* relative to the last confirmed value: a value equal
  to ``last_confirmed`` is never emitted again, even after an unknown gap.

Persistence
-----------
``save``/``to_json`` capture only folded state -- no raw event history:
``threshold``, global ``now``, the outbox sequence counter, and per source the
triple ``(candidate, since, last_confirmed)`` plus the unconsumed outbox.
Events are folded into ``(candidate, since)`` at ingest time and released
immediately; they are never stored.  Therefore:

* an unmet candidate (and its elapsed anchor) survives save/restore;
* a confirmed-but-unconsumed item survives save/restore and is redelivered
  exactly once via ``drain``;
* once drained, an item leaves the persisted state and can never be re-sent.
"""

from __future__ import annotations

import json
from collections import namedtuple
from typing import Any, Dict, Iterable, List, Optional, Tuple

__all__ = ["Confirmed", "Debouncer"]

_VERSION = 1
_UNSET = object()

Confirmed = namedtuple("Confirmed", ["seq", "source", "value", "at", "since"])
"""A confirmed change.

seq         monotonic, per-debouncer confirmation id (stable across restore)
source      source identifier
value       confirmed value
at          event time at which the stability duration was satisfied
since       anchor time from which ``value`` was continuously candidate
"""


class _SourceState:
    __slots__ = ("candidate", "since", "last_confirmed")

    def __init__(self) -> None:
        self.candidate: Any = None
        self.since: Optional[float] = None
        self.last_confirmed: Any = _UNSET

    def to_dict(self) -> Dict[str, Any]:
        return {
            "candidate": self.candidate,
            "since": self.since,
            "has_last": self.last_confirmed is not _UNSET,
            "last_confirmed": (
                None if self.last_confirmed is _UNSET else self.last_confirmed
            ),
        }

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "_SourceState":
        state = cls()
        state.candidate = data["candidate"]
        state.since = data["since"]
        state.last_confirmed = (
            _UNSET if not data.get("has_last", False) else data["last_confirmed"]
        )
        return state


class Debouncer:
    """Debounce multiple independent sources over an explicit event clock."""

    def __init__(self, threshold: float, now: float = 0.0) -> None:
        if threshold < 0:
            raise ValueError("threshold must be non-negative")
        self._threshold = float(threshold)
        self._now = float(now)
        self._sources: Dict[Any, _SourceState] = {}
        self._outbox: List[Confirmed] = []
        self._next_seq = 1

    # -- introspection ----------------------------------------------------

    @property
    def now(self) -> float:
        return self._now

    @property
    def threshold(self) -> float:
        return self._threshold

    @property
    def pending(self) -> Tuple[Confirmed, ...]:
        """Confirmed items not yet consumed (does not consume them)."""
        return tuple(self._outbox)

    def state_of(self, source: Any) -> Dict[str, Any]:
        """Folded state of one source (introspection / assertions)."""
        state = self._sources.get(source)
        if state is None:
            return {"candidate": None, "since": None, "last_confirmed": "__unset__"}
        return {
            "candidate": state.candidate,
            "since": state.since,
            "last_confirmed": "__unset__"
            if state.last_confirmed is _UNSET
            else state.last_confirmed,
        }

    # -- input ------------------------------------------------------------

    def ingest(self, source: Any, value: Any, ts: float) -> None:
        """Fold one observation of ``source`` at event time ``ts``.

        ``value=None`` means *unknown* and interrupts any candidate timing.
        Multiple calls with the same ``ts`` are applied in call order.
        """
        ts = float(ts)
        if ts < self._now:
            raise ValueError(
                f"event time {ts} is before current time {self._now}"
            )
        self._now = ts
        state = self._sources.get(source)
        if state is None:
            state = _SourceState()
            self._sources[source] = state

        if value is None:
            # Unknown: discard candidate and its stability anchor.
            state.candidate = None
            state.since = None
            return

        if state.candidate != value:
            # A genuinely new candidate starts a fresh stability window.
            state.candidate = value
            state.since = ts
        # A duplicate (candidate == value) leaves `since` untouched: repeats
        # neither reset nor extend the stable-time counter.
        self._confirm_if_mature(source, state)

    def ingest_unknown(self, source: Any, ts: float) -> None:
        """Convenience wrapper for an unknown observation."""
        self.ingest(source, None, ts)

    def ingest_batch(self, events: Iterable[Tuple[Any, Any, float]]) -> None:
        """Apply ``(source, value, ts)`` events; same-ts order = iteration order."""
        for source, value, ts in events:
            self.ingest(source, value, ts)

    def advance(self, ts: float) -> None:
        """Move the global event clock to ``ts`` and mature any candidates."""
        ts = float(ts)
        if ts < self._now:
            raise ValueError(f"cannot advance backwards from {self._now} to {ts}")
        if ts == self._now:
            return
        self._now = ts
        # Deterministic tie-break: source creation/insertion order.
        for source, state in self._sources.items():
            self._confirm_if_mature(source, state)

    # -- output -----------------------------------------------------------

    def drain(self) -> List[Confirmed]:
        """Return and remove all confirmed items, in confirmation order."""
        ready = self._outbox
        self._outbox = []
        return ready

    # -- persistence ------------------------------------------------------

    def save(self) -> Dict[str, Any]:
        """Serializable snapshot (JSON-safe values assumed for observations)."""
        return {
            "version": _VERSION,
            "threshold": self._threshold,
            "now": self._now,
            "next_seq": self._next_seq,
            "sources": [
                [source, state.to_dict()] for source, state in self._sources.items()
            ],
            "outbox": [list(item) for item in self._outbox],
        }

    def to_json(self) -> str:
        return json.dumps(self.save())

    @classmethod
    def restore(cls, snapshot: Dict[str, Any]) -> "Debouncer":
        if snapshot.get("version") != _VERSION:
            raise ValueError("unsupported snapshot version")
        deb = cls(snapshot["threshold"], now=snapshot["now"])
        deb._next_seq = snapshot["next_seq"]
        for source, data in snapshot["sources"]:
            deb._sources[_tuplify(source)] = _SourceState.from_dict(data)
        deb._outbox = [Confirmed(*item) for item in snapshot["outbox"]]
        return deb

    @classmethod
    def from_json(cls, text: str) -> "Debouncer":
        return cls.restore(json.loads(text))

    # -- internals --------------------------------------------------------

    def _confirm_if_mature(self, source: Any, state: _SourceState) -> None:
        if state.candidate is None or state.since is None:
            return
        if state.candidate == state.last_confirmed:
            return
        if self._now - state.since >= self._threshold:
            item = Confirmed(
                seq=self._next_seq,
                source=source,
                value=state.candidate,
                at=self._now,
                since=state.since,
            )
            self._next_seq += 1
            state.last_confirmed = state.candidate
            self._outbox.append(item)



def _tuplify(obj):
    # JSON turns tuple source ids into lists; restore them recursively.
    if isinstance(obj, list):
        return tuple(_tuplify(item) for item in obj)
    return obj

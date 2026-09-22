"""Pausable/resumable event-time debouncer (stdlib only).

A downstream consumer only wants state changes that have been *stable for a
configured duration of event time*. This module implements that as a
per-source state machine driven entirely by explicit time advancement -- no
wall clock, no threads, no ``sleep``.

Core semantics
--------------
- Every source has at most one **candidate** value at a time, tagged with the
  event-time ``since`` at which the candidate began.
- A candidate is **confirmed** once ``advance_time``/``ingest`` reaches
  ``since + threshold`` and the candidate is still the latest known value.
  Confirmation emits exactly one ``Confirmation`` into the outbox.
- An **unknown** value (``None``) interrupts the candidate: the candidate is
  discarded and its timer is gone. ``None`` itself never becomes a candidate.
- A **duplicate** of the current candidate does not reset or extend the
  timer (``since`` stays put). A duplicate of the confirmed value is ignored.
- A value equal to the already-confirmed value *cancels* a pending candidate
  (the source reverts to its confirmed state, no re-confirmation needed).
- Sources are fully isolated: each has its own candidate/confirmed state.

Ordering at the same timestamp
------------------------------
1. Events carry an arrival sequence number; events with equal ``ts`` are
   applied in arrival (ingest-call) order.
2. At any given event-time ``t``, all events with ``ts == t`` are applied
   *before* confirmations due at ``t`` are evaluated. In other words, a
   candidate must still be the latest known value at its confirmation
   instant; an event landing exactly on the boundary can still preempt it.
3. ``ingest`` never accepts ``ts < now`` (monotonic event time); doing so
   raises ``ValueError``.

What is persisted (save/restore)
--------------------------------
``save()`` returns a JSON-serializable dict containing:
- ``threshold`` and the current event-time ``now``;
- per-source state: confirmed value (if any), candidate value + ``since``
  (if any);
- the **outbox**: confirmations already generated but not yet consumed via
  ``drain()``;
- the arrival sequence counter (keeps same-timestamp ordering stable across
  a restore).

After ``restore``, previously confirmed changes are NOT re-emitted (they
live in the outbox exactly once, and consumed ones are gone), and pending
candidates resume with their original ``since`` -- no duration is lost.

When old data is released
-------------------------
- Input events are folded into per-source state the moment they are
  ingested; the event objects themselves are never retained. Memory stays
  O(#sources + #unconsumed confirmations), independent of event count.
- A candidate slot is released when the candidate is confirmed, cancelled
  by a revert to the confirmed value, or interrupted by an unknown value.
- An outbox entry is released only when the consumer takes it via
  ``drain()``; until then it survives save/restore cycles.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, Hashable, List, Optional, Tuple


class _Missing:
    """Sentinel for 'no value' (distinct from None, which means 'unknown')."""

    _instance = None

    def __new__(cls) -> "_Missing":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return "MISSING"


MISSING = _Missing()

UNKNOWN = None  # an ingested value of None means "unknown"


@dataclass(frozen=True)
class Confirmation:
    """A confirmed state change, delivered via the outbox / drain()."""

    source: Hashable
    value: Any
    since: float  # event-time at which the (winning) candidate began
    confirmed_at: float  # == since + threshold


@dataclass
class _SourceState:
    confirmed: Any = MISSING
    candidate: Any = MISSING
    candidate_since: float = 0.0


def _pack_value(value: Any) -> Dict[str, Any]:
    if value is MISSING:
        return {"present": False}
    return {"present": True, "value": value}


def _unpack_value(packed: Dict[str, Any]) -> Any:
    return packed["value"] if packed["present"] else MISSING


class Debouncer:
    """Duration-confirming debouncer driven by explicit event time."""

    def __init__(self, threshold: float) -> None:
        if threshold <= 0:
            raise ValueError("threshold must be positive")
        self.threshold = float(threshold)
        self.now = 0.0
        self._seq = 0
        self._sources: Dict[Hashable, _SourceState] = {}
        self._outbox: List[Confirmation] = []

    # ------------------------------------------------------------------ #
    # Intake and time advancement
    # ------------------------------------------------------------------ #

    def ingest(self, source: Hashable, value: Any, ts: float) -> None:
        """Apply one event. ``value=None`` means the source became unknown.

        Advances event time to ``ts`` and then evaluates confirmations due
        at that instant (events at ``ts`` are applied first; see module
        docstring for the ordering contract).
        """
        if ts < self.now:
            raise ValueError(
                f"event time went backwards: ts={ts} < now={self.now}"
            )
        self.now = float(ts)
        self._seq += 1
        state = self._sources.setdefault(source, _SourceState())

        if value is UNKNOWN:
            # Unknown interrupts the candidate timer; it is not a candidate.
            state.candidate = MISSING
        elif state.candidate is not MISSING and value == state.candidate:
            pass  # duplicate of the candidate: timer keeps running
        elif state.confirmed is not MISSING and value == state.confirmed:
            state.candidate = MISSING  # revert to confirmed: cancel candidate
        else:
            state.candidate = value
            state.candidate_since = self.now

        self._evaluate()

    def advance_time(self, ts: float) -> None:
        """Advance event time without an event; confirms anything due."""
        if ts < self.now:
            raise ValueError(
                f"cannot move time backwards: ts={ts} < now={self.now}"
            )
        self.now = float(ts)
        self._evaluate()

    def _evaluate(self) -> None:
        for source, state in self._sources.items():
            if state.candidate is MISSING:
                continue
            due = state.candidate_since + self.threshold
            if due <= self.now:
                confirmed = Confirmation(
                    source=source,
                    value=state.candidate,
                    since=state.candidate_since,
                    confirmed_at=due,
                )
                state.confirmed = state.candidate
                state.candidate = MISSING
                self._outbox.append(confirmed)

    # ------------------------------------------------------------------ #
    # Consumption
    # ------------------------------------------------------------------ #

    def drain(self) -> List[Confirmation]:
        """Take all pending confirmations. Released from the outbox."""
        out, self._outbox = self._outbox, []
        return out

    @property
    def pending(self) -> int:
        """Number of generated-but-unconsumed confirmations."""
        return len(self._outbox)

    # ------------------------------------------------------------------ #
    # Checkpointing
    # ------------------------------------------------------------------ #

    def save(self) -> Dict[str, Any]:
        """Snapshot everything needed to resume without loss or duplication.

        The returned dict contains only JSON-serializable primitives
        (provided event values and source names are themselves
        JSON-serializable).
        """
        return {
            "threshold": self.threshold,
            "now": self.now,
            "seq": self._seq,
            "sources": [
                {
                    "source": source,
                    "confirmed": _pack_value(state.confirmed),
                    "candidate": _pack_value(state.candidate),
                    "candidate_since": state.candidate_since,
                }
                for source, state in self._sources.items()
            ],
            "outbox": [
                {
                    "source": c.source,
                    "value": c.value,
                    "since": c.since,
                    "confirmed_at": c.confirmed_at,
                }
                for c in self._outbox
            ],
        }

    @classmethod
    def restore(cls, snapshot: Dict[str, Any]) -> "Debouncer":
        """Rebuild a debouncer from ``save()`` output.

        Guarantees: outbox entries are delivered exactly once (already
        drained ones are gone, pending ones survive), and candidates keep
        their original ``since`` so no stability duration is lost.
        """
        deb = cls(snapshot["threshold"])
        deb.now = snapshot["now"]
        deb._seq = snapshot["seq"]
        for entry in snapshot["sources"]:
            deb._sources[entry["source"]] = _SourceState(
                confirmed=_unpack_value(entry["confirmed"]),
                candidate=_unpack_value(entry["candidate"]),
                candidate_since=entry["candidate_since"],
            )
        deb._outbox = [
            Confirmation(
                source=c["source"],
                value=c["value"],
                since=c["since"],
                confirmed_at=c["confirmed_at"],
            )
            for c in snapshot["outbox"]
        ]
        return deb

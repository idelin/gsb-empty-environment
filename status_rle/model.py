"""Core value objects for the source-indexed RLE status history.

Conventions
-----------
* Event times are integers.
* Segments use half-open intervals ``[start, end)``; ``end is None`` means
  the segment extends to +infinity (it is the current, open segment).
* Every event carries a stable identity ``(source, seq)``.  Payload
  (``time``, ``status``) may be corrected, identity may not.
* Canonical event order is ``(time, source, seq)``.  When several events
  share a timestamp, the *last* one in canonical order is the effective
  one; the others stay in the segment provenance as evidence.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional, Tuple


@dataclass(frozen=True, order=True)
class Event:
    """A single raw record.  Identity is ``(source, seq)``."""

    time: int
    source: str
    seq: int
    status: str = field(compare=False)

    @property
    def ref(self) -> Tuple[str, int]:
        return (self.source, self.seq)

    def __str__(self) -> str:  # e.g. "A@2 (src=sensor-1#7)"
        return f"{self.status}@{self.time} (src={self.source}#{self.seq})"


@dataclass(frozen=True)
class Segment:
    """A maximal run of one effective status.

    ``provenance`` holds *every* raw event whose time falls inside the
    segment, in canonical order -- including duplicate records in the
    middle of a merged run and overridden events at a shared timestamp.
    The effective event at ``start`` is the last provenance event whose
    time equals ``start``.
    """

    status: str
    start: int
    end: Optional[int]  # None == +inf (open-ended current segment)
    provenance: Tuple[Event, ...] = ()

    def covers(self, t: int) -> bool:
        return self.start <= t and (self.end is None or t < self.end)

    @property
    def effective_event(self) -> Event:
        """The event that actually established this segment's status."""
        at_start = [e for e in self.provenance if e.time == self.start]
        return at_start[-1] if at_start else self.provenance[0]

    def refs(self) -> Tuple[Tuple[str, int], ...]:
        """(source, seq) identities of all forming events, in order."""
        return tuple(e.ref for e in self.provenance)

    def __str__(self) -> str:
        end = "+inf" if self.end is None else str(self.end)
        refs = ", ".join(f"{s}#{q}" for s, q in self.refs())
        return f"[{self.start}, {end}) {self.status}  <- {{{refs}}}"


@dataclass(frozen=True)
class Patch:
    """Incremental delta between two segment snapshots.

    Value-based: a patch is the set of segments that disappeared
    (``removed``) plus the set that appeared (``added``).  Applying it to
    the snapshot it was computed from yields the new snapshot exactly.
    """

    removed: Tuple[Segment, ...] = ()
    added: Tuple[Segment, ...] = ()

    @property
    def is_empty(self) -> bool:
        return not self.removed and not self.added

    def invert(self) -> "Patch":
        return Patch(removed=self.added, added=self.removed)

    def __str__(self) -> str:
        lines = []
        for s in self.removed:
            lines.append(f"  - {s}")
        for s in self.added:
            lines.append(f"  + {s}")
        return "\n".join(lines) if lines else "  (no-op)"

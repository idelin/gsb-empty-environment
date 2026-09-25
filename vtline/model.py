"""Core data model: entries, segments, change spans, and diffs."""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Hashable, Optional

from .time_model import Instant


@dataclass(frozen=True)
class Entry:
    """A fact placed on the timeline.

    ``identity`` names the fact itself; ``value`` is its current payload.  Two
    entries are equal only when both identity and value match, so a correction
    (same identity, new value) is never confused with a different fact that
    happens to carry the same value.
    """

    identity: str
    value: Hashable


class ChangeKind(Enum):
    ADD = "add"
    DELETE = "delete"
    MODIFY = "modify"


def classify_change(
    before: Optional[Entry], after: Optional[Entry]
) -> Optional[ChangeKind]:
    """Classify a transition, or return ``None`` when nothing changed."""
    if before is None and after is None:
        return None
    if before is None:
        return ChangeKind.ADD
    if after is None:
        return ChangeKind.DELETE
    if before == after:
        return None
    return ChangeKind.MODIFY


@dataclass(frozen=True)
class Segment:
    """A half-open interval ``[start, end)`` carrying one entry."""

    start: Instant
    end: Instant
    entry: Entry


@dataclass(frozen=True)
class ChangeSpan:
    """One minimal continuous change: over ``[start, end)`` the content
    transitions from ``before`` to ``after`` (``None`` means a gap)."""

    start: Instant
    end: Instant
    kind: ChangeKind
    before: Optional[Entry]
    after: Optional[Entry]

    @property
    def is_correction(self) -> bool:
        """True when the same identity is kept and only the value changed."""
        return (
            self.kind is ChangeKind.MODIFY
            and self.before is not None
            and self.after is not None
            and self.before.identity == self.after.identity
        )


@dataclass(frozen=True)
class Diff:
    """An ordered, non-overlapping set of change spans plus the digest of the
    baseline they were computed against (used to verify the baseline summary
    before applying)."""

    changes: tuple[ChangeSpan, ...]
    base_digest: Optional[str] = None

    def __bool__(self):
        return bool(self.changes)

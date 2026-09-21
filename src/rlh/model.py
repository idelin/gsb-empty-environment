"""Core data model: source-tagged records, half-open segments and patches."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Callable, Optional

# A resolver ranks records that share the same timestamp.
# The winner is the record with the *largest* rank; seq is compared first,
# then the source id, so B@1 against A@1 (same seq) is deterministic.
Resolver = Callable[["Record", "Record"], int]


def default_rank(record: "Record") -> tuple[int, str]:
    return record.seq, record.source


@dataclass(frozen=True, slots=True)
class Record:
    """One raw device-state event.

    ``seq`` is a strictly increasing sequence number assigned by the producer;
    ``(source, seq)`` is the globally unique identity of the record.
    """

    time: int
    state: str
    source: str
    seq: int

    @property
    def ref(self) -> tuple[str, int]:
        return self.source, self.seq


@dataclass(frozen=True, slots=True)
class RecordKey:
    """Identity of a record involved in an edit."""

    source: str
    seq: int

    @classmethod
    def of(cls, record: Record) -> "RecordKey":
        return cls(record.source, record.seq)

    def as_ref(self) -> tuple[str, int]:
        return self.source, self.seq


@dataclass(frozen=True, slots=True)
class Segment:
    """A maximal half-open ``[start, end)`` interval of one state.

    ``end`` is ``None`` for the trailing segment. ``sources`` preserves every
    record that forms the segment, including records whose state repeats an
    earlier segment and records that merely re-assert the same state, so the
    evidence is never lost during a merge.
    """

    sid: int
    state: str
    start: int
    end: Optional[int]
    sources: tuple[Record, ...]

    def contains(self, t: int) -> bool:
        return self.start <= t and (self.end is None or t < self.end)

    @property
    def key(self) -> tuple[str, int, Optional[int], tuple[Record, ...]]:
        """Content identity: same key means the segment is unchanged."""
        return self.state, self.start, self.end, self.sources


@dataclass(frozen=True, slots=True)
class Patch:
    """Incremental change to the segment stream for one edit.

    Apply ``removed`` first (match by ``sid``), then append ``added``.
    Every edit is atomic: insert / retract / correct each emit exactly one
    patch (possibly empty).
    """

    kind: str
    record: RecordKey
    removed: tuple[Segment, ...] = field(default_factory=tuple)
    added: tuple[Segment, ...] = field(default_factory=tuple)

    @property
    def empty(self) -> bool:
        return not self.removed and not self.added

    def apply_to(self, segments: list[Segment]) -> list[Segment]:
        """Functional apply: returns a new list after this single patch."""
        removed_ids = {seg.sid for seg in self.removed}
        if not removed_ids.issubset({seg.sid for seg in segments}):
            missing = removed_ids - {seg.sid for seg in segments}
            raise ValueError(f"patch references unknown segment ids: {sorted(missing)}")
        result = [seg for seg in segments if seg.sid not in removed_ids]
        index = {seg.sid: pos for pos, seg in enumerate(result)}
        for new_seg in self.added:
            pos = next(
                (index[other.sid] for other in result if other.start > new_seg.start),
                len(result),
            )
            result.insert(pos, new_seg)
            index = {seg.sid: p for p, seg in enumerate(result)}
        return result

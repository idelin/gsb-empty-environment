"""Versioned merger: corrections produce new versions, old readers keep theirs.

A correction never mutates history. ``LogMerger.correct`` re-merges the
amended event stream into a new immutable ``Version`` and emits an
``OrderDiff`` describing exactly what changed in the order and in the
necessary/uncertain annotations. If the correction makes the data
contradictory, the merge raises ``InfeasibleError``/``CausalCycleError``
and no new version is appended -- recorded times are never force-edited.
"""
from __future__ import annotations

from dataclasses import dataclass

from .events import Event
from .merger import MergeResult, merge
from .timemodel import OffsetTable, WallClock


@dataclass(frozen=True)
class Version:
    number: int
    note: str
    events: tuple[Event, ...]
    result: MergeResult


@dataclass(frozen=True)
class OrderDiff:
    """Incremental delta between two versions."""

    from_version: int
    to_version: int
    order_unchanged: bool
    moved: dict[str, tuple[int, int]]          # id -> (old index, new index)
    became_necessary: frozenset[tuple[str, str]]
    became_uncertain: frozenset[tuple[str, str]]

    def __str__(self):
        lines = [
            f"diff v{self.from_version} -> v{self.to_version}: "
            f"order {'unchanged' if self.order_unchanged else 'changed'}"
        ]
        for eid, (old, new) in sorted(self.moved.items()):
            lines.append(f"  moved {eid}: position {old} -> {new}")
        for a, b in sorted(self.became_necessary):
            lines.append(f"  now necessarily ordered: {a} < {b}")
        for a, b in sorted(self.became_uncertain):
            lines.append(f"  now uncertain: {a} ~ {b}")
        return "\n".join(lines)


class LogMerger:
    def __init__(self, table: OffsetTable):
        self._table = table
        self._versions: list[Version] = []

    def build(self, events, note: str = "initial") -> Version:
        result = merge(events, self._table)
        version = Version(
            number=len(self._versions) + 1,
            note=note,
            events=result.events,
            result=result,
        )
        self._versions.append(version)
        return version

    def correct(
        self,
        event_id: str,
        *,
        wall: WallClock | None = None,
        payload: str | None = None,
        add_causes: tuple[str, ...] = (),
        remove_causes: tuple[str, ...] = (),
        note: str = "",
    ) -> Version:
        """Apply a correction to one event and publish a new version."""
        base = self._versions[-1]
        found = False
        new_events: list[Event] = []
        for e in base.events:
            if e.id == event_id:
                found = True
                causes = tuple(c for c in e.causes if c not in remove_causes)
                causes += tuple(c for c in add_causes if c not in causes)
                e = Event(
                    id=e.id,
                    source=e.source,
                    wall=wall if wall is not None else e.wall,
                    payload=payload if payload is not None else e.payload,
                    causes=causes,
                )
            new_events.append(e)
        if not found:
            raise KeyError(f"no event with id {event_id!r}")
        return self.build(new_events, note=note or f"correct {event_id}")

    @property
    def versions(self) -> tuple[Version, ...]:
        return tuple(self._versions)

    def get_version(self, number: int) -> Version:
        return self._versions[number - 1]

    def diff(self, old: Version, new: Version) -> OrderDiff:
        old_pos = {eid: i for i, eid in enumerate(old.result.order)}
        new_pos = {eid: i for i, eid in enumerate(new.result.order)}
        moved = {
            eid: (old_pos[eid], new_pos[eid])
            for eid in old_pos.keys() & new_pos.keys()
            if old_pos[eid] != new_pos[eid]
        }
        old_unc = old.result.uncertain_pairs
        new_unc = new.result.uncertain_pairs
        became_necessary = frozenset(
            pair for pair in new.result.necessary_pairs
            if tuple(sorted(pair)) in old_unc
        )
        became_uncertain = frozenset(
            pair for pair in new_unc if tuple(sorted(pair)) not in old_unc
        )
        return OrderDiff(
            from_version=old.number,
            to_version=new.number,
            order_unchanged=old.result.order == new.result.order,
            moved=moved,
            became_necessary=became_necessary,
            became_uncertain=became_uncertain,
        )

"""Versioned log store with incremental revision.

Every publish/revise produces an immutable Version; old readers keep their
version. Corrections replace events by id and yield a Delta describing the
order difference between consecutive versions.
"""

from __future__ import annotations

from dataclasses import dataclass

from .merger import Contradiction, MergeResult, Merger


class ConflictError(Exception):
    pass


@dataclass(frozen=True)
class Version:
    number: int
    result: object


@dataclass(frozen=True)
class Delta:
    moved: tuple
    added: tuple
    removed: tuple
    became_necessary: tuple
    became_uncertain: tuple


@dataclass(frozen=True)
class Revision:
    old: Version
    new: Version
    delta: object


def _delta(old: MergeResult, new: MergeResult) -> Delta:
    old_pos = {eid: i for i, eid in enumerate(old.ids)}
    new_pos = {eid: i for i, eid in enumerate(new.ids)}
    moved = tuple(
        sorted(
            (eid, old_pos[eid], new_pos[eid])
            for eid in new_pos
            if eid in old_pos and old_pos[eid] != new_pos[eid]
        )
    )
    added = tuple(sorted(set(new_pos) - set(old_pos)))
    removed = tuple(sorted(set(old_pos) - set(new_pos)))
    became_necessary = tuple(sorted(new.necessary - old.necessary))
    became_uncertain = tuple(sorted(old.necessary - new.necessary))
    return Delta(moved, added, removed, became_necessary, became_uncertain)


class LogStore:
    def __init__(self, offsets):
        self._events = {}
        self._merger = Merger(offsets)
        self._versions = []

    def add_event(self, event):
        existing = self._events.get(event.id)
        if existing is not None and existing != event:
            raise ConflictError(
                f"event id {event.id!r} already exists with different content; "
                "use revise() to correct it"
            )
        self._events[event.id] = event

    def publish(self) -> Version:
        result = self._merger.merge(list(self._events.values()))
        version = Version(len(self._versions), result)
        self._versions.append(version)
        return version

    def revise(self, corrections) -> Revision:
        if not self._versions:
            raise RuntimeError("publish an initial version before revising")
        old = self._versions[-1]
        for c in corrections:
            if c.id not in self._events:
                raise KeyError(f"cannot correct unknown event {c.id!r}")
            self._events[c.id] = c
        new = self.publish()
        delta = None
        if isinstance(old.result, MergeResult) and isinstance(new.result, MergeResult):
            delta = _delta(old.result, new.result)
        return Revision(old, new, delta)

    def get(self, number: int) -> Version:
        return self._versions[number]

    @property
    def current(self) -> Version:
        return self._versions[-1]

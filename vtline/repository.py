"""Version chain with retraction of corrections, keyed by identity."""

from __future__ import annotations

from typing import Optional

from .model import ChangeKind, ChangeSpan, Diff, Entry
from .timeline import Timeline


class Repository:
    """A linear chain of timeline versions.

    Every commit applies a diff (verifying its baseline digest) and records
    the diff plus the node statistics, so history stays meaningful: a
    correction can later be retracted by the *identity* of the fact it
    touched, never by matching the current value.
    """

    def __init__(self):
        self._versions = [Timeline()]
        self._diffs: list = []
        self._stats: list = []

    @property
    def head(self) -> Timeline:
        return self._versions[-1]

    @property
    def versions(self) -> tuple:
        return tuple(self._versions)

    @property
    def diffs(self) -> tuple:
        return tuple(self._diffs)

    @property
    def stats(self) -> tuple:
        return tuple(self._stats)

    def commit(self, d: Diff):
        new_head, stats = self.head.apply(d)
        self._versions.append(new_head)
        self._diffs.append(d)
        self._stats.append(stats)
        return stats

    def retract(self, identity: str) -> Optional[Diff]:
        """Withdraw the most recent correction made to ``identity``.

        The correction is located by identity through the recorded history,
        and the pre-correction value is restored wherever that identity
        currently lives.  Other facts that merely hold the same value are
        untouched, and the fact itself is not deleted -- its historical
        meaning is preserved.
        """
        target = None
        for d in reversed(self._diffs):
            for c in reversed(d.changes):
                if c.is_correction and c.before.identity == identity:
                    target = c
                    break
            if target is not None:
                break
        if target is None:
            raise KeyError(f"no retractable correction for identity {identity!r}")

        head = self.head
        changes = []
        for seg in head.segments:
            if seg.entry.identity == identity and seg.entry.value != target.before.value:
                changes.append(
                    ChangeSpan(
                        seg.start,
                        seg.end,
                        ChangeKind.MODIFY,
                        seg.entry,
                        Entry(identity, target.before.value),
                    )
                )
        if not changes:
            return None
        d = Diff(tuple(changes), base_digest=head.summary().digest)
        self.commit(d)
        return d

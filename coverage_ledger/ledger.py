"""Append-only, revisable coverage ledger.

The ledger stores two timelines:

* observations -- half-open ``[start, end)`` intervals a device reports up;
* membership   -- rule-versioned intervals saying which devices belong to
  the group.  A membership rule may be open ended (``end=None``).

Every mutating call appends a *revision* whose effect carries enough
information to undo itself.  Rollback/truncate and materialised checkpoints
replay these effects, and a snapshot view at an old revision answers queries
against the old records and the old membership rules.

Duplicate uploads
-----------------
Adding the exact same observation id twice raises ``DuplicateReport``.
A repeated upload of the same device interval under a new id is harmless:
overlapping intervals are union-merged per device, so coverage is never
double counted.
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Tuple

from .errors import DuplicateReport, InvalidInterval, UnknownRecord
from .store import Timeline


@dataclass(frozen=True)
class Revision:
    """One committed, invertible change."""

    seq: int
    kind: str                       # observation | membership | publish
    action: str                     # add | remove | correct | publish
    effect: Dict[str, Any] = field(default_factory=dict)
    summary: str = ""


# Effect payload shapes
# ---------------------
# observation add/remove: {record: (id, device, start, end)}
# membership    add/remove: {record: (id, device, start, end)}
# correct: {removed: (id, device, start, end), added: (id, device, start, end)}
# publish: {report_id, window, mode, k}


class Ledger:
    def __init__(self) -> None:
        self.observations = Timeline()
        self.membership = Timeline()
        self.revisions: List[Revision] = []
        self.reports: Dict[str, Dict[str, Any]] = {}
        self._obs_seq = itertools.count(1)
        self._mem_seq = itertools.count(1)

    # ------------------------------------------------------------------
    # observation management
    # ------------------------------------------------------------------
    def add_observation(self, device: str, start: object, end: object,
                        *, observation_id: Optional[str] = None,
                        summary: str = "") -> Revision:
        """Record a device online interval ``[start, end)``."""
        record_id = observation_id or f"obs-{next(self._obs_seq)}"
        if self.observations.get(record_id, device) is not None or self._find(record_id, self.observations):
            raise DuplicateReport(f"observation {record_id!r} already exists")
        record = self.observations.add(record_id, device, start, end)
        return self._commit("observation", "add", {"record": record},
                            summary or f"add {device} online [{start},{end})")

    def retract_observation(self, observation_id: str, device: str,
                            *, summary: str = "") -> Revision:
        """Withdraw a previously reported interval."""
        record = self.observations.get(observation_id, device)
        if record is None:
            raise UnknownRecord(f"observation {observation_id!r} for {device!r} not found")
        self.observations.remove(observation_id, device)
        return self._commit("observation", "remove", {"record": record},
                            summary or f"retract {observation_id}")

    def correct_observation(self, device: str, old_id: str,
                            new_start: object, new_end: object,
                            *, new_id: Optional[str] = None,
                            summary: str = "") -> Tuple[Revision, Revision]:
        """Withdraw an old interval and enter its corrected version.

        Returns the two revisions (retraction then new version); together
        they are the correctable unit and each remains independently
        reversible in the log.
        """
        retracted = self.retract_observation(old_id, device, summary=summary or f"retract old {old_id}")
        replacement_id = new_id or f"obs-{next(self._obs_seq)}"
        added = self.add_observation(device, new_start, new_end,
                                     observation_id=replacement_id,
                                     summary=summary or f"correct {old_id} -> [{new_start},{new_end})")
        return retracted, added

    # ------------------------------------------------------------------
    # membership management (rule versions)
    # ------------------------------------------------------------------
    def add_membership(self, device: str, start: object, end: Optional[object] = None,
                       *, rule_id: Optional[str] = None,
                       summary: str = "") -> Revision:
        """State that a device is a member during ``[start, end)``.

        ``end=None`` means the rule stays in force until a later version.
        """
        rule_id = rule_id or f"mem-{next(self._mem_seq)}"
        if self.membership.get(rule_id, device) is not None or self._find(rule_id, self.membership):
            raise DuplicateReport(f"membership rule {rule_id!r} already exists")
        record = self.membership.add(rule_id, device, start, end, allow_open_end=True)
        return self._commit("membership", "add", {"record": record},
                            summary or f"membership {device} from {start}")

    def end_membership(self, device: str, at: object, *, rule_id: Optional[str] = None,
                       summary: str = "") -> Revision:
        """Record that a device left the group at ``at``.

        The open-ended membership rule of the device is replaced by a closed
        one ending at ``at``; this is a rule-version correction.
        """
        rule = self._open_membership(device)
        if rule is None:
            raise UnknownRecord(f"no active membership rule for {device!r} ending after {at!r}")
        old_id, dev, start, _ = rule
        new_rule_id = rule_id or f"mem-{next(self._mem_seq)}"
        self.membership.remove(old_id, dev)
        closed = self.membership.add(new_rule_id, dev, start, at, allow_open_end=True)
        return self._commit(
            "membership", "correct",
            {"removed": rule, "added": closed},
            summary or f"{device} leaves at {at}",
        )

    def retract_membership(self, rule_id: str, device: str, *, summary: str = "") -> Revision:
        record = self.membership.get(rule_id, device)
        if record is None:
            raise UnknownRecord(f"membership rule {rule_id!r} for {device!r} not found")
        self.membership.remove(rule_id, device)
        return self._commit("membership", "remove", {"record": record},
                            summary or f"retract {rule_id}")

    def _open_membership(self, device: str) -> Optional[Tuple[str, str, object, Optional[object]]]:
        open_rules = [record for record in self.membership.raw(device) if record[3] is None]
        if not open_rules:
            return None
        return open_rules[-1]

    # ------------------------------------------------------------------
    # revision log
    # ------------------------------------------------------------------
    def _commit(self, kind: str, action: str, effect: Dict[str, Any], summary: str) -> Revision:
        revision = Revision(seq=len(self.revisions) + 1, kind=kind, action=action,
                            effect=effect, summary=summary)
        self.revisions.append(revision)
        return revision

    def _apply(self, revision: Revision, *, inverse: bool = False) -> None:
        effect = revision.effect
        if revision.kind in ("observation", "membership"):
            timeline = self.observations if revision.kind == "observation" else self.membership
            open_end = revision.kind == "membership"
            if revision.action in ("add", "remove"):
                record = effect["record"]
                record_id, device, start, end = record
                adding = (revision.action == "add") != inverse
                if adding:
                    timeline.add(record_id, device, start, end, allow_open_end=open_end)
                else:
                    timeline.remove(record_id, device)
            elif revision.action == "correct":
                removed = effect["removed"]
                added = effect["added"]
                if inverse:
                    timeline.remove(added[0], added[1])
                    timeline.add(removed[0], removed[1], removed[2], removed[3],
                                 allow_open_end=open_end)
                else:
                    timeline.remove(removed[0], removed[1])
                    timeline.add(added[0], added[1], added[2], added[3],
                                 allow_open_end=open_end)
            else:  # pragma: no cover - defensive
                raise InvalidInterval(f"unknown action {revision.action!r}")
        elif revision.kind == "publish":
            report_id = effect["report_id"]
            if inverse:
                self.reports.pop(report_id, None)
            else:
                self.reports[report_id] = {
                    "report_id": report_id,
                    "window": tuple(effect["window"]),
                    "mode": effect["mode"],
                    "k": effect.get("k"),
                    "revision": revision.seq,
                }

    def rollback_to(self, seq: int) -> List[Revision]:
        """Undo revisions back to sequence ``seq`` (head becomes ``seq``)."""
        if seq < 0 or seq > len(self.revisions):
            raise ValueError(f"cannot roll back to seq {seq}")
        undone: List[Revision] = []
        while len(self.revisions) > seq:
            revision = self.revisions.pop()
            self._apply(revision, inverse=True)
            undone.append(revision)
        return undone

    @property
    def head(self) -> int:
        return len(self.revisions)

    # ------------------------------------------------------------------
    # snapshots
    # ------------------------------------------------------------------
    def snapshot(self, at: Optional[int] = None) -> "StateView":
        """Materialised view of records as of revision ``at`` (default head)."""
        at = self.head if at is None else at
        if at < 0 or at > self.head:
            raise ValueError(f"unknown revision {at}")
        view = StateView(self, at)
        view._build()
        return view

    def report_snapshot(self, report_id: str) -> "StateView":
        """View pinned to the revision a report was published at."""
        if report_id not in self.reports:
            raise UnknownRecord(f"report {report_id!r} not found")
        return self.snapshot(self.reports[report_id]["revision"])

    def publish_report(self, report_id: str, window: Tuple[object, object],
                       mode: str = "union", k: Optional[int] = None,
                       *, summary: str = "") -> Revision:
        """Publish a query definition; its answer stays pinned to this seq."""
        if mode not in ("union", "intersection", "at_least_k"):
            raise InvalidInterval(f"unknown mode {mode!r}")
        if mode == "at_least_k" and (not isinstance(k, int) or isinstance(k, bool) or k < 1):
            raise InvalidInterval("at_least_k requires a positive integer k")
        if report_id in self.reports:
            raise DuplicateReport(f"report {report_id!r} already published")
        effect = {"report_id": report_id, "window": tuple(window), "mode": mode, "k": k}
        revision = self._commit("publish", "publish", effect,
                                summary or f"publish {report_id}")
        self._apply(revision)
        return revision

    # ------------------------------------------------------------------
    # internals / reconstruction
    # ------------------------------------------------------------------
    @staticmethod
    def _find(record_id: str, timeline: Timeline) -> bool:
        return any(timeline.get(record_id, device) is not None for device in timeline.devices())

    def replay(self, revisions: List[Revision]) -> None:
        """Rebuild empty state from a list of revisions (checkpoint load)."""
        if self.revisions or self.observations.devices() or self.membership.devices():
            raise InvalidInterval("replay requires an empty ledger")
        for revision in revisions:
            self.revisions.append(revision)
            self._apply(revision)

class StateView:
    """Immutable view of observations + membership at one revision."""

    def __init__(self, ledger: Ledger, at: int) -> None:
        self._ledger = ledger
        self.at = at
        self.observations = Timeline()
        self.membership = Timeline()
        self.reports: Dict[str, Dict[str, Any]] = {}

    def _build(self) -> None:
        for revision in self._ledger.revisions[: self.at]:
            if revision.kind == "observation":
                self._apply_timeline(self.observations, revision, open_end=False)
            elif revision.kind == "membership":
                self._apply_timeline(self.membership, revision, open_end=True)
            elif revision.kind == "publish":
                effect = revision.effect
                self.reports[effect["report_id"]] = {
                    "report_id": effect["report_id"],
                    "window": tuple(effect["window"]),
                    "mode": effect["mode"],
                    "k": effect.get("k"),
                    "revision": revision.seq,
                }

    @staticmethod
    def _apply_timeline(timeline: Timeline, revision: Revision, *, open_end: bool) -> None:
        effect = revision.effect
        if revision.action in ("add", "remove"):
            record_id, device, start, end = effect["record"]
            if revision.action == "add":
                timeline.add(record_id, device, start, end, allow_open_end=open_end)
            else:
                timeline.remove(record_id, device)
        else:  # correct
            removed, added = effect["removed"], effect["added"]
            timeline.remove(removed[0], removed[1])
            timeline.add(added[0], added[1], added[2], added[3], allow_open_end=open_end)

    def members_during(self, window: Tuple[object, object]) -> List[str]:
        """Devices with any membership overlap in the window."""
        return [device for device in self.membership.devices()
                if self.membership.covering(device, window)]

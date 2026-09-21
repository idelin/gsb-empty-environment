"""Published reports, reversible deltas and revision impact reporting."""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Any, Dict, List, Optional, Tuple

from . import intervals
from .errors import UnknownRecord
from .ledger import Ledger
from .query import QueryResult, query


@dataclass(frozen=True)
class Delta:
    """Reversible change between a published answer and the current one."""

    report_id: str
    mode: str
    k: Optional[int]
    window: Tuple[object, object]
    old_revision: int
    new_revision: int
    old_length: object
    new_length: object
    difference: object
    old_rate: Fraction
    new_rate: Fraction
    gained_segments: Tuple[Tuple[object, object], ...]   # newly covered
    lost_segments: Tuple[Tuple[object, object], ...]     # no longer covered

    @property
    def is_conserving(self) -> bool:
        """new == old + gained - lost (delta conservation)."""
        delta_length = intervals.length(self.gained_segments) - intervals.length(self.lost_segments)
        return self.old_length + delta_length == self.new_length

    def inverse(self) -> "Delta":
        """Delta that takes the new answer back to the published one."""
        return Delta(
            report_id=self.report_id, mode=self.mode, k=self.k, window=self.window,
            old_revision=self.new_revision, new_revision=self.old_revision,
            old_length=self.new_length, new_length=self.old_length,
            difference=-self.difference,
            old_rate=self.new_rate, new_rate=self.old_rate,
            gained_segments=self.lost_segments, lost_segments=self.gained_segments,
        )

    def to_dict(self) -> Dict[str, Any]:
        return {
            "report_id": self.report_id, "mode": self.mode, "k": self.k,
            "window": list(self.window),
            "old_revision": self.old_revision, "new_revision": self.new_revision,
            "old_length": self.old_length, "new_length": self.new_length,
            "difference": self.difference,
            "old_rate": str(self.old_rate), "new_rate": str(self.new_rate),
            "gained_segments": [list(segment) for segment in self.gained_segments],
            "lost_segments": [list(segment) for segment in self.lost_segments],
        }


def _metadata(ledger: Ledger, report_id: str) -> Dict[str, Any]:
    try:
        return ledger.reports[report_id]
    except KeyError:
        raise UnknownRecord(f"report {report_id!r} not found") from None


def run_published(ledger: Ledger, report_id: str) -> QueryResult:
    """Re-run a report against the records at its publish revision."""
    metadata = _metadata(ledger, report_id)
    view = ledger.snapshot(metadata["revision"])
    return query(view, metadata["window"], metadata["mode"], metadata["k"])


def run_current(ledger: Ledger, report_id: str) -> QueryResult:
    """Run a published report's definition against the latest records."""
    metadata = _metadata(ledger, report_id)
    return query(ledger.snapshot(), metadata["window"], metadata["mode"], metadata["k"])


def reconcile(ledger: Ledger, report_id: str) -> Tuple[QueryResult, QueryResult, Delta]:
    """Compare a published answer with the current answer and emit a delta."""
    published = run_published(ledger, report_id)
    current = run_current(ledger, report_id)
    old_segments = [(segment.start, segment.end) for segment in published.covered_segments]
    new_segments = [(segment.start, segment.end) for segment in current.covered_segments]
    gained = tuple(intervals.clip(intervals.subtract(new_segments, old_segments), published.window))
    lost = tuple(intervals.clip(intervals.subtract(old_segments, new_segments), published.window))
    delta = Delta(
        report_id=report_id, mode=published.mode, k=published.k,
        window=published.window,
        old_revision=ledger.reports[report_id]["revision"],
        new_revision=ledger.head,
        old_length=published.known_coverage_length,
        new_length=current.known_coverage_length,
        difference=current.known_coverage_length - published.known_coverage_length,
        old_rate=published.coverage_rate, new_rate=current.coverage_rate,
        gained_segments=gained, lost_segments=lost,
    )
    return published, current, delta


def _old_new_intervals(revision: Any) -> Tuple[List, List, List[str]]:
    """Return (old intervals, new intervals, touched devices) for a revision."""
    effect = revision.effect
    if revision.action in ("add", "remove"):
        record = effect["record"]
        interval = [(record[2], record[3])]
        if revision.action == "add":
            return [], interval, [record[1]]
        return interval, [], [record[1]]
    removed, added = effect["removed"], effect["added"]
    return [(removed[2], removed[3])], [(added[2], added[3])], [removed[1], added[1]]


def revision_impact(ledger: Ledger, seq: int,
                    window: Tuple[object, object]) -> Dict[str, Any]:
    """Report what domain range a revision actually changed inside ``window``.

    ``raw_changed_ranges`` is the symmetric difference between old and new
    record union; ``effective_changed_ranges`` restricts it to where the
    touched device was a group member under the new rules -- the only part
    that can move a coverage answer.  Membership revisions report the member
    ranges they changed instead.
    """
    if seq < 1 or seq > ledger.head:
        raise UnknownRecord(f"revision {seq} not found")
    revision = ledger.revisions[seq - 1]
    after = ledger.snapshot(seq)
    impact: Dict[str, Any] = {
        "seq": seq, "kind": revision.kind, "action": revision.action,
        "summary": revision.summary,
    }

    if revision.kind == "membership":
        effect = revision.effect
        if revision.action in ("add", "remove"):
            record = effect["record"]
            clipped = (intervals.clip([(record[2], record[3])], window) or [None])[0]
            impact.update({
                "devices": [record[1]],
                "changed_membership_ranges": [clipped],
                "removed_ranges": [],
                "added_ranges": [clipped] if revision.action == "add" else [],
            })
        else:
            removed, added = effect["removed"], effect["added"]
            old_clip = (intervals.clip([(removed[2], removed[3])], window) or [None])[0]
            new_clip = (intervals.clip([(added[2], added[3])], window) or [None])[0]
            impact.update({
                "devices": sorted({removed[1], added[1]}),
                "changed_membership_ranges": [old_clip, new_clip],
                "removed_ranges": [old_clip],
                "added_ranges": [new_clip],
            })
        return impact

    if revision.kind == "publish":
        impact.update({"devices": [], "raw_changed_ranges": [],
                       "effective_changed_ranges": []})
        return impact

    old_intervals, new_intervals, devices = _old_new_intervals(revision)
    raw = intervals.clip(intervals.symmetric_difference(old_intervals, new_intervals), window)
    effective: List = []
    for device in set(devices):
        membership = after.membership.active_fragments(device, window)
        effective.extend(intervals.intersect_lists(raw, membership))
    impact.update({
        "devices": sorted(set(devices)),
        "raw_changed_ranges": raw,
        "effective_changed_ranges": intervals.clip(intervals.merge(effective), window),
    })
    return impact

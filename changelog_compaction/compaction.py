"""Merge consecutive same-key changes without crossing visibility barriers."""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from typing import Any

from .changelog import ABSENT, Change, is_absent


@dataclass
class Elision:
    kind: str
    table: str
    key: Any
    commit_seq: int
    txn_id: str
    sources: list[int]
    evidence: list[dict] | None = None

    def to_dict(self) -> dict:
        data = {
            "kind": self.kind,
            "table": self.table,
            "key": self.key,
            "commit_seq": self.commit_seq,
            "txn_id": self.txn_id,
            "sources": self.sources,
        }
        if self.evidence is not None:
            data["evidence"] = self.evidence
        return data


@dataclass
class CompactionResult:
    changes: list[Change]
    elisions: list[Elision]
    merges_used: int
    budget: int | None
    budget_exhausted: bool


@dataclass
class _OutItem:
    change: Change
    position: int


def _segments(chain: list[Change]) -> list[list[Change]]:
    """Split one key chain whenever the intermediate value is ABSENT."""
    segments: list[list[Change]] = []
    current = [chain[0]]
    for change in chain[1:]:
        if is_absent(change.before):
            segments.append(current)
            current = [change]
        else:
            current.append(change)
    segments.append(current)
    return segments


def _elision_kind(first: Change, last: Change) -> str:
    if is_absent(first.before) and is_absent(last.after):
        return "INSERT_DELETE_CANCEL"
    if is_absent(first.before):
        return "MERGE_INSERT_CHAIN"
    if is_absent(last.after):
        return "MERGE_UPDATE_DELETE"
    return "MERGE_UPDATE_CHAIN"


def _evidence(segment: list[Change], audit: bool) -> list[dict] | None:
    if not audit:
        return None
    return [
        {
            "idx": change.idx,
            "before": change.before,
            "after": change.after,
            "op": change.op,
        }
        for change in segment
    ]


def compact(
    changes: list[Change],
    budget: int | None = None,
    audit: bool = False,
) -> CompactionResult:
    """Merge consecutive same-key changes.

    Barriers that are never crossed:
      * commit_seq: declared visibility point / transaction barrier
      * table: schema barrier
      * primary key: entity barrier
      * txn_id mismatch inside one commit is rejected outright

    A chain is cut at every intermediate ABSENT value, so delete/recreate is
    emitted as DELETE + INSERT (fresh identity) instead of a forged UPDATE.
    ``budget`` is the maximum number of raw changes that disappear into a
    merged record; once it is exhausted the remaining fragments are emitted
    uncompacted.
    """
    grouped: dict[tuple, list[Change]] = defaultdict(list)
    order: list[tuple] = []
    for change in sorted(changes, key=lambda c: c.idx):
        group_key = (change.commit_seq, change.table, change.key)
        if group_key not in grouped:
            order.append(group_key)
        grouped[group_key].append(change)

    out: list[_OutItem] = []
    elisions: list[Elision] = []
    merges_used = 0
    budget_exhausted = False
    fused_seq = 0

    for group_key in order:
        commit_seq, table, key = group_key
        chain = grouped[group_key]

        txn_ids = {change.txn_id for change in chain}
        if len(txn_ids) > 1:
            raise ValueError(
                f"refusing to merge across txn barrier for {table}/{key!r} "
                f"at commit {commit_seq}: {sorted(txn_ids)}"
            )

        cursor = chain[0].before
        for change in chain:
            if change.before != cursor:
                raise ValueError(
                    f"non-consecutive chain for {table}/{key!r} at idx "
                    f"{change.idx}: expected {cursor!r}, got {change.before!r}"
                )
            cursor = change.after

        for segment in _segments(chain):
            if len(segment) == 1:
                out.append(_OutItem(segment[0], segment[0].idx))
                continue

            cost = len(segment) - 1
            first, last = segment[0], segment[-1]
            if budget is not None and merges_used + cost > budget:
                budget_exhausted = True
                for change in segment:
                    out.append(_OutItem(change, change.idx))
                continue

            kind = _elision_kind(first, last)
            payload = Elision(
                kind=kind,
                table=table,
                key=key,
                commit_seq=commit_seq,
                txn_id=chain[0].txn_id,
                sources=[change.idx for change in segment],
                evidence=_evidence(segment, audit),
            )
            if kind == "INSERT_DELETE_CANCEL":
                # Net effect is absence; the change itself disappears but the
                # audit trail (always the source ids; payloads only in audit
                # mode) remains attached to the certificate.
                elisions.append(payload)
            else:
                fused_seq -= 1
                fused = Change(
                    idx=fused_seq,
                    txn_id=first.txn_id,
                    table=table,
                    key=key,
                    before=first.before,
                    after=last.after,
                    commit_seq=commit_seq,
                )
                position = min(change.idx for change in segment)
                out.append(_OutItem(fused, position))
                elisions.append(payload)
            merges_used += cost

    out.sort(key=lambda item: (item.change.commit_seq, item.position))
    return CompactionResult(
        changes=[item.change for item in out],
        elisions=elisions,
        merges_used=merges_used,
        budget=budget,
        budget_exhausted=budget_exhausted,
    )


def dump_change(change: Change) -> dict:
    from .changelog import dump_change as _dump

    data = _dump(change)
    data["fused"] = change.idx < 0
    return data

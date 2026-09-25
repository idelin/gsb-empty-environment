"""Versioned fact store with atomic batch corrections."""
from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, Dict, Iterable, List

from .model import ChangeSet, FactSnapshot, FieldValue


class CorrectionError(Exception):
    """Raised when a correction (batch) is invalid; nothing is applied."""


@dataclass(frozen=True)
class SetField:
    field: str
    value: Any
    source: str


@dataclass(frozen=True)
class DeleteField:
    field: str


@dataclass(frozen=True)
class SetRemark:
    key: str
    text: str


def _ensure_serializable(value: Any, what: str) -> None:
    try:
        json.dumps(value)
    except (TypeError, ValueError) as exc:
        raise CorrectionError(f"{what} is not JSON-serializable: {exc}") from exc


class FactStore:
    """Keeps the full version history of every fact."""

    def __init__(self) -> None:
        self._history: Dict[str, List[FactSnapshot]] = {}

    def create(self, fact_id: str, fields: dict, remarks: dict = None) -> FactSnapshot:
        if fact_id in self._history:
            raise CorrectionError(f"fact {fact_id!r} already exists")
        norm = {}
        for name, fv in fields.items():
            fv = fv if isinstance(fv, FieldValue) else FieldValue(*fv)
            _ensure_serializable(fv.value, f"field {name!r}")
            norm[name] = fv
        remarks = dict(remarks or {})
        snap = FactSnapshot(
            fact_id=fact_id,
            version=1,
            fields=norm,
            remarks=remarks,
            change=ChangeSet(updated=frozenset(norm), remarks=frozenset(remarks)),
        )
        self._history[fact_id] = [snap]
        return snap

    def __contains__(self, fact_id: str) -> bool:
        return fact_id in self._history

    def get(self, fact_id: str, version: int = None) -> FactSnapshot:
        try:
            history = self._history[fact_id]
        except KeyError:
            raise CorrectionError(f"unknown fact {fact_id!r}") from None
        if version is None:
            return history[-1]
        if not 1 <= version <= len(history):
            raise CorrectionError(f"fact {fact_id!r} has no version {version}")
        return history[version - 1]

    def current_version(self, fact_id: str) -> int:
        return self.get(fact_id).version

    def correct(self, fact_id: str, ops: Iterable) -> FactSnapshot:
        return self.correct_batch({fact_id: ops})[fact_id]

    def correct_batch(self, batch: Dict[str, Iterable]) -> Dict[str, FactSnapshot]:
        """Apply corrections to several facts atomically.

        Phase 1 validates every op and builds the next snapshots without
        mutating anything; phase 2 commits. Any error in phase 1 leaves
        all facts at their original versions.
        """
        if not batch:
            raise CorrectionError("empty correction batch")
        pending: Dict[str, FactSnapshot] = {}
        for fact_id, ops in batch.items():
            ops = list(ops)
            if not ops:
                raise CorrectionError(f"empty correction for {fact_id!r}")
            current = self.get(fact_id)  # raises for unknown facts
            fields = dict(current.fields)
            remarks = dict(current.remarks)
            updated, deleted, changed_remarks = set(), set(), set()
            seen_fields, seen_remarks = set(), set()
            for op in ops:
                if isinstance(op, SetField):
                    if op.field in seen_fields:
                        raise CorrectionError(f"conflicting ops on field {op.field!r}")
                    seen_fields.add(op.field)
                    _ensure_serializable(op.value, f"field {op.field!r}")
                    fields[op.field] = FieldValue(op.value, op.source)
                    updated.add(op.field)
                    deleted.discard(op.field)
                elif isinstance(op, DeleteField):
                    if op.field in seen_fields:
                        raise CorrectionError(f"conflicting ops on field {op.field!r}")
                    seen_fields.add(op.field)
                    if op.field not in fields:
                        raise CorrectionError(
                            f"fact {fact_id!r} has no field {op.field!r}")
                    del fields[op.field]
                    deleted.add(op.field)
                    updated.discard(op.field)
                elif isinstance(op, SetRemark):
                    if op.key in seen_remarks:
                        raise CorrectionError(f"conflicting ops on remark {op.key!r}")
                    seen_remarks.add(op.key)
                    remarks[op.key] = op.text
                    changed_remarks.add(op.key)
                else:
                    raise CorrectionError(f"unknown op {op!r}")
            change = ChangeSet(frozenset(updated), frozenset(deleted),
                               frozenset(changed_remarks))
            if not change.data_fields and not change.remarks:
                raise CorrectionError(f"correction for {fact_id!r} changes nothing")
            pending[fact_id] = FactSnapshot(
                fact_id=fact_id,
                version=current.version + 1,
                fields=fields,
                remarks=remarks,
                change=change,
            )
        for fact_id, snap in pending.items():
            self._history[fact_id].append(snap)
        return pending

    def changes_since(self, fact_id: str, version: int) -> ChangeSet:
        """Union of all changes that happened after `version`."""
        try:
            history = self._history[fact_id]
        except KeyError:
            raise CorrectionError(f"unknown fact {fact_id!r}") from None
        result = ChangeSet()
        for snap in history[version:]:
            result = result.merge(snap.change)
        return result

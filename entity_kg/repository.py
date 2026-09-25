"""Append-only, replayable entity repository.

State is a materialized view of the JSONL event log: mutating operations only
append events, and :meth:`EntityRepository.rebuild` recomputes every fact,
version, step and conflict. Deleting the log destroys nothing else.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

from .derive import derive
from .model import (
    DerivedState,
    Event,
    EventKind,
    Fact,
    FactKind,
    make_fact_id,
)
from .rules import RULES, Rule, all_rules

SCHEMA_VERSION = 1


class ConflictError(Exception):
    """Raised in strict mode when evidence produces a conflict cycle."""

    def __init__(self, conflicts: list[dict]):
        super().__init__(f"{len(conflicts)} conflicting evidence cycle(s)")
        self.conflicts = conflicts


@dataclass
class OperationResult:
    event: Event
    fact_id: str
    conflict_ids: list[str]


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="microseconds")


def _validate_node(node: str) -> None:
    if not isinstance(node, str) or not node:
        raise ValueError("node id must be a non-empty string")
    if node != node.strip():
        raise ValueError(f"node id must not contain surrounding space: {node!r}")


class EntityRepository:
    def __init__(self, path: str | Path | None = None, *,
                 strict: bool = False, clock=_utc_now):
        self.path = Path(path) if path else None
        self.strict = strict
        self._clock = clock
        self.events: list[Event] = []
        self.facts: dict[str, Fact] = {}
        self.labels: dict[str, str] = {}
        self.known_nodes: set[str] = set()
        self.state: DerivedState = DerivedState(
            version=0, representatives={}, entities=[], steps=[], conflicts=[])
        if self.path and self.path.exists():
            self._load()
            self.rebuild()

    # ------------------------------------------------------------------ #
    # public mutation API
    # ------------------------------------------------------------------ #
    def add_name(self, node: str, name: str) -> Event:
        """Attach a display name. Names never merge entities (R-NAME-LABEL)."""
        _validate_node(node)
        self.known_nodes.add(node)
        event = self._append(EventKind.LABEL, a=node, name=name,
                             reason="display name", rule_id="R-NAME-LABEL")
        self.rebuild()
        return event

    def seed_node(self, node: str) -> Event:
        """Introduce a node with no evidence yet (becomes a singleton entity)."""
        _validate_node(node)
        self.known_nodes.add(node)
        event = self._append(EventKind.SEED, a=node, reason="node introduced",
                             rule_id="R-REP-MIN")
        self.rebuild()
        return event

    def assert_eq(self, a: str, b: str, *, reason: str) -> OperationResult:
        return self._assert_relation(FactKind.EQ, a, b, reason)

    def assert_neq(self, a: str, b: str, *, reason: str) -> OperationResult:
        return self._assert_relation(FactKind.NEQ, a, b, reason)

    def retract(self, a: str, b: str, kind: FactKind, *, reason: str) -> Event:
        """Retract an explicit evidence edge (e.g. an EQ edge)."""
        _validate_node(a)
        _validate_node(b)
        target = self._find_active_fact(kind, a, b)
        if target is None:
            raise KeyError(
                f"no active {kind} fact between {a!r} and {b!r} to retract")
        retract_rule = ("R-EQ-EXPLICIT" if kind is FactKind.EQ
                        else "R-NEQ-EXPLICIT")
        event = self._append(EventKind.RETRACT, fact_id=target.fid,
                             a=target.a, b=target.b, fact_kind=kind,
                             reason=reason, rule_id=retract_rule)
        self.rebuild()
        return event

    def retract_fact(self, fact_id: str, *, reason: str) -> Event:
        target = self.facts.get(fact_id)
        if target is None or not target.active:
            raise KeyError(f"no active fact with id {fact_id}")
        return self.retract(target.a, target.b, target.kind, reason=reason)

    # ------------------------------------------------------------------ #
    # reads
    # ------------------------------------------------------------------ #
    def representative(self, node: str) -> str | None:
        return self.state.representatives.get(node)

    def entity_of(self, node: str) -> dict | None:
        rep = self.representative(node)
        if rep is None:
            return None
        for entity in self.state.entities:
            if entity.representative == rep:
                return entity.to_dict()
        return None

    def conflicts(self) -> list[dict]:
        return [c.to_dict() for c in self.state.conflicts]

    def is_consistent(self) -> bool:
        return not self.state.conflicts

    def rules(self) -> list[Rule]:
        return all_rules()

    def fact(self, a: str, b: str, kind: FactKind) -> Fact | None:
        return self._find_active_fact(kind, a, b)

    def export_log(self) -> list[dict]:
        return [e.to_dict() for e in self.events]

    def snapshot(self) -> dict:
        return {
            "schema_version": SCHEMA_VERSION,
            "events": self.export_log(),
            "labels": dict(sorted(self.labels.items())),
            "known_nodes": sorted(self.known_nodes),
            "facts": {fid: f.to_dict() for fid, f in sorted(self.facts.items())},
            "derived": self.state.to_dict(),
            "rules": [r.to_dict() for r in all_rules()],
        }

    # ------------------------------------------------------------------ #
    # replay / persistence
    # ------------------------------------------------------------------ #
    def rebuild(self) -> DerivedState:
        """Recompute the materialized view from active evidence."""
        self.state = derive(self.facts, self.labels, self.known_nodes,
                            state_version=len(self.events))
        if self.strict and self.state.conflicts:
            raise ConflictError(self.conflicts())
        return self.state

    def save(self) -> Path:
        if self.path is None:
            raise ValueError("repository has no path")
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with self.path.open("w", encoding="utf-8") as fh:
            for event in self.events:
                fh.write(json.dumps(event.to_dict(),
                                    ensure_ascii=False, sort_keys=True) + "\n")
        return self.path

    @classmethod
    def replay(cls, path: str | Path, **kwargs) -> "EntityRepository":
        """Rebuild a repository purely from its log; deterministic."""
        repo = cls(**kwargs)
        repo.path = Path(path)
        repo._load()
        repo.rebuild()
        return repo

    def _load(self) -> None:
        assert self.path is not None
        with self.path.open("r", encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if line:
                    self._apply(Event.from_dict(json.loads(line)), persist=False)

    # ------------------------------------------------------------------ #
    # internals
    # ------------------------------------------------------------------ #
    def _assert_relation(self, kind: FactKind, a: str, b: str,
                         reason: str) -> OperationResult:
        _validate_node(a)
        _validate_node(b)
        if not reason:
            raise ValueError("evidence requires a non-empty reason")
        self.known_nodes.update((a, b))
        existing = self._find_active_fact(kind, a, b)
        if existing is not None:
            original = self.events[existing.added_seq - 1]
            return OperationResult(
                event=original, fact_id=existing.fid,
                conflict_ids=[c.cid for c in self.state.conflicts])
        fid = make_fact_id(kind, a, b, reason)
        rule_id = "R-EQ-EXPLICIT" if kind is FactKind.EQ else "R-NEQ-EXPLICIT"
        event = self._append(EventKind.ASSERT, fact_id=fid, a=a, b=b,
                             fact_kind=kind, reason=reason, rule_id=rule_id)
        self.facts[fid] = Fact(
            fid=fid, kind=kind, a=a, b=b, reason=reason, version=1,
            added_seq=event.seq, active=True)
        self.rebuild()
        result = OperationResult(
            event=event, fact_id=fid,
            conflict_ids=[c.cid for c in self.state.conflicts])
        if self.strict and self.state.conflicts:
            raise ConflictError(self.conflicts())
        return result

    def _find_active_fact(self, kind: FactKind, a: str,
                          b: str) -> Fact | None:
        ordered = tuple(sorted((a, b)))
        for fact in self.facts.values():
            if fact.active and fact.kind is kind and fact.ordered == ordered:
                return fact
        return None

    def _append(self, kind: EventKind, *, a=None, b=None, fact_id=None,
                fact_kind=None, name=None, reason="",
                rule_id="R-EQ-EXPLICIT") -> Event:
        event = Event(
            seq=len(self.events) + 1,
            kind=kind,
            fact_id=fact_id,
            a=a,
            b=b,
            fact_kind=fact_kind,
            name=name,
            reason=reason,
            rule_version=RULES[rule_id].version,
            ts=self._clock(),
        )
        self._apply(event, persist=True)
        return event

    def _apply(self, event: Event, *, persist: bool) -> None:
        self.events.append(event)
        if event.kind is EventKind.RETRACT:
            fact = self.facts.get(event.fact_id or "")
            if fact is not None and fact.active:
                fact.active = False
                fact.retracted_seq = event.seq
                fact.version += 1
        elif event.kind is EventKind.ASSERT:
            fid = event.fact_id or ""
            if fid not in self.facts and event.fact_kind is not None:
                self.facts[fid] = Fact(
                    fid=fid, kind=event.fact_kind, a=event.a or "",
                    b=event.b or "", reason=event.reason, version=1,
                    added_seq=event.seq, active=True)
        elif event.kind is EventKind.LABEL:
            self.known_nodes.add(event.a or "")
            self.labels[event.a] = event.name or ""
        elif event.kind is EventKind.SEED:
            self.known_nodes.add(event.a or "")
        if persist and self.path is not None:
            with self.path.open("a", encoding="utf-8") as fh:
                fh.write(json.dumps(event.to_dict(),
                                    ensure_ascii=False, sort_keys=True) + "\n")

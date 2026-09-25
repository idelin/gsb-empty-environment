"""Core data model for the evidence-driven entity store.

Everything is content-addressed where identity matters (facts, derived
steps, conflicts) so that replay produces byte-identical stable IDs.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from enum import StrEnum
from typing import Any


def digest(payload: str) -> str:
    return "sha256:" + hashlib.sha256(payload.encode("utf-8")).hexdigest()[:16]


class FactKind(StrEnum):
    EQ = "EQ"
    NEQ = "NEQ"


class EventKind(StrEnum):
    ASSERT = "ASSERT"
    RETRACT = "RETRACT"
    LABEL = "LABEL"
    SEED = "SEED"  # a node introduced without any fact yet


@dataclass(frozen=True)
class Rule:
    rid: str
    version: int
    name: str
    description: str

    def to_dict(self) -> dict[str, Any]:
        return {"id": self.rid, "version": self.version, "name": self.name,
                "description": self.description}


@dataclass
class Event:
    """An immutable, append-only entry in the evidence log."""

    seq: int
    kind: EventKind
    fact_id: str | None
    a: str | None
    b: str | None
    fact_kind: FactKind | None
    name: str | None
    reason: str
    rule_version: int
    ts: str

    def to_dict(self) -> dict[str, Any]:
        return {
            "seq": self.seq,
            "kind": str(self.kind),
            "fact_id": self.fact_id,
            "a": self.a,
            "b": self.b,
            "fact_kind": str(self.fact_kind) if self.fact_kind else None,
            "name": self.name,
            "reason": self.reason,
            "rule_version": self.rule_version,
            "ts": self.ts,
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Event":
        return cls(
            seq=d["seq"],
            kind=EventKind(d["kind"]),
            fact_id=d["fact_id"],
            a=d["a"],
            b=d["b"],
            fact_kind=FactKind(d["fact_kind"]) if d.get("fact_kind") else None,
            name=d.get("name"),
            reason=d["reason"],
            rule_version=d["rule_version"],
            ts=d["ts"],
        )


@dataclass
class Fact:
    """An explicit EQ / NEQ evidence statement."""

    fid: str
    kind: FactKind
    a: str
    b: str
    reason: str
    version: int
    added_seq: int
    active: bool = True
    retracted_seq: int | None = None

    @property
    def ordered(self) -> tuple[str, str]:
        """Canonical unordered pair (facts are symmetric)."""
        return tuple(sorted((self.a, self.b)))  # type: ignore[return-value]

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.fid,
            "kind": str(self.kind),
            "a": self.a,
            "b": self.b,
            "reason": self.reason,
            "version": self.version,
            "added_seq": self.added_seq,
            "active": self.active,
            "retracted_seq": self.retracted_seq,
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Fact":
        return cls(
            fid=d["id"],
            kind=FactKind(d["kind"]),
            a=d["a"],
            b=d["b"],
            reason=d["reason"],
            version=d["version"],
            added_seq=d["added_seq"],
            active=d["active"],
            retracted_seq=d.get("retracted_seq"),
        )


def make_fact_id(kind: FactKind, a: str, b: str, reason: str) -> str:
    x, y = sorted((a, b))
    return digest(f"fact|{kind}|{x}|{y}|{reason}")


@dataclass(frozen=True)
class PremiseEdge:
    """One edge inside a derivation chain."""

    step_id: str | None  # None => direct explicit fact
    fact_id: str
    a: str
    b: str

    def to_dict(self) -> dict[str, Any]:
        return {"step_id": self.step_id, "fact_id": self.fact_id, "a": self.a,
                "b": self.b}

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "PremiseEdge":
        return cls(step_id=d.get("step_id"), fact_id=d["fact_id"], a=d["a"], b=d["b"])


@dataclass
class Step:
    """A derived equivalence produced by a named rule from explicit premises."""

    sid: str
    rule_id: str
    rule_version: int
    version: int
    a: str
    b: str
    chain: list[PremiseEdge]

    @property
    def ordered(self) -> tuple[str, str]:
        return tuple(sorted((self.a, self.b)))  # type: ignore[return-value]

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.sid,
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "version": self.version,
            "a": self.a,
            "b": self.b,
            "chain": [p.to_dict() for p in self.chain],
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Step":
        return cls(
            sid=d["id"],
            rule_id=d["rule_id"],
            rule_version=d["rule_version"],
            version=d["version"],
            a=d["a"],
            b=d["b"],
            chain=[PremiseEdge.from_dict(p) for p in d["chain"]],
        )


def make_step_id(rule_id: str, chain_fact_ids: list[str], a: str, b: str) -> str:
    return digest(f"step|{rule_id}|{a}|{b}|{'|'.join(chain_fact_ids)}")


@dataclass(frozen=True)
class CycleEdge:
    fact_id: str
    a: str
    b: str
    kind: FactKind
    step_id: str | None  # supporting derived step when kind == EQ

    def to_dict(self) -> dict[str, Any]:
        return {"fact_id": self.fact_id, "a": self.a, "b": self.b,
                "kind": str(self.kind), "step_id": self.step_id}

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "CycleEdge":
        return cls(fact_id=d["fact_id"], a=d["a"], b=d["b"],
                   kind=FactKind(d["kind"]), step_id=d.get("step_id"))


@dataclass
class Conflict:
    """A closed evidence cycle: an EQ chain contradicted by an explicit NEQ."""

    cid: str
    kind: str  # "inequality_cycle" | "reflexivity" | "direct_contradiction"
    neq_fact_id: str
    cycle: list[CycleEdge]
    rule_id: str
    rule_version: int
    version: int

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.cid,
            "kind": self.kind,
            "neq_fact_id": self.neq_fact_id,
            "cycle": [e.to_dict() for e in self.cycle],
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "version": self.version,
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Conflict":
        return cls(
            cid=d["id"],
            kind=d["kind"],
            neq_fact_id=d["neq_fact_id"],
            cycle=[CycleEdge.from_dict(e) for e in d["cycle"]],
            rule_id=d["rule_id"],
            rule_version=d["rule_version"],
            version=d["version"],
        )


@dataclass
class Entity:
    representative: str
    members: list[str]
    names: list[str]

    def to_dict(self) -> dict[str, Any]:
        return {"representative": self.representative, "members": self.members,
                "names": self.names}


@dataclass
class DerivedState:
    """A pure materialized view over the active evidence."""

    version: int
    representatives: dict[str, str]
    entities: list[Entity]
    steps: list[Step]
    conflicts: list[Conflict]

    def to_dict(self) -> dict[str, Any]:
        return {
            "version": self.version,
            "representatives": dict(sorted(self.representatives.items())),
            "entities": [e.to_dict() for e in self.entities],
            "steps": [s.to_dict() for s in self.steps],
            "conflicts": [c.to_dict() for c in self.conflicts],
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "DerivedState":
        return cls(
            version=d["version"],
            representatives=dict(d["representatives"]),
            entities=[Entity(**e) for e in d["entities"]],
            steps=[Step.from_dict(s) for s in d["steps"]],
            conflicts=[Conflict.from_dict(c) for c in d["conflicts"]],
        )

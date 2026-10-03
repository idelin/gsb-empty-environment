"""Evidence domain model.

Facts, rules and steps all carry a stable ID and a version.  An
EvidenceRecord is self-contained: it embeds the exact (id, version)
facts and rules its steps reference, so any verifier can recompute the
record digest without external data.  Records are replayable: the
canonical encoding is deterministic JSON (sorted keys, tight
separators), so the same record always yields the same digest.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field

from .hashing import record_digest as _record_digest


def _canonical(obj: object) -> bytes:
    return json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=True
    ).encode("utf-8")


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    statement: str

    def to_obj(self) -> dict:
        return {"id": self.fact_id, "version": self.version,
                "statement": self.statement}


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    text: str

    def to_obj(self) -> dict:
        return {"id": self.rule_id, "version": self.version, "text": self.text}


@dataclass(frozen=True)
class Step:
    step_id: str
    version: int
    action: str
    fact_refs: tuple[tuple[str, int], ...] = ()
    rule_refs: tuple[tuple[str, int], ...] = ()

    def to_obj(self) -> dict:
        return {
            "id": self.step_id,
            "version": self.version,
            "action": self.action,
            "fact_refs": [list(ref) for ref in self.fact_refs],
            "rule_refs": [list(ref) for ref in self.rule_refs],
        }


@dataclass(frozen=True)
class EvidenceRecord:
    record_id: str
    version: int
    facts: tuple[Fact, ...] = ()
    rules: tuple[Rule, ...] = ()
    steps: tuple[Step, ...] = ()

    def to_obj(self) -> dict:
        return {
            "id": self.record_id,
            "version": self.version,
            "facts": [f.to_obj() for f in self.facts],
            "rules": [r.to_obj() for r in self.rules],
            "steps": [s.to_obj() for s in self.steps],
        }

    def canonical(self) -> bytes:
        return _canonical(self.to_obj())

    def digest(self) -> bytes:
        return _record_digest(self.canonical())

    def validate(self) -> None:
        """Check that every step reference resolves to an embedded
        fact/rule with the exact (id, version) pair."""
        facts = {(f.fact_id, f.version) for f in self.facts}
        rules = {(r.rule_id, r.version) for r in self.rules}
        for step in self.steps:
            for ref in step.fact_refs:
                if tuple(ref) not in facts:
                    raise ValueError(
                        f"step {step.step_id} references missing fact {ref}")
            for ref in step.rule_refs:
                if tuple(ref) not in rules:
                    raise ValueError(
                        f"step {step.step_id} references missing rule {ref}")


@dataclass(frozen=True)
class EvidenceLog:
    """An ordered, append-only sequence of evidence records."""

    log_id: str
    log_version: str
    records: tuple[EvidenceRecord, ...] = field(default_factory=tuple)

    def digests(self) -> list[bytes]:
        return [rec.digest() for rec in self.records]

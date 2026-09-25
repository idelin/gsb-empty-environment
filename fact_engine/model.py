"""Core data model: facts, rules, steps, decisions.

Everything carries a stable id and a version so evidence bundles are
traceable and replayable. All ordering is derived from stable keys,
never from input order.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum

ENGINE_ID = "fact-engine"
ENGINE_VERSION = "1.0.0"


class TruthValue(Enum):
    TRUE = "true"
    FALSE = "false"
    UNDECIDED = "undecided"


class SuppressionReason(Enum):
    SUPERSEDED = "superseded-by-newer-version"
    NOT_YET_EFFECTIVE = "not-yet-effective"
    EXPIRED = "expired"
    OUTRANKED = "outranked-by-higher-level"


def _dt_to_str(value: datetime | None) -> str | None:
    return value.isoformat() if value is not None else None


def _dt_from_str(value: str | None) -> datetime | None:
    return datetime.fromisoformat(value) if value is not None else None


@dataclass(frozen=True)
class Fact:
    """A claim assertion from one source, valid over a half-open interval."""

    fact_id: str
    version: int
    claim: str
    value: bool
    source_level: int  # higher means more reliable
    valid_from: datetime
    valid_to: datetime | None  # None means open-ended
    source: str = ""

    def ref(self) -> str:
        return f"{self.fact_id}@v{self.version}"

    def effective_at(self, moment: datetime) -> bool:
        if moment < self.valid_from:
            return False
        return self.valid_to is None or moment < self.valid_to

    def to_dict(self) -> dict:
        return {
            "fact_id": self.fact_id,
            "version": self.version,
            "claim": self.claim,
            "value": self.value,
            "source_level": self.source_level,
            "valid_from": _dt_to_str(self.valid_from),
            "valid_to": _dt_to_str(self.valid_to),
            "source": self.source,
        }

    @staticmethod
    def from_dict(data: dict) -> "Fact":
        return Fact(
            fact_id=data["fact_id"],
            version=data["version"],
            claim=data["claim"],
            value=data["value"],
            source_level=data["source_level"],
            valid_from=datetime.fromisoformat(data["valid_from"]),
            valid_to=_dt_from_str(data["valid_to"]),
            source=data.get("source", ""),
        )


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    description: str

    def to_dict(self) -> dict:
        return {
            "rule_id": self.rule_id,
            "version": self.version,
            "description": self.description,
        }

    @staticmethod
    def from_dict(data: dict) -> "Rule":
        return Rule(
            rule_id=data["rule_id"],
            version=data["version"],
            description=data["description"],
        )


DEFAULT_RULES: tuple[Rule, ...] = (
    Rule(
        "rule-version-supersede",
        1,
        "For a given fact id only the highest version is current; "
        "older versions are suppressed.",
    ),
    Rule(
        "rule-validity-window",
        1,
        "A fact is effective only inside [valid_from, valid_to); "
        "outside it is suppressed as not-yet-effective or expired.",
    ),
    Rule(
        "rule-prefer-high-level",
        1,
        "Among effective facts only the highest source_level may decide; "
        "lower levels are suppressed as outranked.",
    ),
    Rule(
        "rule-same-level-conflict-undecided",
        1,
        "If top-level facts assert both true and false the claim stays "
        "UNDECIDED; no side is picked arbitrarily.",
    ),
)


@dataclass(frozen=True)
class Step:
    """One deterministic resolution step, tagged with the rule applied."""

    step_id: str
    version: int
    rule_id: str
    rule_version: int
    summary: str
    kept: tuple[str, ...] = ()
    dropped: tuple[str, ...] = ()

    def to_dict(self) -> dict:
        return {
            "step_id": self.step_id,
            "version": self.version,
            "rule_id": self.rule_id,
            "rule_version": self.rule_version,
            "summary": self.summary,
            "kept": list(self.kept),
            "dropped": list(self.dropped),
        }


@dataclass(frozen=True)
class SuppressedEvidence:
    fact_ref: str
    reason: SuppressionReason
    detail: str

    def to_dict(self) -> dict:
        return {
            "fact_ref": self.fact_ref,
            "reason": self.reason.value,
            "detail": self.detail,
        }


@dataclass
class Decision:
    """The outcome of resolving one claim at one moment."""

    claim: str
    at: datetime
    value: TruthValue
    winners: list[str] = field(default_factory=list)
    contesting: list[str] = field(default_factory=list)
    suppressed: list[SuppressedEvidence] = field(default_factory=list)
    steps: list[Step] = field(default_factory=list)
    engine_id: str = ENGINE_ID
    engine_version: str = ENGINE_VERSION

    def canonical(self) -> dict:
        return {
            "engine_id": self.engine_id,
            "engine_version": self.engine_version,
            "claim": self.claim,
            "at": self.at.isoformat(),
            "value": self.value.value,
            "winners": sorted(self.winners),
            "contesting": sorted(self.contesting),
            "suppressed": sorted(
                (s.to_dict() for s in self.suppressed),
                key=lambda s: (s["fact_ref"], s["reason"]),
            ),
            "steps": [s.to_dict() for s in self.steps],
        }

    def digest(self) -> str:
        blob = json.dumps(self.canonical(), sort_keys=True, separators=(",", ":"))
        return hashlib.sha256(blob.encode("utf-8")).hexdigest()

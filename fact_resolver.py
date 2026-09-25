"""Deterministic fact arbitration with versioned evidence, rules, and steps."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from enum import IntEnum
import hashlib
import json
from typing import Any, Iterable


SCHEMA_VERSION = "1.0.0"
DIGEST_ALGORITHM = "sha256"


class ReliabilityLevel(IntEnum):
    """Higher numeric values represent more reliable sources."""

    LEVEL_1 = 1
    LEVEL_2 = 2
    LEVEL_3 = 3


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: str
    description: str


@dataclass(frozen=True)
class StepSpec:
    step_id: str
    version: str
    rule_id: str
    description: str


RULES = {
    "R-EVIDENCE-STABILITY": Rule(
        "R-EVIDENCE-STABILITY",
        "1.0.0",
        "Every evidence and fact version is immutable and addressed by stable ID and version.",
    ),
    "R-VALIDITY-WINDOW": Rule(
        "R-VALIDITY-WINDOW",
        "1.0.0",
        "Only facts whose half-open valid_from <= query_at < valid_until can decide a claim.",
    ),
    "R-RELIABILITY-PRIORITY": Rule(
        "R-RELIABILITY-PRIORITY",
        "1.0.0",
        "The highest active reliability tier selects the value for a claim.",
    ),
    "R-PEER-CONFLICT": Rule(
        "R-PEER-CONFLICT",
        "1.0.0",
        "Conflicting facts at the highest active tier leave the claim undecided.",
    ),
    "R-SUPPRESSED-EVIDENCE": Rule(
        "R-SUPPRESSED-EVIDENCE",
        "1.0.0",
        "Every non-selected evidence record is reported with a machine-readable reason.",
    ),
    "R-DETERMINISTIC-REPLAY": Rule(
        "R-DETERMINISTIC-REPLAY",
        "1.0.0",
        "Input is canonicalized before resolution, so order cannot affect the result.",
    ),
}

STEPS = {
    "S-VALIDATE": StepSpec(
        "S-VALIDATE",
        "1.0.0",
        "R-EVIDENCE-STABILITY",
        "Validate stable IDs, versions, timestamps, and duplicate canonical records.",
    ),
    "S-CANONICALIZE": StepSpec(
        "S-CANONICALIZE",
        "1.0.0",
        "R-DETERMINISTIC-REPLAY",
        "Sort facts and encode the complete replayable operation canonically.",
    ),
    "S-FILTER-WINDOW": StepSpec(
        "S-FILTER-WINDOW",
        "1.0.0",
        "R-VALIDITY-WINDOW",
        "Classify facts as active, expired, or not-yet-effective.",
    ),
    "S-GROUP-CLAIMS": StepSpec(
        "S-GROUP-CLAIMS",
        "1.0.0",
        "R-EVIDENCE-STABILITY",
        "Group active facts by stable claim ID.",
    ),
    "S-APPLY-PRIORITY": StepSpec(
        "S-APPLY-PRIORITY",
        "1.0.0",
        "R-RELIABILITY-PRIORITY",
        "Select the highest active reliability tier independently for each claim.",
    ),
    "S-RESOLVE-CONFLICTS": StepSpec(
        "S-RESOLVE-CONFLICTS",
        "1.0.0",
        "R-PEER-CONFLICT",
        "Preserve undecided results for same-tier true/false conflicts.",
    ),
    "S-EXPLAIN-SUPPRESSION": StepSpec(
        "S-EXPLAIN-SUPPRESSION",
        "1.0.0",
        "R-SUPPRESSED-EVIDENCE",
        "Attach reasons to all suppressed or inactive evidence records.",
    ),
}

STEP_ORDER = (
    "S-VALIDATE",
    "S-CANONICALIZE",
    "S-FILTER-WINDOW",
    "S-GROUP-CLAIMS",
    "S-APPLY-PRIORITY",
    "S-RESOLVE-CONFLICTS",
    "S-EXPLAIN-SUPPRESSION",
)


@dataclass(frozen=True)
class Evidence:
    evidence_id: str
    version: str
    source: str
    reliability: ReliabilityLevel
    synthetic: bool

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "Evidence":
        return cls(
            evidence_id=_required_text(data, "evidence_id"),
            version=_required_text(data, "version"),
            source=_required_text(data, "source"),
            reliability=_reliability(data.get("reliability")),
            synthetic=bool(data.get("synthetic", False)),
        )

    def canonical(self) -> dict[str, Any]:
        return {
            "evidence_id": self.evidence_id,
            "version": self.version,
            "source": self.source,
            "reliability": int(self.reliability),
            "synthetic": self.synthetic,
        }


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: str
    claim_id: str
    value: bool
    evidence: Evidence
    valid_from: datetime
    valid_until: datetime

    @property
    def reliability(self) -> ReliabilityLevel:
        return self.evidence.reliability

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "Fact":
        evidence_data = data.get("evidence")
        if not isinstance(evidence_data, dict):
            raise ValueError("fact.evidence must be an object")
        valid_from = _parse_time(data.get("valid_from"), "valid_from")
        valid_until = _parse_time(data.get("valid_until"), "valid_until")
        if valid_from >= valid_until:
            raise ValueError("valid_from must be earlier than valid_until")
        value = data.get("value")
        if not isinstance(value, bool):
            raise ValueError("fact value must be boolean true or false")
        return cls(
            fact_id=_required_text(data, "fact_id"),
            version=_required_text(data, "version"),
            claim_id=_required_text(data, "claim_id"),
            value=value,
            evidence=Evidence.from_dict(evidence_data),
            valid_from=valid_from,
            valid_until=valid_until,
        )

    def canonical(self) -> dict[str, Any]:
        return {
            "fact_id": self.fact_id,
            "version": self.version,
            "claim_id": self.claim_id,
            "value": self.value,
            "evidence": self.evidence.canonical(),
            "valid_from": _format_time(self.valid_from),
            "valid_until": _format_time(self.valid_until),
        }


@dataclass(frozen=True)
class ClaimResolution:
    claim_id: str
    value: bool | None
    status: str
    winning_tier: int | None
    evidence: list[dict[str, Any]]
    conflicting_evidence: list[dict[str, Any]]
    reasons: list[str]

    def canonical(self) -> dict[str, Any]:
        return {
            "claim_id": self.claim_id,
            "value": self.value,
            "status": self.status,
            "winning_tier": self.winning_tier,
            "evidence": self.evidence,
            "conflicting_evidence": self.conflicting_evidence,
            "reasons": self.reasons,
        }


@dataclass(frozen=True)
class ResolutionResult:
    query_at: datetime
    claims: list[ClaimResolution]
    suppressed: list[dict[str, Any]]
    inactive: list[dict[str, Any]]
    selected: list[dict[str, Any]]
    steps: list[dict[str, Any]]
    replay: dict[str, Any]
    synthetic_data: bool
    totals: dict[str, int]

    def canonical(self) -> dict[str, Any]:
        return {
            "schema_version": SCHEMA_VERSION,
            "query_at": _format_time(self.query_at),
            "claims": [claim.canonical() for claim in self.claims],
            "suppressed": self.suppressed,
            "inactive": self.inactive,
            "selected": self.selected,
            "steps": self.steps,
            "replay": self.replay,
            "synthetic_data": self.synthetic_data,
            "totals": self.totals,
        }


def resolve_facts(facts: Iterable[Fact | dict[str, Any]], query_at: datetime | str) -> ResolutionResult:
    """Resolve claims and return every selected, suppressed, and inactive fact."""

    query_time = _parse_time(query_at, "query_at")
    parsed_facts = [fact if isinstance(fact, Fact) else Fact.from_dict(fact) for fact in facts]

    validate_details = _validate_facts(parsed_facts)
    ordered_facts = sorted(parsed_facts, key=lambda fact: (fact.fact_id, fact.version))

    rule_signatures = _rule_signatures()
    step_signatures = _step_signatures()
    operation = {
        "schema_version": SCHEMA_VERSION,
        "digest_algorithm": DIGEST_ALGORITHM,
        "query_at": _format_time(query_time),
        "facts": [fact.canonical() for fact in ordered_facts],
        "rules": rule_signatures,
        "steps": step_signatures,
    }
    replay_digest = _sha256_json(operation)

    active: dict[str, list[Fact]] = {}
    inactive: list[dict[str, Any]] = []
    for fact in ordered_facts:
        if query_time < fact.valid_from:
            inactive.append(_evidence_ref(fact, "inactive", "NOT_YET_EFFECTIVE"))
        elif query_time >= fact.valid_until:
            inactive.append(_evidence_ref(fact, "inactive", "EXPIRED"))
        else:
            active.setdefault(fact.claim_id, []).append(fact)

    claims: list[ClaimResolution] = []
    selected: list[dict[str, Any]] = []
    suppressed: list[dict[str, Any]] = []

    for claim_id in sorted(active):
        claim_facts = sorted(
            active[claim_id],
            key=lambda fact: (
                -int(fact.reliability),
                fact.fact_id,
                fact.version,
                fact.evidence.evidence_id,
                fact.evidence.version,
            ),
        )
        top_tier = max(int(fact.reliability) for fact in claim_facts)
        top_facts = [fact for fact in claim_facts if int(fact.reliability) == top_tier]
        values = {fact.value for fact in top_facts}

        if len(values) == 1:
            value = next(iter(values))
            supporting = [fact for fact in top_facts if fact.value == value]
            winning = {
                (fact.fact_id, fact.version, fact.evidence.evidence_id, fact.evidence.version)
                for fact in supporting
            }
            for fact in claim_facts:
                identity = (
                    fact.fact_id,
                    fact.version,
                    fact.evidence.evidence_id,
                    fact.evidence.version,
                )
                if identity in winning:
                    selected.append(_evidence_ref(fact, "selected", "SELECTED_BY_HIGHEST_TIER"))
                else:
                    suppressed.append(_evidence_ref(fact, "suppressed", "LOWER_TIER_NOT_PREFERRED"))
            claims.append(
                ClaimResolution(
                    claim_id=claim_id,
                    value=value,
                    status="decided",
                    winning_tier=top_tier,
                    evidence=[_brief_ref(fact) for fact in supporting],
                    conflicting_evidence=[],
                    reasons=["SELECTED_BY_HIGHEST_TIER"],
                )
            )
        else:
            true_facts = sorted(fact for fact in top_facts if fact.value)
            false_facts = sorted(fact for fact in top_facts if not fact.value)
            for fact in claim_facts:
                if int(fact.reliability) == top_tier:
                    suppressed.append(_evidence_ref(fact, "suppressed", "PEER_TIER_CONFLICT_UNRESOLVED"))
                else:
                    suppressed.append(
                        _evidence_ref(fact, "suppressed", "BLOCKED_BY_UNRESOLVED_PEER_CONFLICT")
                    )
            claims.append(
                ClaimResolution(
                    claim_id=claim_id,
                    value=None,
                    status="undecided",
                    winning_tier=top_tier,
                    evidence=[],
                    conflicting_evidence=[_brief_ref(fact) for fact in true_facts + false_facts],
                    reasons=["PEER_TIER_CONFLICT_UNRESOLVED"],
                )
            )

    steps = _build_steps(
        validate_details=validate_details,
        ordered_count=len(ordered_facts),
        replay_digest=replay_digest,
        active=active,
        inactive=inactive,
        claims=claims,
        selected=selected,
        suppressed=suppressed,
    )

    replay = {
        "digest": replay_digest,
        "algorithm": DIGEST_ALGORITHM,
        "schema_version": SCHEMA_VERSION,
        "canonical_operation": operation,
        "note": "Recompute sha256 over canonical JSON of canonical_operation to verify replay.",
    }
    return ResolutionResult(
        query_at=query_time,
        claims=claims,
        suppressed=suppressed,
        inactive=inactive,
        selected=selected,
        steps=steps,
        replay=replay,
        synthetic_data=bool(ordered_facts) and all(fact.evidence.synthetic for fact in ordered_facts),
        totals={
            "facts": len(ordered_facts),
            "claims": len(claims),
            "selected": len(selected),
            "suppressed": len(suppressed),
            "inactive": len(inactive),
        },
    )


def replay(canonical_operation: dict[str, Any]) -> dict[str, Any]:
    """Recompute a digest from a persisted canonical operation and check rule metadata."""

    expected_rules = _rule_signatures()
    expected_steps = _step_signatures()
    if canonical_operation.get("rules") != expected_rules:
        raise ValueError("rule IDs or versions do not match replayable engine rules")
    if canonical_operation.get("steps") != expected_steps:
        raise ValueError("step IDs or versions do not match replayable engine steps")
    digest = _sha256_json(canonical_operation)
    query_at = _parse_time(canonical_operation["query_at"], "query_at")
    facts = [Fact.from_dict(fact) for fact in canonical_operation.get("facts", [])]
    result = resolve_facts(facts, query_at)
    return {"digest": digest, "matches": digest == result.replay["digest"], "result": result.canonical()}


def _validate_facts(facts: list[Fact]) -> dict[str, Any]:
    evidence_identities: set[tuple[str, str]] = set()
    fact_identities: set[tuple[str, str]] = set()
    canonical_identities: set[str] = set()

    for fact in facts:
        evidence_identity = (fact.evidence.evidence_id, fact.evidence.version)
        if evidence_identity in evidence_identities:
            for existing in facts:
                existing_identity = (
                    existing.evidence.evidence_id,
                    existing.evidence.version,
                )
                if existing_identity == evidence_identity and existing.evidence != fact.evidence:
                    raise ValueError(
                        f"evidence {fact.evidence.evidence_id}@{fact.evidence.version} "
                        "has inconsistent immutable metadata"
                    )
        evidence_identities.add(evidence_identity)

        fact_identity = (fact.fact_id, fact.version)
        if fact_identity in fact_identities:
            raise ValueError(f"duplicate fact {fact.fact_id}@{fact.version}")
        fact_identities.add(fact_identity)

        canonical_identity = _sha256_json(fact.canonical())
        if canonical_identity in canonical_identities:
            raise ValueError("duplicate canonical fact record")
        canonical_identities.add(canonical_identity)

    return {
        "facts_checked": len(facts),
        "fact_versions": len(fact_identities),
        "evidence_versions": len(evidence_identities),
        "duplicate_canonical_records": 0,
    }


def _build_steps(
    validate_details: dict[str, Any],
    ordered_count: int,
    replay_digest: str,
    active: dict[str, list[Fact]],
    inactive: list[dict[str, Any]],
    claims: list[ClaimResolution],
    selected: list[dict[str, Any]],
    suppressed: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    details_by_step = {
        "S-VALIDATE": validate_details,
        "S-CANONICALIZE": {
            "ordered_facts": ordered_count,
            "ordering": ["fact_id", "version"],
            "replay_digest": replay_digest,
        },
        "S-FILTER-WINDOW": {
            "active_facts": sum(len(group) for group in active.values()),
            "inactive_facts": len(inactive),
        },
        "S-GROUP-CLAIMS": {
            "claims": len(active),
            "claim_ids": sorted(active),
        },
        "S-APPLY-PRIORITY": {
            "decided": sum(claim.status == "decided" for claim in claims),
            "selected_evidence": len(selected),
            "suppressed_evidence": len(suppressed),
        },
        "S-RESOLVE-CONFLICTS": {
            "undecided": sum(claim.status == "undecided" for claim in claims),
            "policy": "same-tier true and false are never arbitrarily chosen",
        },
        "S-EXPLAIN-SUPPRESSION": {
            "suppressed_evidence": len(suppressed),
            "inactive_evidence": len(inactive),
            "all_non_selected_reported": True,
        },
    }
    return [
        {
            "step_id": step_id,
            "step_version": STEPS[step_id].version,
            "rule_id": STEPS[step_id].rule_id,
            "rule_version": RULES[STEPS[step_id].rule_id].version,
            "description": STEPS[step_id].description,
            "details": details_by_step[step_id],
        }
        for step_id in STEP_ORDER
    ]


def _evidence_ref(fact: Fact, state: str, reason_code: str) -> dict[str, Any]:
    return {
        "state": state,
        "reason_code": reason_code,
        "claim_id": fact.claim_id,
        "fact_id": fact.fact_id,
        "fact_version": fact.version,
        "value": fact.value,
        "reliability": int(fact.reliability),
        "evidence_id": fact.evidence.evidence_id,
        "evidence_version": fact.evidence.version,
        "source": fact.evidence.source,
        "synthetic": fact.evidence.synthetic,
        "valid_from": _format_time(fact.valid_from),
        "valid_until": _format_time(fact.valid_until),
    }


def _brief_ref(fact: Fact) -> dict[str, Any]:
    return {
        "fact_id": fact.fact_id,
        "fact_version": fact.version,
        "evidence_id": fact.evidence.evidence_id,
        "evidence_version": fact.evidence.version,
        "source": fact.evidence.source,
        "reliability": int(fact.reliability),
        "value": fact.value,
    }


def _rule_signatures() -> list[dict[str, str]]:
    return [
        {"rule_id": rule.rule_id, "version": rule.version}
        for rule in (RULES[rule_id] for rule_id in sorted(RULES))
    ]


def _step_signatures() -> list[dict[str, str]]:
    return [
        {"step_id": step_id, "version": STEPS[step_id].version}
        for step_id in STEP_ORDER
    ]


def _sha256_json(value: Any) -> str:
    encoded = json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
        allow_nan=False,
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def _required_text(data: dict[str, Any], key: str) -> str:
    value = data.get(key)
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{key} must be a non-empty string")
    return value


def _reliability(value: Any) -> ReliabilityLevel:
    if not isinstance(value, int) or isinstance(value, bool):
        raise ValueError("reliability must be integer 1, 2, or 3")
    try:
        return ReliabilityLevel(value)
    except ValueError as exc:
        raise ValueError("reliability must be integer 1, 2, or 3") from exc


def _parse_time(value: Any, field_name: str) -> datetime:
    if isinstance(value, datetime):
        parsed = value
    elif isinstance(value, str):
        try:
            parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        except ValueError as exc:
            raise ValueError(f"{field_name} must be ISO-8601 UTC timestamp") from exc
    else:
        raise ValueError(f"{field_name} must be ISO-8601 UTC timestamp")
    if parsed.tzinfo is None or parsed.utcoffset() != timedelta(0):
        raise ValueError(f"{field_name} must use UTC timezone")
    return parsed.astimezone(timezone.utc)


def _format_time(value: datetime) -> str:
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")

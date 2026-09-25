"""Replayable evidence bundles.

build_evidence() captures everything needed to reproduce a decision:
the input facts, the rule set, the query moment, the engine version
and the decision digest. replay() re-runs the engine from the bundle;
verify() checks the replayed decision matches the recorded one.
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime
from typing import Iterable, Sequence

from .engine import resolve
from .model import (
    ENGINE_ID,
    ENGINE_VERSION,
    DEFAULT_RULES,
    Decision,
    Fact,
    Rule,
)

BUNDLE_FORMAT = "fact-engine-evidence/1"


def _input_digest(claim: str, at: datetime, facts, rules) -> str:
    blob = json.dumps(
        {
            "claim": claim,
            "at": at.isoformat(),
            "facts": [f.to_dict() for f in facts],
            "rules": [r.to_dict() for r in rules],
        },
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()


def build_evidence(
    claim: str,
    facts: Iterable[Fact],
    at: datetime,
    rules: Sequence[Rule] = DEFAULT_RULES,
) -> dict:
    facts = sorted(facts, key=lambda f: (f.fact_id, f.version))
    rules = sorted(rules, key=lambda r: (r.rule_id, r.version))
    decision = resolve(claim, facts, at, rules)
    return {
        "format": BUNDLE_FORMAT,
        "engine": {"id": ENGINE_ID, "version": ENGINE_VERSION},
        "claim": claim,
        "at": at.isoformat(),
        "facts": [f.to_dict() for f in facts],
        "rules": [r.to_dict() for r in rules],
        "input_digest": _input_digest(claim, at, facts, rules),
        "decision": decision.canonical(),
        "digest": decision.digest(),
    }


def replay(evidence: dict) -> Decision:
    if evidence.get("format") != BUNDLE_FORMAT:
        raise ValueError(f"unsupported evidence format: {evidence.get('format')!r}")
    facts = [Fact.from_dict(d) for d in evidence["facts"]]
    rules = [Rule.from_dict(d) for d in evidence["rules"]]
    at = datetime.fromisoformat(evidence["at"])
    return resolve(evidence["claim"], facts, at, rules)


def verify(evidence: dict) -> bool:
    engine = evidence.get("engine", {})
    if engine.get("id") != ENGINE_ID or engine.get("version") != ENGINE_VERSION:
        return False
    facts = sorted(
        (Fact.from_dict(d) for d in evidence["facts"]),
        key=lambda f: (f.fact_id, f.version),
    )
    rules = sorted(
        (Rule.from_dict(d) for d in evidence["rules"]),
        key=lambda r: (r.rule_id, r.version),
    )
    at = datetime.fromisoformat(evidence["at"])
    if _input_digest(evidence["claim"], at, facts, rules) != evidence.get(
        "input_digest"
    ):
        return False
    decision = replay(evidence)
    return (
        decision.digest() == evidence.get("digest")
        and decision.canonical() == evidence.get("decision")
    )


def dumps(evidence: dict) -> str:
    return json.dumps(evidence, indent=2, sort_keys=True)


def loads(payload: str) -> dict:
    return json.loads(payload)

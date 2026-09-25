"""Deterministic replay of versioned steps.

A scenario is a list of step records, each with a stable ``step_id`` and
``version``.  Replaying the same records always yields the same result;
``digest`` gives a canonical SHA-256 fingerprint of the outcome.
"""
from __future__ import annotations

import hashlib
import json
from typing import Dict, Sequence

from .engine import evaluate
from .model import CategoryRequirement, Evidence, Rule
from .subsets import minimal_subset


def _evidence_from(record) -> Evidence:
    return Evidence(
        evidence_id=record["evidence_id"],
        version=record["version"],
        source_id=record["source_id"],
        category=record["category"],
        start=record["start"],
        end=record["end"],
        expires_at=record.get("expires_at"),
    )


def _rule_from(record) -> Rule:
    return Rule(
        rule_id=record["rule_id"],
        version=record["version"],
        requirements=tuple(
            CategoryRequirement(r["category"], r["min_sources"])
            for r in record["requirements"]
        ),
        exclusive_sources=record.get("exclusive_sources", True),
    )


def replay(steps: Sequence[dict]) -> dict:
    """Replay steps and return the full, JSON-serialisable outcome."""
    rules: Dict[str, Rule] = {}
    evidence: Dict[str, Evidence] = {}
    evaluations = []
    subset_reports = []
    for step in steps:
        op = step["op"]
        if op == "declare_rule":
            rule = _rule_from(step["rule"])
            rules[rule.rule_id] = rule
        elif op == "add_evidence":
            item = _evidence_from(step["evidence"])
            evidence[item.evidence_id] = item
        elif op == "retract_evidence":
            evidence.pop(step["evidence_id"], None)
        elif op == "evaluate":
            rule = rules[step["rule_id"]]
            result = evaluate(rule, list(evidence.values()), tuple(step["domain"]))
            evaluations.append(
                {
                    "step_id": step["step_id"],
                    "step_version": step["version"],
                    **result.as_dict(),
                }
            )
        elif op == "minimal_subset":
            rule = rules[step["rule_id"]]
            report = minimal_subset(
                rule,
                list(evidence.values()),
                tuple(step["target"]),
                budget=step.get("budget", 200_000),
            )
            subset_reports.append(
                {
                    "step_id": step["step_id"],
                    "step_version": step["version"],
                    **report.as_dict(),
                }
            )
        else:
            raise ValueError(f"unknown op: {op!r}")
    return {
        "rules": {
            rid: {
                "rule_id": rule.rule_id,
                "version": rule.version,
                "requirements": [
                    {"category": r.category, "min_sources": r.min_sources}
                    for r in rule.requirements
                ],
                "exclusive_sources": rule.exclusive_sources,
            }
            for rid, rule in sorted(rules.items())
        },
        "evidence_ids": sorted(evidence),
        "evaluations": evaluations,
        "subset_reports": subset_reports,
    }


def digest(outcome: dict) -> str:
    """Canonical SHA-256 fingerprint of a replay outcome."""
    blob = json.dumps(outcome, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(blob.encode("utf-8")).hexdigest()

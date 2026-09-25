"""Deterministic export of a proof plan as a self-contained package.

The package contains exactly the facts, rules and proof steps of the
pruned subgraph plus a verification summary (counts + SHA-256 over the
canonical bytes).  Serialisation is canonical JSON (sorted keys, tight
separators, UTF-8), so identical inputs always produce identical bytes.
"""

from __future__ import annotations

import copy
import hashlib
import json
from dataclasses import dataclass

from .model import KnowledgeBase
from .search import DEFAULT_BUDGET, Plan, SearchResult, solve

FORMAT = "proofpack/1"


def canonical_bytes(obj) -> bytes:
    """Stable byte representation of any JSON-able structure."""
    return json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode("utf-8")


def _content_hash(pkg: dict) -> str:
    unsigned = copy.deepcopy(pkg)
    unsigned["summary"].pop("sha256", None)
    return hashlib.sha256(canonical_bytes(unsigned)).hexdigest()


def _fill_summary(pkg: dict) -> None:
    facts = pkg["facts"]
    summary = pkg["summary"]
    summary["facts"] = len(facts)
    summary["base_facts"] = sum(1 for f in facts if f["role"] == "base")
    summary["public_base_facts"] = sum(
        1 for f in facts if f["role"] == "base" and f["public"]
    )
    summary["rules"] = len(pkg["rules"])
    summary["steps"] = len(pkg["steps"])
    summary["package_items"] = len(facts) + len(pkg["rules"]) + len(pkg["steps"])
    summary["sha256"] = _content_hash(pkg)


def resign(pkg: dict) -> dict:
    """Return a deep copy of ``pkg`` with a freshly computed summary.

    Used by the verifier's minimality check to build internally consistent
    mutant packages (so failures are semantic, not just hash mismatches).
    """
    mutant = copy.deepcopy(pkg)
    _fill_summary(mutant)
    return mutant


def _replay_steps(kb: KnowledgeBase, plan: Plan) -> list[dict]:
    """Deterministically order the plan's rules into proof steps."""
    available = set(plan.base_facts)
    pending = sorted(plan.rules)
    steps: list[dict] = []
    while pending:
        progressed = False
        for rule_id in list(pending):
            rule = kb.rules[rule_id]
            if all(p in available for p in rule.premises):
                steps.append(
                    {
                        "id": f"S{len(steps) + 1:03d}",
                        "version": 1,
                        "rule": rule.rule_id,
                        "rule_version": rule.version,
                        "inputs": sorted(rule.premises),
                        "output": rule.conclusion,
                    }
                )
                available.add(rule.conclusion)
                pending.remove(rule_id)
                progressed = True
        if not progressed:
            raise ValueError("plan does not derive its goals (cycle or gap)")
    return steps


def build_package(kb: KnowledgeBase, goals, plan: Plan) -> dict:
    """Assemble the export package for an optimal ``plan``."""

    goal_list = sorted(goals)
    steps = _replay_steps(kb, plan)
    derived_ids = sorted(kb.rules[r].conclusion for r in plan.rules)
    available = set(plan.base_facts) | set(derived_ids)
    missing = [g for g in goal_list if g not in available]
    if missing:
        raise ValueError(f"plan does not derive goals: {missing}")

    facts_section = []
    for fact_id in sorted(set(plan.base_facts) | set(derived_ids)):
        fact = kb.facts[fact_id]
        facts_section.append(
            {
                "id": fact.fact_id,
                "version": fact.version,
                "public": fact.public,
                "role": "base" if fact_id in plan.base_facts else "derived",
                "payload": fact.payload,
            }
        )
    rules_section = [
        {
            "id": rule.rule_id,
            "version": rule.version,
            "premises": sorted(rule.premises),
            "conclusion": rule.conclusion,
        }
        for rule in sorted(
            (kb.rules[r] for r in plan.rules), key=lambda r: r.rule_id
        )
    ]
    pkg = {
        "format": FORMAT,
        "goals": goal_list,
        "facts": facts_section,
        "rules": rules_section,
        "steps": steps,
        "summary": {},
    }
    _fill_summary(pkg)
    return pkg


@dataclass(frozen=True)
class ExportResult:
    search: SearchResult
    package: dict | None
    data: bytes | None


def export_package(kb: KnowledgeBase, goals, budget: int = DEFAULT_BUDGET) -> ExportResult:
    """Search for the optimal plan and, if found, export it deterministically."""

    result = solve(kb, goals, budget=budget)
    if result.status != "OPTIMAL":
        return ExportResult(result, None, None)
    pkg = build_package(kb, goals, result.plan)
    return ExportResult(result, pkg, canonical_bytes(pkg))

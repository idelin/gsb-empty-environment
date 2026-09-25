"""Deterministic export-package construction.

A package is a self-contained JSON document: the root goals, every fact,
rule and derivation step of the pruned subgraph, plus a verification
summary (counts, public-fact list, sha256 over the canonical body).

Canonical bytes: keys sorted, no whitespace, UTF-8.  The same knowledge
base and goals always produce byte-identical output, regardless of the
insertion order of facts/rules in the knowledge base.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass

from .search import DEFAULT_BUDGET, FACT, RULE, SearchResult, search

FORMAT = "proofpack/1"
BODY_KEYS = ("format", "goals", "facts", "rules", "steps")


def canonical_bytes(obj) -> bytes:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def step_id_for(rule_id: str) -> str:
    """Stable step id: one derivation step per rule application."""
    return f"step:{rule_id}"


def build_body(kb, goals, option: frozenset) -> dict:
    fact_ids = sorted(ident for kind, ident in option if kind == FACT)
    rule_ids = sorted(ident for kind, ident in option if kind == RULE)
    facts = [
        {
            "id": fact.id,
            "version": fact.version,
            "statement": fact.statement,
            "public": fact.public,
            "base": fact.base,
        }
        for fact in (kb.facts[i] for i in fact_ids)
    ]
    rules = [
        {
            "id": rule.id,
            "version": rule.version,
            "premises": list(rule.premises),
            "conclusion": rule.conclusion,
        }
        for rule in (kb.rules[i] for i in rule_ids)
    ]
    steps = [
        {
            "id": step_id_for(rule.id),
            "version": rule.version,
            "rule": rule.id,
            "inputs": list(rule.premises),
            "output": rule.conclusion,
        }
        for rule in (kb.rules[i] for i in rule_ids)
    ]
    return {
        "format": FORMAT,
        "goals": sorted(goals),
        "facts": facts,
        "rules": rules,
        "steps": steps,
    }


def summarize(body: dict) -> dict:
    return {
        "fact_count": len(body["facts"]),
        "rule_count": len(body["rules"]),
        "step_count": len(body["steps"]),
        "goal_count": len(body["goals"]),
        "public_facts": sorted(f["id"] for f in body["facts"] if f["public"]),
        "sha256": hashlib.sha256(canonical_bytes(body)).hexdigest(),
    }


def attach_summary(body: dict) -> dict:
    package = dict(body)
    package["summary"] = summarize(body)
    return package


class ExportError(RuntimeError):
    pass


@dataclass(frozen=True)
class Export:
    package: dict
    result: SearchResult

    def to_bytes(self) -> bytes:
        return canonical_bytes(self.package)


def build_export(kb, goals, budget: int = DEFAULT_BUDGET) -> Export:
    """Prune the dependency subgraph for ``goals`` and build the package.

    Works for both exact and undecided search results: an undecided result
    still carries a complete, valid proof (the upper-bound witness); only
    its optimality is unproven.  Raises ExportError if the budget was too
    small to find any complete proof.
    """
    result = search(kb, goals, budget)
    if result.option is None:
        raise ExportError(
            "no complete proof found within the enumeration budget; "
            "retry with a larger budget"
        )
    package = attach_summary(build_body(kb, goals, result.option))
    return Export(package, result)


def export_bytes(kb, goals, budget: int = DEFAULT_BUDGET) -> bytes:
    return build_export(kb, goals, budget).to_bytes()

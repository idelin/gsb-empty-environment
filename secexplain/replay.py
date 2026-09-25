"""Replayable evidence.

An evidence package contains everything a verifier needs to re-check a
projected explanation WITHOUT any hidden information: the projected
tree, the visible facts/rules/steps it references (stable ids and
versions), a schema version and a SHA-256 digest.  Replay is fully
deterministic: it recomputes the digest, recomputes every step id from
(rule id, rule version, output fact id, output fact version), recomputes
the structural counts from the tree, and checks that every reference
resolves inside the package.
"""
from __future__ import annotations

import hashlib
import json

from .engine import stable_step_id
from .projection import (PLACEHOLDER_TOKEN, Explanation, PFactNode,
                         PPlaceholder, PStepNode, to_canonical)

EVIDENCE_SCHEMA_VERSION = 1


def _canonical_json(obj) -> str:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"))


def build_evidence(explanation: Explanation, derivation) -> dict:
    facts: dict[str, dict] = {}
    rules: dict[str, dict] = {}
    steps: dict[str, dict] = {}

    def walk(node):
        if isinstance(node, PFactNode):
            facts[node.fact_id] = {
                "fact_id": node.fact_id, "version": node.version,
                "key": node.key, "value": node.value,
            }
            if isinstance(node.support, PStepNode):
                step = node.support
                rule = derivation.rules_by_id[step.rule_id]
                # Only the stable identity of the rule is pinned here.
                # The full rule text is distributed separately and may
                # reference hidden premise keys, so it is not copied.
                rules[rule.rule_id] = {
                    "rule_id": rule.rule_id, "version": rule.version,
                }
                steps[step.step_id] = {
                    "step_id": step.step_id, "version": step.version,
                    "rule_id": step.rule_id, "rule_version": step.rule_version,
                    "output": node.fact_id,
                    "visible_inputs": [c.fact_id for c in step.children
                                       if isinstance(c, PFactNode)],
                    "has_withheld_inputs": any(
                        isinstance(c, PPlaceholder) for c in step.children),
                }
                for child in step.children:
                    walk(child)

    walk(explanation.root)
    body = {
        "schema": EVIDENCE_SCHEMA_VERSION,
        "query": explanation.query,
        "clearance": explanation.clearance,
        "complete": explanation.complete,
        "summary": explanation.summary,
        "facts": [facts[k] for k in sorted(facts)],
        "rules": [rules[k] for k in sorted(rules)],
        "steps": [steps[k] for k in sorted(steps)],
        "tree": to_canonical(explanation.root),
    }
    digest = hashlib.sha256(_canonical_json(body).encode("utf-8")).hexdigest()
    return {**body, "digest": digest}


def replay(evidence: dict) -> bool:
    """Re-verify an evidence package; deterministic and self-contained."""
    if evidence.get("schema") != EVIDENCE_SCHEMA_VERSION:
        return False
    body = {k: v for k, v in evidence.items() if k != "digest"}
    want = hashlib.sha256(_canonical_json(body).encode("utf-8")).hexdigest()
    if want != evidence.get("digest"):
        return False

    facts = {f["fact_id"]: f for f in evidence["facts"]}
    rules = {r["rule_id"]: r for r in evidence["rules"]}
    steps = {s["step_id"]: s for s in evidence["steps"]}

    for step in evidence["steps"]:
        output = facts.get(step["output"])
        if output is None:
            return False
        recomputed = stable_step_id(step["rule_id"], step["rule_version"],
                                    output["fact_id"], output["version"])
        if recomputed != step["step_id"]:
            return False
        if step["rule_id"] not in rules:
            return False
        if any(in_id not in facts for in_id in step["visible_inputs"]):
            return False

    counts = {"visible_facts": 0, "visible_steps": 0, "placeholders": 0,
              "max_depth": 0}

    def walk(node, depth) -> bool:
        counts["max_depth"] = max(counts["max_depth"], depth)
        kind = node.get("type")
        if kind == "placeholder":
            counts["placeholders"] += 1
            return node.get("token") == PLACEHOLDER_TOKEN
        if kind == "fact":
            counts["visible_facts"] += 1
            if node["fact_id"] not in facts:
                return False
            support = node.get("support")
            return True if support is None else walk(support, depth + 1)
        if kind == "step":
            counts["visible_steps"] += 1
            if node["step_id"] not in steps:
                return False
            return all(walk(c, depth + 1) for c in node["children"])
        return False

    if not walk(evidence["tree"], 1):
        return False
    return counts == evidence["summary"]

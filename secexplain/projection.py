"""Visibility projection - the only supported way to publish a derivation.

Design invariants:

* Hidden facts/rules/steps are replaced by ONE constant placeholder.
  The placeholder carries no identity, no count and no depth.
* Any number of hidden premises of a visible step collapse into a single
  placeholder child, so the number of hidden premises is not leaked.
* A hidden support chain of any length collapses into a single
  placeholder edge, so hidden chain length is not leaked via path depth.
* Structural counts in the summary are computed AFTER projection and
  therefore only ever describe visible structure.
* Public errors use one constant message, so "does not exist" and
  "exists but is hidden" are indistinguishable.
* Debug traces are projected with the same rules.

Intentional disclosure (single bit): ``complete`` is False whenever any
content was withheld, so callers know the explanation is incomplete.

Out of scope (explicitly NOT promised): timing side channels.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass

from .engine import Derivation

PLACEHOLDER_TOKEN = "[RESTRICTED]"
PUBLIC_ERROR = "no explanation available at this clearance"


class ExplanationError(Exception):
    """Public-safe error; the message never depends on hidden state."""


@dataclass(frozen=True)
class PPlaceholder:
    token: str = PLACEHOLDER_TOKEN


@dataclass(frozen=True)
class PStepNode:
    step_id: str
    version: int
    rule_id: str
    rule_version: int
    children: tuple  # PNodes; at most one PPlaceholder, kept last


@dataclass(frozen=True)
class PFactNode:
    fact_id: str
    version: int
    key: str
    value: str
    support: object  # None | PStepNode | PPlaceholder


PLACEHOLDER = PPlaceholder()


@dataclass
class Explanation:
    query: str
    clearance: int
    root: object
    complete: bool
    summary: dict
    digest: str

    def to_canonical(self) -> dict:
        return {
            "query": self.query,
            "clearance": self.clearance,
            "complete": self.complete,
            "summary": self.summary,
            "tree": to_canonical(self.root),
            "digest": self.digest,
        }

    def to_json(self) -> str:
        return _canonical_json(self.to_canonical())


def _canonical_json(obj) -> str:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"))


def to_canonical(node) -> dict:
    if isinstance(node, PPlaceholder):
        return {"type": "placeholder", "token": node.token}
    if isinstance(node, PFactNode):
        return {
            "type": "fact",
            "fact_id": node.fact_id,
            "version": node.version,
            "key": node.key,
            "value": node.value,
            "support": None if node.support is None else to_canonical(node.support),
        }
    if isinstance(node, PStepNode):
        return {
            "type": "step",
            "step_id": node.step_id,
            "version": node.version,
            "rule_id": node.rule_id,
            "rule_version": node.rule_version,
            "children": [to_canonical(c) for c in node.children],
        }
    raise TypeError(f"unknown node: {node!r}")


def project(derivation: Derivation, query_key: str, clearance: int) -> Explanation:
    fact = derivation.facts_by_key.get(query_key)
    if fact is None or fact.level > clearance:
        raise ExplanationError(PUBLIC_ERROR)
    root = _project_fact(fact, derivation, clearance)
    summary = _summarize(root)
    digest = hashlib.sha256(
        _canonical_json({"query": query_key, "clearance": clearance,
                         "tree": to_canonical(root)}).encode("utf-8")
    ).hexdigest()
    return Explanation(
        query=query_key,
        clearance=clearance,
        root=root,
        complete=not _has_placeholder(root),
        summary=summary,
        digest=digest,
    )


def _project_fact(fact, derivation: Derivation, clearance: int):
    if fact.level > clearance:
        return PLACEHOLDER
    step = derivation.producer.get(fact.key)
    if step is None:
        return PFactNode(fact.fact_id, fact.version, fact.key, fact.value, None)
    rule = derivation.rules_by_id[step.rule_id]
    if rule.level > clearance:
        # Visible fact whose support is hidden: one placeholder edge,
        # regardless of how long the hidden support chain really is.
        return PFactNode(fact.fact_id, fact.version, fact.key, fact.value,
                         PLACEHOLDER)
    children = []
    hidden_input_seen = False
    for in_id in step.inputs:
        child = _project_fact(derivation.facts_by_id[in_id], derivation, clearance)
        if isinstance(child, PPlaceholder):
            # All hidden premises collapse into ONE placeholder child.
            if not hidden_input_seen:
                hidden_input_seen = True
                children.append(PLACEHOLDER)
        else:
            children.append(child)
    pstep = PStepNode(step.step_id, step.version, step.rule_id,
                      step.rule_version, tuple(children))
    return PFactNode(fact.fact_id, fact.version, fact.key, fact.value, pstep)


def _has_placeholder(node) -> bool:
    if isinstance(node, PPlaceholder):
        return True
    if isinstance(node, PFactNode):
        return node.support is not None and _has_placeholder(node.support)
    if isinstance(node, PStepNode):
        return any(_has_placeholder(c) for c in node.children)
    return False


def _summarize(root) -> dict:
    counts = {"visible_facts": 0, "visible_steps": 0, "placeholders": 0,
              "max_depth": 0}

    def walk(node, depth):
        counts["max_depth"] = max(counts["max_depth"], depth)
        if isinstance(node, PPlaceholder):
            counts["placeholders"] += 1
        elif isinstance(node, PFactNode):
            counts["visible_facts"] += 1
            if node.support is not None:
                walk(node.support, depth + 1)
        elif isinstance(node, PStepNode):
            counts["visible_steps"] += 1
            for child in node.children:
                walk(child, depth + 1)

    walk(root, 1)
    return counts


def project_trace(derivation: Derivation, clearance: int) -> list[dict]:
    """Project the engine's debug trace with the same visibility rules.

    Events whose rule or output is hidden are dropped entirely; visible
    events list only visible input fact ids plus a constant boolean
    recording that some inputs were withheld.
    """
    projected = []
    for event in derivation.trace:
        rule = derivation.rules_by_id[event.rule_id]
        out_fact = derivation.facts_by_id[event.output]
        if rule.level > clearance or out_fact.level > clearance:
            continue
        visible_inputs = []
        withheld = False
        for in_id in event.inputs:
            if derivation.facts_by_id[in_id].level <= clearance:
                visible_inputs.append(in_id)
            else:
                withheld = True
        projected.append({
            "rule_id": event.rule_id,
            "step_id": event.step_id,
            "inputs": visible_inputs,
            "output": event.output,
            "withheld_inputs": withheld,
        })
    return projected

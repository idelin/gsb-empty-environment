"""Domain model: facts, rules and steps with stable IDs and versions.

Evidence must be replayable: ``replay()`` deterministically re-evaluates
rules against facts for every step and ``replay_digest()`` reduces the
result to a single digest that is pinned inside the manifest.
"""

from __future__ import annotations

import ast
from dataclasses import dataclass
from typing import Mapping, Sequence

from .hashing import canonical_bytes, sha256_hex

DOMAIN_FILES = ("facts.json", "rules.json", "steps.json")


class ReplayError(Exception):
    """Replay failed (dangling reference or unsupported expression)."""


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    payload: Mapping[str, object]

    def to_dict(self):
        return {"fact_id": self.fact_id, "version": self.version, "payload": dict(self.payload)}

    @classmethod
    def from_dict(cls, d):
        return cls(d["fact_id"], int(d["version"]), dict(d["payload"]))


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    fact_refs: tuple
    expression: str

    def to_dict(self):
        return {
            "rule_id": self.rule_id,
            "version": self.version,
            "fact_refs": list(self.fact_refs),
            "expression": self.expression,
        }

    @classmethod
    def from_dict(cls, d):
        return cls(d["rule_id"], int(d["version"]), tuple(d["fact_refs"]), d["expression"])


@dataclass(frozen=True)
class Step:
    step_id: str
    version: int
    rule_refs: tuple
    action: str

    def to_dict(self):
        return {
            "step_id": self.step_id,
            "version": self.version,
            "rule_refs": list(self.rule_refs),
            "action": self.action,
        }

    @classmethod
    def from_dict(cls, d):
        return cls(d["step_id"], int(d["version"]), tuple(d["rule_refs"]), d["action"])


_COMPARE_OPS = {
    ast.Gt: lambda a, b: a > b,
    ast.Lt: lambda a, b: a < b,
    ast.GtE: lambda a, b: a >= b,
    ast.LtE: lambda a, b: a <= b,
    ast.Eq: lambda a, b: a == b,
    ast.NotEq: lambda a, b: a != b,
}


def _eval_node(node, env):
    """Tiny whitelisted evaluator (no arbitrary code execution)."""
    if isinstance(node, ast.Expression):
        return _eval_node(node.body, env)
    if isinstance(node, ast.Constant):
        if isinstance(node.value, (int, float, bool, str)):
            return node.value
        raise ReplayError(f"constant not allowed: {node.value!r}")
    if isinstance(node, ast.Name):
        if node.id in env:
            return env[node.id]
        raise ReplayError(f"unknown name in expression: {node.id}")
    if isinstance(node, ast.Compare):
        left = _eval_node(node.left, env)
        for op, comparator in zip(node.ops, node.comparators):
            fn = _COMPARE_OPS.get(type(op))
            if fn is None:
                raise ReplayError(f"operator not allowed: {type(op).__name__}")
            right = _eval_node(comparator, env)
            if not fn(left, right):
                return False
            left = right
        return True
    if isinstance(node, ast.BoolOp):
        values = [bool(_eval_node(v, env)) for v in node.values]
        if isinstance(node.op, ast.And):
            return all(values)
        if isinstance(node.op, ast.Or):
            return any(values)
    raise ReplayError(f"unsupported expression node: {type(node).__name__}")


def evaluate_rule(rule: Rule, facts_by_id: Mapping[str, Fact]) -> bool:
    env = {}
    for ref in rule.fact_refs:
        fact = facts_by_id.get(ref)
        if fact is None:
            raise ReplayError(f"rule {rule.rule_id} references missing fact {ref}")
        env.update(fact.payload)
    return bool(_eval_node(ast.parse(rule.expression, mode="eval"), env))


def replay(facts: Sequence[Fact], rules: Sequence[Rule], steps: Sequence[Step]) -> list:
    """Deterministically re-execute every step; output order is stable."""
    facts_by_id = {f.fact_id: f for f in facts}
    rules_by_id = {r.rule_id: r for r in rules}
    results = []
    for step in sorted(steps, key=lambda s: s.step_id):
        rule_results = []
        for ref in step.rule_refs:
            rule = rules_by_id.get(ref)
            if rule is None:
                raise ReplayError(f"step {step.step_id} references missing rule {ref}")
            rule_results.append(
                {
                    "rule_id": rule.rule_id,
                    "rule_version": rule.version,
                    "passed": evaluate_rule(rule, facts_by_id),
                }
            )
        results.append(
            {
                "step_id": step.step_id,
                "step_version": step.version,
                "action": step.action,
                "rules": rule_results,
            }
        )
    return results


def replay_digest(facts, rules, steps) -> str:
    return sha256_hex(canonical_bytes(replay(facts, rules, steps)))


def facts_from_bytes(data: bytes) -> list:
    import json

    return [Fact.from_dict(d) for d in json.loads(data.decode("utf-8"))]


def rules_from_bytes(data: bytes) -> list:
    import json

    return [Rule.from_dict(d) for d in json.loads(data.decode("utf-8"))]


def steps_from_bytes(data: bytes) -> list:
    import json

    return [Step.from_dict(d) for d in json.loads(data.decode("utf-8"))]


def compute_replay_digest(file_bytes: Mapping[str, bytes]) -> str:
    """Replay the domain files of a package and return the digest.

    This is the function injected into the receiver and the export
    verifier so both recompute the very same evidence trail.
    """
    facts = facts_from_bytes(file_bytes["facts.json"])
    rules = rules_from_bytes(file_bytes["rules.json"])
    steps = steps_from_bytes(file_bytes["steps.json"])
    return replay_digest(facts, rules, steps)

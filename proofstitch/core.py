"""Core evidence model: facts, rules, and replayable derivation steps.

Everything carries a stable string ID plus an integer version. Digests are
computed over a canonical JSON serialization so evidence is reproducible
across processes and machines.
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any, Callable


def canonical_digest(obj: Any) -> str:
    blob = json.dumps(obj, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(blob).hexdigest()


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    payload: dict

    @property
    def digest(self) -> str:
        return canonical_digest(
            {"id": self.fact_id, "version": self.version, "payload": self.payload}
        )


RuleFn = Callable[[list[dict]], dict]


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    fn: RuleFn


class RuleRegistry:
    """Deterministic rules addressable by (rule_id, version) for replay."""

    def __init__(self) -> None:
        self._rules: dict[tuple[str, int], Rule] = {}

    def register(self, rule: Rule) -> None:
        key = (rule.rule_id, rule.version)
        if key in self._rules:
            raise ValueError(f"duplicate rule registration: {key}")
        self._rules[key] = rule

    def get(self, rule_id: str, version: int) -> Rule:
        try:
            return self._rules[(rule_id, version)]
        except KeyError:
            raise KeyError(f"unknown rule: {rule_id}@{version}") from None


@dataclass(frozen=True)
class Step:
    """One replayable derivation: rule(inputs) == output."""

    step_id: str
    version: int
    rule_id: str
    rule_version: int
    input_fact_ids: tuple[str, ...]
    output: Fact


class ReplayError(Exception):
    """Raised when a step fails to reproduce its claimed output."""


def replay_steps(
    registry: RuleRegistry, facts: list[Fact], steps: list[Step]
) -> dict[str, Fact]:
    """Replay derivation steps, returning all available facts by fact_id.

    Raises ReplayError if any step's claimed output digest does not match
    the digest of re-applying the referenced rule to the referenced inputs.
    """
    available = {f.fact_id: f for f in facts}
    for step in steps:
        rule = registry.get(step.rule_id, step.rule_version)
        missing = [fid for fid in step.input_fact_ids if fid not in available]
        if missing:
            raise ReplayError(f"step {step.step_id}: missing inputs {missing}")
        inputs = [available[fid] for fid in step.input_fact_ids]
        derived = Fact(
            step.output.fact_id,
            step.output.version,
            rule.fn([f.payload for f in inputs]),
        )
        if derived.digest != step.output.digest:
            raise ReplayError(
                f"step {step.step_id}: replay digest mismatch "
                f"(claimed {step.output.digest[:12]}..., got {derived.digest[:12]}...)"
            )
        available[derived.fact_id] = derived
    return available

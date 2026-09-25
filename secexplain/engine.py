"""Deterministic forward-chaining deriver.

Step ids are stable: they depend only on (rule id, rule version, output
fact id, output fact version) - never on the identities of the input
facts.  This keeps step ids recomputable from visible information alone
(required for evidence replay) and prevents hidden input identities from
influencing a visible step's id.
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass, field

from .model import Fact, Rule, Step

STEP_ID_VERSION = 1


def stable_step_id(rule_id: str, rule_version: int,
                   output_fact_id: str, output_fact_version: int) -> str:
    payload = f"step|{rule_id}|{rule_version}|{output_fact_id}|{output_fact_version}"
    return "step-" + hashlib.sha256(payload.encode("utf-8")).hexdigest()[:16]


@dataclass(frozen=True)
class FireEvent:
    rule_id: str
    step_id: str
    inputs: tuple[str, ...]
    output: str


@dataclass
class Derivation:
    facts_by_key: dict[str, Fact]
    facts_by_id: dict[str, Fact]
    steps: list[Step]
    producer: dict[str, Step]  # conclusion key -> step that derived it
    trace: list[FireEvent]
    rules_by_id: dict[str, Rule]


class Engine:
    """Forward-chaining over unique-key facts; deterministic rule order."""

    def __init__(self, rules):
        self.rules: tuple[Rule, ...] = tuple(sorted(rules, key=lambda r: r.rule_id))
        self.rules_by_id = {r.rule_id: r for r in self.rules}

    def derive(self, facts) -> Derivation:
        facts_by_key: dict[str, Fact] = {}
        for fact in sorted(facts, key=lambda f: f.fact_id):
            facts_by_key.setdefault(fact.key, fact)
        steps: list[Step] = []
        producer: dict[str, Step] = {}
        trace: list[FireEvent] = []
        changed = True
        while changed:
            changed = False
            for rule in self.rules:
                if rule.conclusion_key in facts_by_key:
                    continue
                if not all(p in facts_by_key for p in rule.premises):
                    continue
                inputs = tuple(facts_by_key[p].fact_id for p in rule.premises)
                output = Fact(
                    fact_id=f"derived:{rule.conclusion_key}",
                    version=1,
                    key=rule.conclusion_key,
                    value="derived",
                    level=rule.conclusion_level,
                )
                step = Step(
                    step_id=stable_step_id(rule.rule_id, rule.version,
                                           output.fact_id, output.version),
                    version=STEP_ID_VERSION,
                    rule_id=rule.rule_id,
                    rule_version=rule.version,
                    inputs=inputs,
                    output=output.fact_id,
                )
                facts_by_key[rule.conclusion_key] = output
                producer[rule.conclusion_key] = step
                steps.append(step)
                trace.append(FireEvent(rule.rule_id, step.step_id, inputs,
                                       output.fact_id))
                changed = True
        facts_by_id = {f.fact_id: f for f in facts_by_key.values()}
        return Derivation(facts_by_key, facts_by_id, steps, producer, trace,
                          self.rules_by_id)

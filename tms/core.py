"""Justification-based truth maintenance with replayable evidence.

Semantics
---------
* Facts are assumptions asserted by the caller. Each fact carries a stable
  fact_id and version; its proposition is the node it supports.
* Rules encode conjunctive premises: premises -> conclusion. Several
  rules may conclude the same node (alternative supports).
* A node is IN iff it belongs to the *least* set containing all asserted
  propositions and closed under the rules. Least-fixed-point semantics means
  a cycle of rules with no external fact support cannot sustain itself.
* The support set of an IN node is the minimal set of currently valid
  justifications: {"assumption"} if the node is asserted, plus one
  entry per rule whose premises are all IN. Duplicate facts are idempotent
  and never inflate support counts.
* Every mutation is recorded as a Step with a stable id and version plus
  the hash of the resulting state, so the whole history can be replayed
  and verified from scratch.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field

ASSUMPTION = "assumption"

OP_ASSERT_FACT = "assert_fact"
OP_RETRACT_FACT = "retract_fact"
OP_ADD_RULE = "add_rule"


@dataclass(frozen=True)
class Fact:
    fact_id: str
    version: int
    proposition: str


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    premises: frozenset
    conclusion: str

    def __init__(self, rule_id, version, premises, conclusion):
        object.__setattr__(self, "rule_id", rule_id)
        object.__setattr__(self, "version", version)
        object.__setattr__(self, "premises", frozenset(premises))
        object.__setattr__(self, "conclusion", conclusion)


@dataclass(frozen=True)
class Step:
    step_id: str
    version: int
    op: str
    args: tuple
    noop: bool
    state_hash: str


@dataclass(frozen=True)
class ReplayReport:
    ok: bool
    steps_checked: int
    failed_step_id: str = ""
    expected_hash: str = ""
    actual_hash: str = ""


class Engine:
    """Maintains IN/OUT status and minimal support sets for all nodes."""

    def __init__(self):
        self.facts = {}
        self.rules = {}
        self.steps = []
        self._seq = 0
        self.in_nodes = frozenset()
        self.supports = {}
        self._recompute()

    # ------------------------------------------------------------------ API
    def add_rule(self, rule):
        existing = self.rules.get(rule.rule_id)
        noop = existing == rule
        if existing is not None and not noop:
            raise ValueError(
                "rule %r already registered with different content " % rule.rule_id
                + "(stable id %r, version %r)" % (rule.rule_id, rule.version)
            )
        if not noop:
            self.rules[rule.rule_id] = rule
            self._recompute()
        return self._record(OP_ADD_RULE, (rule,), noop)

    def assert_fact(self, fact):
        existing = self.facts.get(fact.fact_id)
        noop = existing == fact
        if existing is not None and not noop:
            raise ValueError(
                "fact %r already asserted with different content " % fact.fact_id
                + "(stable id %r, version %r)" % (fact.fact_id, fact.version)
            )
        if not noop:
            self.facts[fact.fact_id] = fact
            self._recompute()
        return self._record(OP_ASSERT_FACT, (fact,), noop)

    def retract_fact(self, fact_id):
        noop = fact_id not in self.facts
        if not noop:
            del self.facts[fact_id]
            self._recompute()
        return self._record(OP_RETRACT_FACT, (fact_id,), noop)

    def is_in(self, node):
        return node in self.in_nodes

    def support_of(self, node):
        """Minimal currently-valid support set for *node* (empty if OUT)."""
        return self.supports.get(node, frozenset())

    def support_count(self, node):
        return len(self.support_of(node))

    # -------------------------------------------------------------- replay
    def state_digest(self):
        payload = {
            "facts": sorted(
                (f.fact_id, f.version, f.proposition) for f in self.facts.values()
            ),
            "rules": sorted(
                (r.rule_id, r.version, sorted(r.premises), r.conclusion)
                for r in self.rules.values()
            ),
            "in_nodes": sorted(self.in_nodes),
            "supports": {n: sorted(s) for n, s in sorted(self.supports.items())},
        }
        blob = json.dumps(payload, sort_keys=True).encode("utf-8")
        return hashlib.sha256(blob).hexdigest()

    def replay(self):
        """Re-apply the recorded step log on a fresh engine and verify that
        every recorded post-state hash is reproduced exactly."""
        fresh = Engine()
        for step in self.steps:
            if step.op == OP_ADD_RULE:
                fresh.add_rule(*step.args)
            elif step.op == OP_ASSERT_FACT:
                fresh.assert_fact(*step.args)
            elif step.op == OP_RETRACT_FACT:
                fresh.retract_fact(*step.args)
            else:
                return ReplayReport(False, self.steps.index(step), step.step_id)
            actual = fresh.state_digest()
            if actual != step.state_hash:
                return ReplayReport(
                    False,
                    self.steps.index(step) + 1,
                    step.step_id,
                    step.state_hash,
                    actual,
                )
        return ReplayReport(True, len(self.steps))

    # ----------------------------------------------------------- internals
    def _record(self, op, args, noop):
        self._seq += 1
        step = Step(
            step_id="step-%04d" % self._seq,
            version=1,
            op=op,
            args=tuple(args),
            noop=noop,
            state_hash=self.state_digest(),
        )
        self.steps.append(step)
        return step

    def _recompute(self):
        assumed = {f.proposition for f in self.facts.values()}
        in_nodes = set(assumed)
        changed = True
        while changed:
            changed = False
            for rule in self.rules.values():
                if rule.conclusion not in in_nodes and rule.premises <= in_nodes:
                    in_nodes.add(rule.conclusion)
                    changed = True
        supports = {}
        for node in in_nodes:
            entries = set()
            if node in assumed:
                entries.add(ASSUMPTION)
            for rule in self.rules.values():
                if rule.conclusion == node and rule.premises <= in_nodes:
                    entries.add(rule.rule_id)
            supports[node] = frozenset(entries)
        self.in_nodes = frozenset(in_nodes)
        self.supports = supports

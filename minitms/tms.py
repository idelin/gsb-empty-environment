"""ATMS-style mini truth-maintenance engine.

Each believed atom carries a label of inclusion-minimal environments; an
environment is a frozenset of external fact IDs.  Conjunctive rules combine
one premise environment each via cartesian product/union.  Labels are
recomputed to a fixpoint after every mutation, so:

* multiple alternative rules give multiple independent supports;
* retracting a fact removes every environment containing it;
* a conclusion survives while at least one environment survives;
* a rule cycle with no external grounding produces no environment and
  therefore cannot establish any conclusion.
"""
from __future__ import annotations

import itertools
from typing import Dict, List, Optional, Tuple

from .model import Environment, Fact, Rule, Step, minimize_envs

EMPTY_ENV: Environment = frozenset()


def _env_key(env: Environment):
    return (len(env), tuple(sorted(env)))


class ReplayError(RuntimeError):
    pass


class TruthMaintenanceSystem:
    def __init__(self) -> None:
        self._facts: Dict[str, Fact] = {}
        self._rules: Dict[str, Rule] = {}
        self._steps: Dict[str, Step] = {}
        self._labels: Dict[str, frozenset] = {}
        self._witnesses: Dict[Environment, object] = {}
        self.events: List[dict] = []

    # ------------------------------------------------------------------ facts
    def assert_fact(self, fact_id: str, atom: str, record: bool = True) -> Fact:
        """Assert an external fact.  Idempotent for the same active fact ID."""
        existing = self._facts.get(fact_id)
        if existing is not None:
            if existing.atom != atom:
                raise ValueError(
                    f"fact {fact_id!r} already asserts {existing.atom!r}, "
                    f"cannot rebind it to {atom!r}"
                )
            if existing.active:
                return existing  # repeated assertion adds nothing
            fact = Fact(fact_id, atom, active=True, version=existing.version + 1)
        else:
            fact = Fact(fact_id, atom, active=True, version=1)
        self._facts[fact_id] = fact
        if record:
            self.events.append(
                {"op": "assert_fact", "id": fact_id, "atom": atom,
                 "version": fact.version}
            )
        self._recompute()
        return fact

    def retract_fact(self, fact_id: str, record: bool = True) -> Optional[Fact]:
        """Retract an external fact; no-op when it is already inactive."""
        existing = self._facts.get(fact_id)
        if existing is None:
            raise KeyError(f"unknown fact id: {fact_id!r}")
        if not existing.active:
            return existing
        fact = Fact(existing.id, existing.atom, active=False,
                    version=existing.version + 1)
        self._facts[fact_id] = fact
        if record:
            self.events.append(
                {"op": "retract_fact", "id": fact_id,
                 "version": fact.version}
            )
        self._recompute()
        return fact

    def facts(self) -> Dict[str, Fact]:
        return dict(self._facts)

    def fact(self, fact_id: str) -> Fact:
        return self._facts[fact_id]

    # ------------------------------------------------------------------ rules
    def add_rule(self, rule_id: str, premises, conclusion: str,
                 record: bool = True) -> Rule:
        if rule_id in self._rules:
            raise ValueError(f"rule id already exists: {rule_id!r}")
        rule = Rule(rule_id, tuple(premises), conclusion, version=1)
        self._rules[rule_id] = rule
        self._steps[rule_id] = Step(rule_id, conclusion,
                                    tuple(premises), active=False, version=0)
        if record:
            self.events.append(
                {"op": "add_rule", "id": rule_id,
                 "premises": list(premises), "conclusion": conclusion,
                 "version": 1}
            )
        self._recompute()
        return rule

    def rules(self) -> Dict[str, Rule]:
        return dict(self._rules)

    def steps(self) -> Dict[str, Step]:
        return dict(self._steps)

    def step(self, rule_id: str) -> Step:
        return self._steps[rule_id]

    # -------------------------------------------------------------- queries
    def believed(self, atom: str) -> bool:
        return bool(self._labels.get(atom))

    def supports(self, atom: str) -> List[frozenset]:
        """Minimal supporting environments, deterministically ordered."""
        return sorted(self._labels.get(atom, frozenset()), key=_env_key)

    def support_count(self, atom: str) -> int:
        """Number of minimal independent supports (set cardinality, not
        multiplicity — asserting the same fact ID again does not raise it)."""
        return len(self._labels.get(atom, frozenset()))

    def explain(self, atom: str) -> dict:
        envs = self.supports(atom)
        if not envs:
            return {"atom": atom, "believed": False, "supports": []}
        trees = [self._explain_env(atom, env, ()) for env in envs]
        return {"atom": atom, "believed": True,
                "supports": [
                    {"env": sorted(env), "tree": tree}
                    for env, tree in zip(envs, trees)
                ]}

    def _explain_env(self, atom: str, env: Environment, stack: Tuple) -> dict:
        witness = self._witnesses.get(atom, {}).get(env)
        node = {"atom": atom, "env": sorted(env)}
        key = (atom, env)
        if witness is None:
            node["kind"] = "premise"
            node["fact_ids"] = [fid for fid in sorted(env)
                                if self._facts.get(fid) and
                                self._facts[fid].atom == atom]
            return node
        rule_id, premise_envs = witness
        node["kind"] = "rule"
        node["rule_id"] = rule_id
        node["step_version"] = self._steps[rule_id].version
        if key in stack:
            node["cycle"] = True
            return node
        rule = self._rules[rule_id]
        node["premises"] = [
            self._explain_env(p, pe, stack + (key,))
            for p, pe in zip(rule.premises, premise_envs)
        ]
        return node

    # ------------------------------------------------------------- snapshot
    def snapshot(self) -> dict:
        return {
            "facts": [
                {"id": f.id, "atom": f.atom, "active": f.active,
                 "version": f.version}
                for f in sorted(self._facts.values(), key=lambda x: x.id)
            ],
            "rules": [
                {"id": r.id, "premises": list(r.premises),
                 "conclusion": r.conclusion, "version": r.version}
                for r in sorted(self._rules.values(), key=lambda x: x.id)
            ],
            "steps": [
                {"rule_id": s.rule_id, "active": s.active,
                 "version": s.version}
                for s in sorted(self._steps.values(), key=lambda x: x.rule_id)
            ],
            "labels": {
                atom: [sorted(env) for env in sorted(envs, key=_env_key)]
                for atom, envs in sorted(self._labels.items())
            },
        }

    # -------------------------------------------------------------- internals
    def _recompute(self) -> None:
        labels: Dict[str, frozenset] = {}

        def add_premise(atom: str, fact_id: str) -> None:
            env = frozenset({fact_id})
            labels[atom] = labels.get(atom, frozenset()) | {env}

        for fact in sorted(self._facts.values(), key=lambda f: f.id):
            if fact.active:
                add_premise(fact.atom, fact.id)

        ordered_rules = sorted(self._rules.values(), key=lambda r: r.id)

        # Fixpoint: labels only grow (up to the finite premise-ID lattice);
        # subsumed environments are pruned each round.
        while True:
            changed = False
            for rule in ordered_rules:
                missing = [p for p in rule.premises if p not in labels]
                if missing:
                    continue
                new_envs = set()
                for combo in itertools.product(
                        *(labels[p] for p in rule.premises)):
                    new_envs.add(frozenset().union(*combo))
                old = labels.get(rule.conclusion, frozenset())
                merged = minimize_envs(old | frozenset(new_envs))
                if merged != old:
                    labels[rule.conclusion] = merged
                    changed = True
            if not changed:
                break

        # Rebuild witnesses deterministically from the final labels so that
        # every recorded premise environment is itself in some final label.
        witnesses: Dict[str, Dict[Environment, object]] = {}
        for fact in sorted(self._facts.values(), key=lambda f: f.id):
            if fact.active:
                env = frozenset({fact.id})
                if env in labels.get(fact.atom, frozenset()):
                    witnesses.setdefault(fact.atom, {})[env] = None
        for rule in ordered_rules:
            if any(p not in labels for p in rule.premises):
                continue
            w = witnesses.setdefault(rule.conclusion, {})
            for combo in itertools.product(
                    *(sorted(labels[p], key=_env_key)
                      for p in rule.premises)):
                env = frozenset().union(*combo)
                if env in labels.get(rule.conclusion, frozenset()) \
                        and env not in w:
                    w[env] = (rule.id, tuple(combo))

        # Reconcile step activity/versions against the final labels.
        for rule in ordered_rules:
            active = self._rule_supports_minimal(rule, labels)
            prev = self._steps[rule.id]
            if active != prev.active:
                self._steps[rule.id] = Step(
                    prev.rule_id, prev.conclusion, prev.premises,
                    active=active, version=prev.version + 1)

        self._labels = labels
        self._witnesses = witnesses

    @staticmethod
    def _rule_supports_minimal(rule: Rule,
                               labels: Dict[str, frozenset]) -> bool:
        if any(p not in labels for p in rule.premises):
            return False
        premise_lists = [labels[p] for p in rule.premises]
        conclusion_label = labels.get(rule.conclusion, frozenset())
        for combo in itertools.product(*premise_lists):
            if frozenset().union(*combo) in conclusion_label:
                return True
        return False

    # ---------------------------------------------------------------- replay
    def to_evidence(self) -> dict:
        """Export the append-only event log (the replayable evidence)."""
        return {"version": 1, "events": [dict(e) for e in self.events]}

    @classmethod
    def from_evidence(cls, evidence: dict) -> "TruthMaintenanceSystem":
        if evidence.get("version") != 1:
            raise ReplayError("unsupported evidence version")
        tms = cls()
        for event in evidence["events"]:
            op = event["op"]
            if op == "assert_fact":
                tms.assert_fact(event["id"], event["atom"], record=True)
            elif op == "retract_fact":
                tms.retract_fact(event["id"], record=True)
            elif op == "add_rule":
                tms.add_rule(event["id"], event["premises"],
                             event["conclusion"], record=True)
            else:
                raise ReplayError(f"unknown event op: {op!r}")
        return tms

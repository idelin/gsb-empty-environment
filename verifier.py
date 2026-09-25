"""Independent verifier for support sets produced by supportdag.Engine.

The verifier deliberately does NOT reuse the engine's support computation.
It replays the derivation from scratch, restricted to the facts pinned in
the support, and checks:

- sufficiency: the goal is derivable using exactly the pinned facts (at
  their pinned versions) while every negated fact is currently absent;
- non-redundancy: every pinned fact is actually used, every negated
  condition is actually consulted, and removing any single fact makes the
  derivation fail;
- replayability: the derivation is emitted as a proof DAG of steps with
  stable content-hashed ids and versions; `replay_proof` re-validates a
  proof against the current evidence and detects any version drift.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field

from supportdag import FACT, NEGFACT, GOAL, Engine, Support

RULE = "rule"


# --------------------------------------------------------------------------
# Proof steps (stable id + version, content addressed)
# --------------------------------------------------------------------------

def _step_id(kind: str, ref: str, version: int, alt_id, children) -> str:
    payload = json.dumps(
        [kind, ref, version, alt_id, list(children)],
        separators=(",", ":"),
        sort_keys=True,
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()[:16]


@dataclass(frozen=True)
class Step:
    kind: str          # RULE | FACT | NEGFACT
    ref: str           # rule_id or fact_id
    version: int       # rule/fact version this step cites (0 for negation)
    alt_id: str | None
    children: tuple    # child step ids, aligned with the alternative's literals
    step_id: str

    def to_json(self) -> dict:
        return {
            "kind": self.kind,
            "ref": self.ref,
            "version": self.version,
            "alt_id": self.alt_id,
            "children": list(self.children),
            "step_id": self.step_id,
        }

    @staticmethod
    def from_json(data: dict) -> "Step":
        return Step(
            data["kind"], data["ref"], data["version"],
            data["alt_id"], tuple(data["children"]), data["step_id"],
        )


@dataclass
class Proof:
    goal: str
    root: str
    steps: dict  # step_id -> Step

    def to_json(self) -> dict:
        return {
            "goal": self.goal,
            "root": self.root,
            "steps": [s.to_json() for s in self.steps.values()],
        }

    @staticmethod
    def from_json(data: dict) -> "Proof":
        steps = {s["step_id"]: Step.from_json(s) for s in data["steps"]}
        return Proof(data["goal"], data["root"], steps)


def _make_step(kind, ref, version, alt_id, children) -> Step:
    return Step(kind, ref, version, alt_id, tuple(children),
                _step_id(kind, ref, version, alt_id, children))


# --------------------------------------------------------------------------
# Derivation replay (independent of the engine's own algorithm)
# --------------------------------------------------------------------------

class _Tracer:
    def __init__(self, engine: Engine, allowed: dict):
        self.engine = engine
        self.allowed = allowed      # fact_id -> pinned version
        self.used: set = set()      # fact ids actually consumed
        self.consulted_neg: set = set()
        self.stale: set = set()     # fact ids whose version drifted
        self.steps: dict = {}

    def _add(self, kind, ref, version, alt_id, children) -> str:
        step = _make_step(kind, ref, version, alt_id, children)
        self.steps[step.step_id] = step
        return step.step_id

    def derive(self, goal: str, visiting: frozenset):
        for rid in sorted(self.engine.rule_ids_for(goal)):
            rule = self.engine.rules[rid]
            for alt in rule.alts:
                sid = self._try_alt(rule, alt, visiting)
                if sid is not None:
                    return sid
        return None

    def _try_alt(self, rule, alt, visiting):
        children = []
        for lit in alt.lits:
            if lit.kind == FACT:
                pinned = self.allowed.get(lit.ref)
                if pinned is None:
                    return None
                cur = self.engine.facts.get(lit.ref)
                if cur is None or cur.version != pinned:
                    self.stale.add(lit.ref)
                    return None
                self.used.add(lit.ref)
                children.append(self._add(FACT, lit.ref, pinned, None, ()))
            elif lit.kind == NEGFACT:
                if lit.ref in self.engine.facts:
                    return None
                self.consulted_neg.add(lit.ref)
                children.append(self._add(NEGFACT, lit.ref, 0, None, ()))
            else:
                if lit.ref in visiting:
                    return None
                sub = self.derive(lit.ref, visiting | {lit.ref})
                if sub is None:
                    return None
                children.append(sub)
        return self._add(RULE, rule.rule_id, rule.version, alt.alt_id, tuple(children))


# --------------------------------------------------------------------------
# Verification reports
# --------------------------------------------------------------------------

@dataclass
class VerifyReport:
    ok: bool
    sufficient: bool
    minimal: bool
    stale: bool
    reasons: tuple
    proof: Proof | None


def verify_support(engine: Engine, goal: str, support: Support) -> VerifyReport:
    """Check that `support` is sufficient for `goal` and non-redundant."""
    allowed = {f.fact_id: f.version for f in support.pos}
    tracer = _Tracer(engine, allowed)
    root = tracer.derive(goal, frozenset({goal}))
    if root is None:
        reasons = ["goal is not derivable from the pinned facts"]
        if tracer.stale:
            reasons.append(
                "stale evidence for: " + ", ".join(sorted(tracer.stale))
            )
        return VerifyReport(False, False, False, bool(tracer.stale),
                            tuple(reasons), None)

    reasons = []
    minimal = True
    pos_ids = {f.fact_id for f in support.pos}
    unused = pos_ids - tracer.used
    if unused:
        minimal = False
        reasons.append("unused facts (redundant): " + ", ".join(sorted(unused)))
    if tracer.consulted_neg != set(support.neg):
        minimal = False
        reasons.append(
            "negated conditions mismatch: consulted "
            f"{sorted(tracer.consulted_neg)}, declared {sorted(support.neg)}"
        )
    for fact_id in sorted(pos_ids):
        reduced = {k: v for k, v in allowed.items() if k != fact_id}
        if _Tracer(engine, reduced).derive(goal, frozenset({goal})) is not None:
            minimal = False
            reasons.append(f"fact {fact_id} is not necessary")

    proof = Proof(goal, root, tracer.steps)
    return VerifyReport(minimal, True, minimal, False, tuple(reasons), proof)


def replay_proof(engine: Engine, proof: Proof) -> bool:
    """Re-validate a recorded proof against the current evidence base."""
    for step in proof.steps.values():
        if step.step_id != _step_id(step.kind, step.ref, step.version,
                                    step.alt_id, step.children):
            return False
        if step.kind == FACT:
            fact = engine.facts.get(step.ref)
            if fact is None or fact.version != step.version:
                return False
        elif step.kind == NEGFACT:
            if step.ref in engine.facts:
                return False
        elif step.kind == RULE:
            rule = engine.rules.get(step.ref)
            if rule is None or rule.version != step.version:
                return False
            alt = next((a for a in rule.alts if a.alt_id == step.alt_id), None)
            if alt is None or len(alt.lits) != len(step.children):
                return False
            for lit, child_id in zip(alt.lits, step.children):
                child = proof.steps.get(child_id)
                if child is None:
                    return False
                if lit.kind == FACT:
                    if child.kind != FACT or child.ref != lit.ref:
                        return False
                elif lit.kind == NEGFACT:
                    if child.kind != NEGFACT or child.ref != lit.ref:
                        return False
                else:
                    if child.kind != RULE:
                        return False
                    child_rule = engine.rules.get(child.ref)
                    if child_rule is None or child_rule.head != lit.ref:
                        return False
        else:
            return False
    root = proof.steps.get(proof.root)
    if root is None or root.kind != RULE:
        return False
    root_rule = engine.rules.get(root.ref)
    return root_rule is not None and root_rule.head == proof.goal

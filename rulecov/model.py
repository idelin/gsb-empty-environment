"""Typed rule-spec model.  Facts, rules and the spec itself all carry a
stable ``id`` and a monotonically increasing ``version``."""
from __future__ import annotations

from dataclasses import dataclass

from .regions import EnumSet, IntRange, constraint_to_json


class SpecError(ValueError):
    """Raised when a spec document fails validation."""


@dataclass(frozen=True)
class FactSpec:
    fact_id: str
    version: int
    kind: str  # "enum" | "int"
    values: tuple | None = None  # enum domain
    lo: int | None = None        # int domain lower bound
    hi: int | None = None        # int domain upper bound

    def domain(self):
        if self.kind == "enum":
            return EnumSet(self.values)
        return IntRange(self.lo, self.hi)

    def to_doc(self):
        doc = {"id": self.fact_id, "version": self.version, "kind": self.kind}
        if self.kind == "enum":
            doc["values"] = sorted(self.values)
        else:
            doc["lo"] = self.lo
            doc["hi"] = self.hi
        return doc


@dataclass(frozen=True)
class Rule:
    rule_id: str
    version: int
    priority: int
    action: str
    conditions: dict  # fact id -> constraint; missing fact = unconstrained

    def ref(self):
        return {"id": self.rule_id, "version": self.version}

    def to_doc(self):
        return {
            "id": self.rule_id,
            "version": self.version,
            "priority": self.priority,
            "action": self.action,
            "conditions": {
                k: constraint_to_json(self.conditions[k])
                for k in sorted(self.conditions)
            },
        }


@dataclass(frozen=True)
class Spec:
    spec_id: str
    version: int
    facts: tuple
    rules: tuple

    def fact_map(self):
        return {f.fact_id: f for f in self.facts}

    def rule_map(self):
        return {r.rule_id: r for r in self.rules}

    def domain(self):
        return {f.fact_id: f.domain() for f in self.facts}

    def to_doc(self):
        return {
            "spec_id": self.spec_id,
            "version": self.version,
            "facts": [f.to_doc() for f in self.facts],
            "rules": [r.to_doc() for r in self.rules],
        }


def _parse_constraint(fact, raw, ctx):
    if not isinstance(raw, dict) or len(raw) != 1:
        raise SpecError(f"{ctx}: constraint for fact {fact.fact_id!r} must be "
                        f"a single-key object ({{'enum': [...]}} or {{'int': [lo, hi]}})")
    if "enum" in raw:
        if fact.kind != "enum":
            raise SpecError(f"{ctx}: fact {fact.fact_id!r} is not an enum fact")
        values = raw["enum"]
        if not isinstance(values, list) or not values:
            raise SpecError(f"{ctx}: enum constraint for {fact.fact_id!r} must be a non-empty list")
        unknown = sorted(set(values) - set(fact.values))
        if unknown:
            raise SpecError(f"{ctx}: values {unknown} outside domain of {fact.fact_id!r}")
        return EnumSet(values)
    if "int" in raw:
        if fact.kind != "int":
            raise SpecError(f"{ctx}: fact {fact.fact_id!r} is not an int fact")
        bounds = raw["int"]
        if (not isinstance(bounds, list) or len(bounds) != 2
                or not all(isinstance(b, int) for b in bounds)):
            raise SpecError(f"{ctx}: int constraint for {fact.fact_id!r} must be [lo, hi]")
        lo, hi = bounds
        if lo > hi:
            raise SpecError(f"{ctx}: empty interval [{lo}, {hi}] for {fact.fact_id!r}")
        if lo < fact.lo or hi > fact.hi:
            raise SpecError(f"{ctx}: interval [{lo}, {hi}] outside domain "
                            f"[{fact.lo}, {fact.hi}] of {fact.fact_id!r}")
        return IntRange(lo, hi)
    raise SpecError(f"{ctx}: unknown constraint kind for fact {fact.fact_id!r}")


def load_spec(doc):
    """Parse and validate a spec document (already JSON-decoded)."""
    ctx = "spec"
    if not isinstance(doc, dict):
        raise SpecError("spec document must be an object")
    for key in ("spec_id", "version", "facts", "rules"):
        if key not in doc:
            raise SpecError(f"spec: missing required key {key!r}")

    facts = []
    seen_facts = set()
    for fdoc in doc["facts"]:
        fctx = f"fact {fdoc.get('id')!r}"
        fact_id = fdoc["id"]
        if fact_id in seen_facts:
            raise SpecError(f"{fctx}: duplicate fact id")
        seen_facts.add(fact_id)
        kind = fdoc.get("kind")
        if kind == "enum":
            values = fdoc.get("values")
            if not isinstance(values, list) or not values:
                raise SpecError(f"{fctx}: enum fact needs a non-empty 'values' list")
            facts.append(FactSpec(fact_id, int(fdoc["version"]), kind,
                                  values=tuple(values)))
        elif kind == "int":
            lo, hi = fdoc.get("lo"), fdoc.get("hi")
            if not isinstance(lo, int) or not isinstance(hi, int) or lo > hi:
                raise SpecError(f"{fctx}: int fact needs integer lo <= hi")
            facts.append(FactSpec(fact_id, int(fdoc["version"]), kind, lo=lo, hi=hi))
        else:
            raise SpecError(f"{fctx}: kind must be 'enum' or 'int'")

    fact_map = {f.fact_id: f for f in facts}
    rules = []
    seen_rules = set()
    for rdoc in doc["rules"]:
        rctx = f"rule {rdoc.get('id')!r}"
        rule_id = rdoc["id"]
        if rule_id in seen_rules:
            raise SpecError(f"{rctx}: duplicate rule id")
        seen_rules.add(rule_id)
        conditions = {}
        for fact_id, raw in (rdoc.get("conditions") or {}).items():
            if fact_id not in fact_map:
                raise SpecError(f"{rctx}: unknown fact {fact_id!r} in conditions")
            conditions[fact_id] = _parse_constraint(fact_map[fact_id], raw, rctx)
        rules.append(Rule(rule_id, int(rdoc["version"]), int(rdoc["priority"]),
                          str(rdoc["action"]), conditions))

    return Spec(str(doc["spec_id"]), int(doc["version"]),
                tuple(facts), tuple(rules))


def rule_matches(rule, inputs):
    """True iff the concrete input satisfies every condition of the rule."""
    for fact_id, constraint in rule.conditions.items():
        value = inputs[fact_id]
        if isinstance(constraint, EnumSet):
            if value not in constraint.values:
                return False
        else:
            if not (constraint.lo <= value <= constraint.hi):
                return False
    return True


def evaluate(spec, inputs):
    """Evaluate a concrete input; returns matching rules ordered by
    (-priority, rule_id).  Raises SpecError if the input leaves the domain."""
    for fact in spec.facts:
        if fact.fact_id not in inputs:
            raise SpecError(f"input is missing fact {fact.fact_id!r}")
        value = inputs[fact.fact_id]
        if fact.kind == "enum":
            if value not in fact.values:
                raise SpecError(f"input value {value!r} outside domain of {fact.fact_id!r}")
        else:
            if not (fact.lo <= value <= fact.hi):
                raise SpecError(f"input value {value!r} outside domain of {fact.fact_id!r}")
    matched = [r for r in spec.rules if rule_matches(r, inputs)]
    return sorted(matched, key=lambda r: (-r.priority, r.rule_id))

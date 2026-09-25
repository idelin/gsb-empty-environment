"""Deterministic forward-chaining derivation engine.

The engine is pure: it only sees the facts it is given.  The secure
renderer feeds it the *projected* (caller-visible) facts; the naive
renderer feeds it the full world and then tries to delete fields, which
is the counterexample we test against.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass

from .model import STEP_SCHEMA_VERSION, Fact, Rule, Step


@dataclass(frozen=True)
class Derivation:
    fact: Fact
    step: Step | None  # None means an asserted base fact
    premises: tuple["Derivation", ...]


def make_step(rule: Rule, premise_facts: tuple[Fact, ...]) -> Step:
    """Build a step with a stable, content-derived ID (never hash())."""
    digest = hashlib.sha256()
    digest.update(f"{rule.rule_id}@{rule.version}".encode())
    for fact in premise_facts:
        digest.update(f"|{fact.fact_id}@{fact.version}".encode())
    return Step(
        step_id=f"S-{rule.rule_id}-{digest.hexdigest()[:12]}",
        version=STEP_SCHEMA_VERSION,
        rule_id=rule.rule_id,
        rule_version=rule.version,
        premises=tuple(f.fact_id for f in premise_facts),
        conclusion=rule.conclusion,
    )


def derive(
    facts: tuple[Fact, ...],
    rules: tuple[Rule, ...],
    debug: list[str] | None = None,
) -> dict[str, Derivation]:
    """Closure under forward chaining.

    Deterministic: rules fire in sorted (rule_id, version) order and each
    conclusion is derived at most once, so derivations are acyclic and
    reproducible.  When ``debug`` is a list, human-readable trace lines
    are appended; they only mention facts the engine was given.
    """
    known: dict[str, Derivation] = {}
    for fact in sorted(facts, key=lambda f: (f.fact_id, -f.version)):
        if fact.fact_id not in known:
            known[fact.fact_id] = Derivation(fact=fact, step=None, premises=())
    if debug is not None:
        debug.append(f"asserted facts: {len(known)}")
    round_no = 0
    changed = True
    while changed:
        changed = False
        round_no += 1
        for rule in sorted(rules, key=lambda r: (r.rule_id, r.version)):
            if rule.conclusion in known:
                continue
            if all(p in known for p in rule.premises):
                prems = tuple(known[p] for p in rule.premises)
                step = make_step(rule, tuple(d.fact for d in prems))
                level = max((d.fact.level for d in prems), default=0)
                derived = Fact(
                    fact_id=rule.conclusion,
                    version=rule.version,
                    level=level,
                    payload=f"derived-by:{rule.rule_id}",
                )
                known[rule.conclusion] = Derivation(derived, step, prems)
                changed = True
                if debug is not None:
                    debug.append(
                        f"round {round_no}: fired {rule.rule_id}@{rule.version}"
                        f" -> {rule.conclusion}"
                    )
    if debug is not None:
        debug.append(f"closure size: {len(known)}")
    return known

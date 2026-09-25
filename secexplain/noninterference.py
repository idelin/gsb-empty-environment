"""Finite-domain pairwise noninterference tester.

Low-visibility input equivalence: two worlds are low-equivalent at a
clearance when their visible slices (facts and rules at or below the
clearance) are identical.  The security requirement: low-equivalent
worlds must produce identical public outputs - explanations, errors and
structural counts.  The tester enumerates a finite domain of worlds and
compares every low-equivalent pair.

Scope note: timing is explicitly NOT checked; only output content is.
If hidden facts influence *which* visible conclusions are derivable at
all, that is a genuine information flow no projection can hide - the
tester will (correctly) report it as a violation.
"""
from __future__ import annotations

import json
from dataclasses import dataclass

from . import naive, projection
from .engine import Engine
from .model import Fact, Rule


@dataclass(frozen=True)
class World:
    name: str
    facts: tuple[Fact, ...]
    rules: tuple[Rule, ...]


@dataclass(frozen=True)
class Violation:
    world_a: str
    world_b: str
    output_a: str
    output_b: str


def visible_slice(world: World, clearance: int) -> str:
    facts = sorted((f for f in world.facts if f.level <= clearance),
                   key=lambda f: f.fact_id)
    rules = sorted((r for r in world.rules if r.level <= clearance),
                   key=lambda r: r.rule_id)
    canon = {
        "facts": [(f.fact_id, f.version, f.key, f.value, f.level) for f in facts],
        "rules": [(r.rule_id, r.version, list(r.premises), r.conclusion_key,
                   r.conclusion_level, r.level) for r in rules],
    }
    return json.dumps(canon, sort_keys=True)


def low_equivalent(a: World, b: World, clearance: int) -> bool:
    return visible_slice(a, clearance) == visible_slice(b, clearance)


def public_output(world: World, query_key: str, clearance: int,
                  mode: str = "fixed") -> str:
    """Canonical public output string: explanation JSON or public error."""
    derivation = Engine(world.rules).derive(world.facts)
    try:
        if mode == "fixed":
            return "OK:" + projection.project(
                derivation, query_key, clearance).to_json()
        if mode == "naive":
            return "OK:" + json.dumps(
                naive.naive_project(derivation, query_key, clearance),
                sort_keys=True)
        raise ValueError(f"unknown mode: {mode}")
    except projection.ExplanationError as exc:
        return "ERROR:" + str(exc)


def pairwise_check(worlds, query_key: str, clearance: int,
                   mode: str = "fixed") -> list[Violation]:
    """Compare public outputs of every low-equivalent pair of worlds."""
    outputs = {w.name: public_output(w, query_key, clearance, mode)
               for w in worlds}
    violations = []
    for i, world_a in enumerate(worlds):
        for world_b in worlds[i + 1:]:
            if not low_equivalent(world_a, world_b, clearance):
                continue
            if outputs[world_a.name] != outputs[world_b.name]:
                violations.append(Violation(world_a.name, world_b.name,
                                            outputs[world_a.name],
                                            outputs[world_b.name]))
    return violations

"""Visibility projection.

A policy is just a clearance level.  ``project`` keeps exactly the facts
the caller may see and drops everything else; no placeholders, counts or
residual metadata about hidden facts survive the projection.
"""

from __future__ import annotations

import hashlib

from .model import Fact, Rule, World


def project(world: World, clearance: int) -> tuple[Fact, ...]:
    return tuple(f for f in world.facts if f.level <= clearance)


def projection_digest(facts: tuple[Fact, ...], rules: tuple[Rule, ...]) -> str:
    """Canonical digest of a projection; used as a cache/evidence key."""
    digest = hashlib.sha256()
    for fact in sorted(facts, key=lambda f: (f.fact_id, f.version)):
        digest.update(
            f"F {fact.fact_id}@{fact.version} L{fact.level} {fact.payload}\n".encode()
        )
    for rule in sorted(rules, key=lambda r: (r.rule_id, r.version)):
        digest.update(
            f"R {rule.rule_id}@{rule.version} {','.join(rule.premises)}"
            f"->{rule.conclusion}\n".encode()
        )
    return digest.hexdigest()

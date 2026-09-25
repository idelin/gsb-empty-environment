"""Versioned inference rules referenced by derivation steps."""
from __future__ import annotations

from .model import Rule

RULE_EQUIV_TRANSITIVE = Rule(
    rule_id="rule-equiv-transitive",
    version=1,
    description="Equivalence evidence merges components transitively.",
)

RULE_NEQUIV_SYMMETRIC = Rule(
    rule_id="rule-nequiv-symmetric",
    version=1,
    description="Non-equivalence is symmetric between its two endpoints.",
)

RULE_CONFLICT_RING = Rule(
    rule_id="rule-conflict-evidence-ring",
    version=1,
    description=(
        "A nequiv edge whose endpoints are connected by an equiv path "
        "is a conflict; the path plus the edge form the evidence ring."
    ),
)

RULE_REPRESENTATIVE_MIN_KEY = Rule(
    rule_id="rule-representative-min-key",
    version=1,
    description="The representative ID of a component is its lexicographically smallest entity key.",
)

ALL_RULES: tuple[Rule, ...] = (
    RULE_EQUIV_TRANSITIVE,
    RULE_NEQUIV_SYMMETRIC,
    RULE_CONFLICT_RING,
    RULE_REPRESENTATIVE_MIN_KEY,
)

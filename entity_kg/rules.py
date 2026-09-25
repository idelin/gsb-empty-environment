"""Named, versioned inference rules.

Rules are data, not hidden behavior: every derived Step records the rule
id/version it came from, and every Conflict records the rule that detected it.
"""

from __future__ import annotations

from .model import Rule

RULES: dict[str, Rule] = {
    "R-EQ-EXPLICIT": Rule(
        rid="R-EQ-EXPLICIT",
        version=1,
        name="explicit_equivalence",
        description="An explicit EQ fact asserts two node ids are the same entity.",
    ),
    "R-NEQ-EXPLICIT": Rule(
        rid="R-NEQ-EXPLICIT",
        version=1,
        name="explicit_inequivalence",
        description="An explicit NEQ fact asserts two node ids are different entities.",
    ),
    "R-EQ-TRANSITIVE": Rule(
        rid="R-EQ-TRANSITIVE",
        version=1,
        name="equivalence_transitive_chain",
        description="If x0=x1=...=xn is supported by a chain of explicit EQ "
                    "facts, then x0=xn.",
    ),
    "R-NEQ-CYCLE": Rule(
        rid="R-NEQ-CYCLE",
        version=1,
        name="inequality_evidence_cycle",
        description="An EQ chain whose endpoints are joined by an explicit NEQ "
                    "forms a contradictory evidence cycle.",
    ),
    "R-NEQ-REFLEXIVE": Rule(
        rid="R-NEQ-REFLEXIVE",
        version=1,
        name="inequality_reflexivity_violation",
        description="Every node is equal to itself; asserting a!=a contradicts reflexivity.",
    ),
    "R-EQ-NEQ-DIRECT": Rule(
        rid="R-EQ-NEQ-DIRECT",
        version=1,
        name="direct_eq_neq_contradiction",
        description="The same unordered pair carrying both EQ and NEQ evidence "
                    "is directly contradictory.",
    ),
    "R-NAME-LABEL": Rule(
        rid="R-NAME-LABEL",
        version=1,
        name="names_are_labels_only",
        description="Shared display names never imply identity; only explicit "
                    "EQ evidence merges node ids.",
    ),
    "R-REP-MIN": Rule(
        rid="R-REP-MIN",
        version=1,
        name="representative_min_node_id",
        description="The representative of an equivalence component is its "
                    "lexicographically smallest node id, independent of "
                    "assertion order.",
    ),
}


def rule(rid: str) -> Rule:
    return RULES[rid]


def all_rules() -> list[Rule]:
    return [RULES[k] for k in sorted(RULES)]

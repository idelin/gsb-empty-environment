"""Core data model: facts, rules, goals, and replayable proof certificates.

Every entity carries a stable string ID and an integer version.  Versions are
part of the identity of evidence: a certificate records the exact versions it
was built from, so it can be replayed (verified) later against a snapshot of
facts and rules.  All sample data in this repository is synthetic.
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Mapping


class GoalStatus(Enum):
    PROVED = "proved"
    DISPROVED = "disproved"
    UNDECIDED = "undecided"


@dataclass(frozen=True)
class Fact:
    """A base fact (axiom).  Identity = stable ID; evidence pins the version."""

    fact_id: str
    version: int = 1


@dataclass(frozen=True)
class Rule:
    """A derivation rule: premises (node IDs) -> conclusion (node ID)."""

    rule_id: str
    premises: tuple[str, ...]
    conclusion: str
    version: int = 1


@dataclass(frozen=True)
class Goal:
    """A weighted conjunctive goal: proved when every target node is proved."""

    goal_id: str
    targets: tuple[str, ...]
    weight: float


@dataclass(frozen=True)
class ProofNode:
    """One node of a proof tree.

    A leaf (rule_id is None) cites a base fact and its version; an internal
    node cites the rule ID and version used, plus one child per premise.
    """

    node_id: str
    fact_version: int | None = None
    rule_id: str | None = None
    rule_version: int | None = None
    children: tuple["ProofNode", ...] = ()


@dataclass(frozen=True)
class Certificate:
    """Replayable evidence for a proved goal: one proof tree per conjunct."""

    goal_id: str
    proofs: tuple[ProofNode, ...]


def replay_certificate(
    cert: Certificate,
    facts: Mapping[str, Fact],
    rules: Mapping[str, Rule],
) -> bool:
    """Verify a certificate against a snapshot of facts and rules.

    Every cited fact/rule must exist with the exact recorded version, each
    rule's conclusion must match the node it justifies, and children must
    match the rule premises in order.  Pure function: no engine state needed.
    """

    def check(node: ProofNode) -> bool:
        if node.rule_id is None:
            fact = facts.get(node.node_id)
            return fact is not None and fact.version == node.fact_version
        rule = rules.get(node.rule_id)
        if rule is None or rule.version != node.rule_version:
            return False
        if rule.conclusion != node.node_id:
            return False
        if len(rule.premises) != len(node.children):
            return False
        return all(
            child.node_id == premise and check(child)
            for child, premise in zip(node.children, rule.premises)
        )

    return all(check(proof) for proof in cert.proofs)

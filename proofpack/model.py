"""Core data model: facts, rules and the knowledge base.

Everything carries a stable string ID and a monotonically increasing
integer version.  All sample data shipped with this package is synthetic.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Fact:
    """A fact node in the evidence graph.

    ``public`` marks facts whose disclosure is considered a "leak" (the
    primary optimisation objective minimises how many of them must appear
    in an exported package).  ``evidence`` marks facts that may be used as
    base (leaf) evidence items; derived-only facts set it to False.
    """

    fact_id: str
    version: int
    payload: str
    public: bool = False
    evidence: bool = True


@dataclass(frozen=True)
class Rule:
    """A Horn-style derivation rule: premises |- conclusion."""

    rule_id: str
    version: int
    premises: tuple[str, ...]
    conclusion: str


class KnowledgeBase:
    """An immutable, validated collection of facts and rules."""

    def __init__(self, facts, rules):
        fact_list = list(facts)
        rule_list = list(rules)
        self.facts = {f.fact_id: f for f in fact_list}
        self.rules = {r.rule_id: r for r in rule_list}
        if len(self.facts) != len(fact_list):
            raise ValueError("duplicate fact IDs")
        if len(self.rules) != len(rule_list):
            raise ValueError("duplicate rule IDs")
        self._validate()
        self._by_conclusion: dict[str, list[Rule]] = {}
        for rule in sorted(rule_list, key=lambda r: r.rule_id):
            self._by_conclusion.setdefault(rule.conclusion, []).append(rule)

    def _validate(self) -> None:
        for rule in self.rules.values():
            for ref in (*rule.premises, rule.conclusion):
                if ref not in self.facts:
                    raise ValueError(
                        f"rule {rule.rule_id} references unknown fact {ref}"
                    )

    def rules_concluding(self, fact_id: str) -> list[Rule]:
        """All rules deriving ``fact_id``, in stable rule-ID order."""
        return self._by_conclusion.get(fact_id, [])

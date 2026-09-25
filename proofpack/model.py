"""Core data model: facts, rules and the knowledge base.

A *fact* is a piece of evidence (base) or a derived claim (non-base).
A *rule* derives one fact (its conclusion) from a set of premises.
Everything carries a stable string id and a positive integer version.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Fact:
    id: str
    version: int
    statement: str
    public: bool = False
    base: bool = True  # base facts may be used as leaf evidence


@dataclass(frozen=True)
class Rule:
    id: str
    version: int
    premises: tuple[str, ...]
    conclusion: str


class KnowledgeBaseError(ValueError):
    pass


class KnowledgeBase:
    """A finite, validated fact/rule graph."""

    def __init__(self, facts, rules):
        self.facts: dict[str, Fact] = {}
        for fact in facts:
            if fact.id in self.facts:
                raise KnowledgeBaseError(f"duplicate fact id: {fact.id!r}")
            if not isinstance(fact.version, int) or fact.version < 1:
                raise KnowledgeBaseError(f"fact {fact.id!r} has invalid version")
            self.facts[fact.id] = fact
        self.rules: dict[str, Rule] = {}
        for rule in rules:
            if rule.id in self.rules:
                raise KnowledgeBaseError(f"duplicate rule id: {rule.id!r}")
            if not isinstance(rule.version, int) or rule.version < 1:
                raise KnowledgeBaseError(f"rule {rule.id!r} has invalid version")
            for premise in rule.premises:
                if premise not in self.facts:
                    raise KnowledgeBaseError(
                        f"rule {rule.id!r} has unknown premise {premise!r}"
                    )
            if rule.conclusion not in self.facts:
                raise KnowledgeBaseError(
                    f"rule {rule.id!r} has unknown conclusion {rule.conclusion!r}"
                )
            self.rules[rule.id] = rule
        self._by_conclusion: dict[str, list[Rule]] = {}
        for rule in self.rules.values():
            self._by_conclusion.setdefault(rule.conclusion, []).append(rule)
        for rule_list in self._by_conclusion.values():
            rule_list.sort(key=lambda r: r.id)  # deterministic iteration
        self.acyclic = self._check_acyclic()

    def rules_concluding(self, fact_id: str):
        return self._by_conclusion.get(fact_id, ())

    def _check_acyclic(self) -> bool:
        white, gray, black = 0, 1, 2
        color = {fact_id: white for fact_id in self.facts}

        def visit(fact_id: str) -> bool:
            color[fact_id] = gray
            for rule in self._by_conclusion.get(fact_id, ()):
                for premise in rule.premises:
                    if color[premise] == gray:
                        return False
                    if color[premise] == white and not visit(premise):
                        return False
            color[fact_id] = black
            return True

        return all(color[f] != white or visit(f) for f in self.facts)

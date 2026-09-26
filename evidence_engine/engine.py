"""Traceable, monotonic rule engine with source-attributed evidence.

Semantics
---------
* A fact is a non-empty string (a ground atom).
* A rule is a definite clause: a finite, deduplicated premise set of facts
  entails one conclusion fact. Negation is not representable.
* Base facts are supplied by named sources. A fact can have several
  independent sources; each source alone is a minimal support.
* A rule fires by choosing one support set per premise; the conclusion is
  supported by the union of those sets.
* Only inclusion-minimal support sets are kept for each fact (an antichain).
  They are returned ordered by (number of sources, lexicographic order).
* Consequences are computed by semi-naive forward chaining starting from the
  base facts only (the least fixpoint). A rule cycle therefore produces
  nothing unless at least one member has an outside support: it cannot
  bootstrap itself.
* Revoking a source recomputes the least fixpoint from the remaining base
  facts, which removes exactly the derived facts that lost every support.

The number of minimal support sets can grow exponentially. A derivation
budget bounds the work; when it is exceeded the engine sets ``interrupted``
and reports a sound but possibly incomplete prefix of the model.
"""

from __future__ import annotations

from itertools import product
from typing import Iterable, Optional


class BudgetExceeded(RuntimeError):
    """Raised internally when the derivation budget is exhausted."""


def _valid_token(value: object, what: str) -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{what} must be a non-empty string, got {value!r}")
    return value


class Rule:
    """A definite rule: ``premises`` (a finite set) entail ``conclusion``."""

    __slots__ = ("premises", "conclusion")

    def __init__(self, premises: Iterable[str], conclusion: str):
        conclusion = _valid_token(conclusion, "rule conclusion")
        seen: set[str] = set()
        ordered: list[str] = []
        for premise in premises:
            premise = _valid_token(premise, "rule premise")
            if premise not in seen:
                seen.add(premise)
                ordered.append(premise)
        self.premises: tuple[str, ...] = tuple(ordered)
        self.conclusion: str = conclusion

    def __repr__(self) -> str:
        lhs = ", ".join(self.premises) if self.premises else "() (axiom)"
        return f"Rule({lhs} -> {self.conclusion})"

    def __eq__(self, other: object) -> bool:
        return (
            isinstance(other, Rule)
            and self.premises == other.premises
            and self.conclusion == other.conclusion
        )

    def __hash__(self) -> int:
        return hash((self.premises, self.conclusion))


# A recipe records how one support set of one fact was obtained:
#   ("base", source)
#   ("rule", Rule, tuple[(premise_fact, premise_support), ...])
Recipe = tuple


class Engine:
    """Least-fixpoint inference engine over source-attributed base facts."""

    def __init__(self, rules: Iterable[Rule] = (), budget: int = 1_000_000):
        self.rules: tuple[Rule, ...] = tuple(rules)
        if not isinstance(budget, int) or budget < 0:
            raise ValueError("budget must be a non-negative int")
        self.budget = budget
        # base fact -> set of independent source ids
        self._base: dict[str, set[str]] = {}
        # fact -> list of (support: frozenset[str], recipe: Recipe)
        self._families: dict[str, list[tuple[frozenset[str], Recipe]]] = {}
        self.derivations = 0
        self.interrupted = False
        self._recompute()

    # ------------------------------------------------------------------ base

    def add_source(self, source: str, fact: str) -> None:
        """Assert ``fact`` on the authority of ``source`` and recompute."""
        source = _valid_token(source, "source")
        fact = _valid_token(fact, "fact")
        self._base.setdefault(fact, set()).add(source)
        self._recompute()

    def revoke_source(self, source: str) -> None:
        """Remove every base assertion made by ``source`` and recompute."""
        source = _valid_token(source, "source")
        for supporters in self._base.values():
            supporters.discard(source)
        self._base = {
            fact: supporters
            for fact, supporters in self._base.items()
            if supporters
        }
        self._recompute()

    @property
    def base_facts(self) -> dict[str, list[str]]:
        return {
            fact: sorted(supporters) for fact, supporters in self._base.items()
        }

    # ---------------------------------------------------------------- queries

    def supported(self, fact: str) -> bool:
        return fact in self._families

    def supports(self, fact: str) -> list[frozenset[str]]:
        """Return the minimal support sets of ``fact``.

        Order: ascending cardinality, then lexicographic on the sorted
        source-id tuple.
        """
        return [support for support, _ in self._families.get(fact, ())]

    def all_facts(self) -> list[str]:
        return sorted(self._families)

    def trace(self, fact: str) -> Optional[dict]:
        """Return a fully expanded derivation tree for the minimum support."""
        if fact not in self._families:
            return None
        support = self.supports(fact)[0]
        return self._trace(fact, support, {})

    def state(self) -> dict:
        return {
            fact: {
                "supports": [sorted(s) for s in self.supports(fact)],
                "trace": self.trace(fact),
            }
            for fact in self.all_facts()
        }

    # ------------------------------------------------------------- internals

    def _trace(
        self,
        fact: str,
        support: frozenset[str],
        memo: dict[tuple[str, frozenset[str]], dict],
    ) -> dict:
        key = (fact, support)
        if key in memo:
            return memo[key]
        recipe = self._recipe_of(fact, support)
        node: dict = {"fact": fact, "support": sorted(support)}
        if recipe[0] == "base":
            node["kind"] = "base"
            node["source"] = recipe[1]
        else:
            _, rule, chosen = recipe
            node["kind"] = "rule"
            node["premises"] = [
                {"fact": premise, "support": sorted(premise_support)}
                for premise, premise_support in chosen
            ]
            node["children"] = [
                self._trace(premise, premise_support, memo)
                for premise, premise_support in chosen
            ]
        memo[key] = node
        return node

    def _recipe_of(self, fact: str, support: frozenset[str]) -> Recipe:
        for candidate, recipe in self._families[fact]:
            if candidate == support:
                return recipe
        raise KeyError((fact, support))

    def _recompute(self) -> None:
        """Recompute the least fixpoint from the current base facts.

        Semi-naive strategy: in each round a rule combination is examined
        only when at least one premise uses a support newly discovered since
        the previous round. Each combination is therefore examined at most
        once.
        """
        families: dict[str, list[tuple[frozenset[str], Recipe]]] = {}
        used = 0
        interrupted = False
        fired_axioms: set[int] = set()
        previous: dict[str, set[frozenset[str]]] = {}

        def charge(n: int = 1) -> None:
            nonlocal used
            used += n
            if used > self.budget:
                raise BudgetExceeded

        try:
            # Base assertions are the first delta.
            for fact in sorted(self._base):
                for source in sorted(self._base[fact]):
                    charge()
                    self._insert(
                        families, fact, frozenset({source}), ("base", source)
                    )

            # Axiom rules (empty premise set) fire exactly once, unsupported
            # by any external source: their support is the empty set.
            for rule in self.rules:
                if not rule.premises:
                    charge()
                    self._insert(families, rule.conclusion, frozenset(),
                                 ("rule", rule, ()))
                    fired_axioms.add(id(rule))

            while True:
                round_start = {
                    fact: {support for support, _ in entries}
                    for fact, entries in families.items()
                }
                inserted_any = False
                for rule in self.rules:
                    if id(rule) in fired_axioms:
                        continue
                    premise_entries = [
                        families.get(premise) for premise in rule.premises
                    ]
                    if any(entries is None for entries in premise_entries):
                        continue
                    premise_sets = [
                        [support for support, _ in entries]
                        for entries in premise_entries
                    ]
                    delta_sets = [
                        [s for s in sets if s not in previous.get(premise, ())]
                        for premise, sets in zip(rule.premises, premise_sets)
                    ]
                    if not any(delta_sets):
                        continue
                    visited: set[tuple[frozenset[str], ...]] = set()
                    for i, delta in enumerate(delta_sets):
                        if not delta:
                            continue
                        lists = [
                            delta if j == i else premise_sets[j]
                            for j in range(len(rule.premises))
                        ]
                        for combo in product(*lists):
                            if combo in visited:
                                continue
                            visited.add(combo)
                            charge()
                            support = frozenset().union(*combo)
                            chosen = tuple(zip(rule.premises, combo))
                            if self._insert(
                                families,
                                rule.conclusion,
                                support,
                                ("rule", rule, chosen),
                            ):
                                inserted_any = True
                previous = round_start
                if not inserted_any:
                    break
        except BudgetExceeded:
            interrupted = True

        self._families = self._sort_families(families)
        self.derivations = used
        self.interrupted = interrupted

    @staticmethod
    def _insert(
        families: dict[str, list[tuple[frozenset[str], Recipe]]],
        fact: str,
        support: frozenset[str],
        recipe: Recipe,
    ) -> bool:
        """Insert into the inclusion antichain; return True if new/minimal."""
        entries = families.setdefault(fact, [])
        for existing, _ in entries:
            if existing == support or existing < support:
                return False
        entries[:] = [
            (existing, existing_recipe)
            for existing, existing_recipe in entries
            if not support < existing
        ]
        entries.append((support, recipe))
        return True

    @staticmethod
    def _sort_families(
        families: dict[str, list[tuple[frozenset[str], Recipe]]],
    ) -> dict[str, list[tuple[frozenset[str], Recipe]]]:
        ordered: dict[str, list[tuple[frozenset[str], Recipe]]] = {}
        for fact, entries in families.items():
            ordered[fact] = sorted(
                entries,
                key=lambda item: (len(item[0]), tuple(sorted(item[0]))),
            )
        return ordered

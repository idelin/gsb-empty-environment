"""Target (build) conditions gating version sets.

A :class:`Condition` is a conjunction of ``key=value`` literals; the empty
condition matches every build.  A :class:`ConditionalVersionSet` is an
ordered list of branches; at evaluation time the first matching branch wins.

Canonicalisation builds a truth table over the *finite* set of atoms that
appear in the input, groups assignments that select the same version set,
and folds minterms with Quine-style consensus rules.  The version universe
itself is never enumerated.
"""
from __future__ import annotations

import hashlib
from dataclasses import dataclass

from .versionset import VersionSet


@dataclass(frozen=True)
class Literal:
    key: str
    value: str

    @classmethod
    def parse(cls, text: str) -> "Literal":
        if "=" not in text:
            raise ValueError(f"condition literal must be key=value: {text!r}")
        key, value = text.split("=", 1)
        return cls(key.strip(), value.strip())

    def matches(self, context: dict[str, str]) -> bool:
        return context.get(self.key) == self.value

    def __str__(self) -> str:
        return f"{self.key}={self.value}"


@dataclass(frozen=True)
class Condition:
    literals: frozenset[Literal] = frozenset()

    @classmethod
    def parse(cls, text: str) -> "Condition":
        text = text.strip()
        if not text:
            return cls(frozenset())
        return cls(frozenset(Literal.parse(part) for part in text.split(",") if part))

    @property
    def atoms(self) -> frozenset[Literal]:
        return self.literals

    def matches(self, context: dict[str, str]) -> bool:
        return all(lit.matches(context) for lit in self.literals)

    def __str__(self) -> str:
        return ",".join(str(lit) for lit in sorted(self.literals, key=str))


@dataclass(frozen=True)
class Branch:
    condition: Condition
    value: VersionSet


@dataclass(frozen=True)
class ConditionalVersionSet:
    branches: tuple[Branch, ...]

    @classmethod
    def unconditional(cls, value: VersionSet) -> "ConditionalVersionSet":
        return cls((Branch(Condition(), value),))

    @classmethod
    def parse(cls, spec: str | dict) -> "ConditionalVersionSet":
        from .spec import parse_spec

        if isinstance(spec, str):
            return cls.unconditional(parse_spec(spec))
        if isinstance(spec, dict):
            if "branches" in spec:
                raw = (
                    Branch(Condition.parse(item.get("when", "")), parse_spec(item["set"]))
                    for item in spec["branches"]
                )
            else:
                raw = (
                    Branch(Condition.parse(when), parse_spec(value))
                    for when, value in spec.items()
                )
            return cls(tuple(raw)).canonical()
        raise TypeError("conditional spec must be str or dict")

    def all_atoms(self) -> list[Literal]:
        atoms: set[Literal] = set()
        for branch in self.branches:
            atoms |= branch.condition.atoms
        return sorted(atoms, key=str)

    def evaluate(self, context: dict[str, str]) -> VersionSet:
        for branch in self.branches:
            if branch.condition.matches(context):
                return branch.value
        return VersionSet.empty()

    def __and__(self, other: "ConditionalVersionSet") -> "ConditionalVersionSet":
        expanded: list[Branch] = []
        for assignment in _assignments(sorted(set(self.all_atoms()) | set(other.all_atoms()), key=str)):
            context = _context_for(assignment)
            condition = Condition(frozenset(lit for lit, truth in assignment.items() if truth))
            expanded.append(
                Branch(condition, self.evaluate(context) & other.evaluate(context))
            )
        return ConditionalVersionSet(tuple(expanded)).canonical()

    def canonical(self) -> "ConditionalVersionSet":
        atoms = self.all_atoms()
        table: dict[frozenset[Literal], str] = {}
        values: dict[str, VersionSet] = {}
        for assignment in _assignments(atoms):
            context = _context_for(assignment)
            value = self.evaluate(context)
            positive = frozenset(lit for lit, truth in assignment.items() if truth)
            digest = value.serialize()
            values[digest] = value
            table[positive] = digest
        groups: dict[str, list[frozenset[Literal]]] = {}
        for minterm, digest in table.items():
            groups.setdefault(digest, []).append(minterm)
        branches: list[Branch] = []
        for digest, minterms in groups.items():
            for term in _cover(frozenset(atoms), minterms):
                branches.append(Branch(Condition(term), values[digest]))
        branches.sort(key=lambda b: (b.value.serialize(), _condition_key(b.condition, atoms)))
        return ConditionalVersionSet(tuple(branches))

    def serialize(self) -> str:
        return " ? ".join(
            f"{branch.condition}:{branch.value.serialize()}" for branch in self.branches
        )

    def digest(self) -> str:
        return hashlib.sha256(self.serialize().encode("utf-8")).hexdigest()


def _condition_key(condition: Condition, atoms: list[Literal]):
    return tuple(1 if atom in condition.atoms else 0 for atom in atoms) or (0,)


def _assignments(atoms: list[Literal]):
    if not atoms:
        yield {}
        return
    head, rest = atoms[0], atoms[1:]
    for tail in _assignments(rest):
        true_case = dict(tail)
        true_case[head] = True
        yield true_case
        false_case = dict(tail)
        false_case[head] = False
        yield false_case


def _context_for(assignment: dict[Literal, bool]) -> dict[str, str]:
    context: dict[str, str] = {}
    for literal, truth in assignment.items():
        if truth:
            context[literal.key] = literal.value
    return context


def _covers(term: frozenset[Literal], minterm: frozenset[Literal]) -> bool:
    return term <= minterm


def _covered_by(term: frozenset[Literal], minterms: list[frozenset[Literal]]) -> set[frozenset[Literal]]:
    return {m for m in minterms if term <= m}


def _cover(atoms: frozenset[Literal], minterms: list[frozenset[Literal]]) -> list[frozenset[Literal]]:
    """Monotone DNF cover reduced by absorption and Quine consensus.

    Terms are conjunctions of positive literals (a missing atom means
    "don't care").  Absorption removes a term whose covered minterms are all
    covered by another term; consensus merges two terms differing on exactly
    one atom when the merged term stays within the target minterm set.
    """
    targets = set(minterms)
    current = set(minterms)

    def reduce_once(terms: set[frozenset[Literal]]):
        items = list(terms)
        redundant: set[frozenset[Literal]] = set()
        for term in items:
            covered = _covered_by(term, list(targets))
            others = [o for o in items if o != term]
            if any(covered <= _covered_by(other, list(targets)) for other in others):
                redundant.add(term)
        terms -= redundant
        items = list(terms)
        consumed: set[frozenset[Literal]] = set()
        produced: set[frozenset[Literal]] = set()
        for i, left in enumerate(items):
            for right in items[i + 1:]:
                if left <= right or right <= left:
                    continue
                if len(left ^ right) == 1:
                    common = left & right
                    if _covered_by(common, list(targets)) <= targets:
                        produced.add(common)
                        consumed.add(left)
                        consumed.add(right)
        if produced:
            terms = (terms - consumed) | produced
        return terms, bool(produced)

    changed = True
    while changed:
        current, changed = reduce_once(current)
    return sorted(current, key=lambda s: (-len(s), sorted(map(str, s))))

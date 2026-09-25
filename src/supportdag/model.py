"""Core domain model: facts and rules with stable identity and versioning."""

from __future__ import annotations

from dataclasses import dataclass
from typing import FrozenSet, Iterable, List, Sequence, Tuple, Union

SignedAtom = Union[str, Tuple[str, str]]
Premise = Union[str, Tuple]

_UNSAT = frozenset({("__unsat__",)})


def NOT(atom: str) -> Tuple[str, str]:
    return ("not", atom)


def ANY(*branches: Premise) -> Tuple[str, Tuple[Premise, ...]]:
    return ("any", tuple(branches))


def is_negative(lit: SignedAtom) -> bool:
    return isinstance(lit, tuple) and lit and lit[0] == "not"


def atom_of(lit: SignedAtom) -> str:
    return lit[1] if is_negative(lit) else lit


@dataclass(frozen=True)
class Fact:
    fid: str
    atom: str
    version: int = 1

    def step_id(self) -> str:
        return f"fact:{self.fid}:v{self.version}"


@dataclass(frozen=True)
class Rule:
    rid: str
    head: str
    body: Tuple[Premise, ...]
    version: int = 1

    @property
    def alternatives(self) -> FrozenSet[FrozenSet[SignedAtom]]:
        return body_dnf(self.body)

    def step_id(self, alt_index: int) -> str:
        return f"rule:{self.rid}:v{self.version}#a{alt_index}"


def _dnf(premise):
    """DNF of one premise. Unsatisfiable conjunction -> singleton {_UNSAT}."""
    if isinstance(premise, str):
        return [frozenset({premise})]
    if not isinstance(premise, tuple):
        raise ValueError(f"unknown premise: {premise!r}")
    tag = premise[0]
    if tag == "not":
        return [frozenset({("not", premise[1])})]
    if tag == "any":
        merged: List[FrozenSet[SignedAtom]] = []
        for branch in premise[1]:
            for option in _dnf(branch):
                if option and _UNSAT <= option:
                    continue  # unsatisfiable branch dropped from the OR
                merged.append(option)
        return merged or [_UNSAT]
    # Bare tuple: conjunction of parts.
    combos: List[FrozenSet[SignedAtom]] = [frozenset()]
    for part in premise:
        combos = _cross(combos, _dnf(part))
    return combos or [_UNSAT]


def _cross(left, right):
    left = list(left)
    right = list(right)
    if any(_UNSAT <= x for x in left) or any(_UNSAT <= x for x in right):
        return [_UNSAT]
    return [a | b for a in left for b in right]


def body_dnf(body: Sequence[Premise]) -> FrozenSet[FrozenSet[SignedAtom]]:
    combos: List[FrozenSet[SignedAtom]] = [frozenset()]
    for premise in body:
        combos = _cross(combos, _dnf(premise))
    result = set()
    for combo in combos:
        if _UNSAT <= combo:
            continue
        pos = {lit for lit in combo if not is_negative(lit)}
        neg = {atom_of(lit) for lit in combo if is_negative(lit)}
        if pos.isdisjoint(neg):
            result.add(combo)
    return frozenset(result)


def premise_to_json(premise: Premise):
    if isinstance(premise, str):
        return premise
    if premise[0] == "not":
        return {"not": premise[1]}
    if premise[0] == "any":
        return {"any": [premise_to_json(b) for b in premise[1]]}
    raise ValueError(f"cannot serialize premise {premise!r}")


def premise_from_json(data) -> Premise:
    if isinstance(data, str):
        return data
    if "not" in data:
        return NOT(data["not"])
    if "any" in data:
        return ANY(*(premise_from_json(b) for b in data["any"]))
    raise ValueError(f"cannot parse premise {data!r}")

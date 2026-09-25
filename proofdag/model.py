"""Data model: facts, rules and steps, all with stable IDs and versions."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Tuple, Union


@dataclass(frozen=True)
class Fact:
    """A fact asserted by some source.

    Identity includes ``source_id``: the same value coming from two different
    sources is never merged into one node.
    """

    fact_id: str
    version: int
    source_id: str
    payload: str

    def structural_key(self) -> tuple:
        return ("fact", self.fact_id, self.version, self.source_id, self.payload)


@dataclass(frozen=True)
class Rule:
    """A derivation rule.

    ``commutative=True`` means the premises form an unordered (conjunctive)
    set; ``commutative=False`` means premise order is significant.
    Both kinds may freely coexist inside one proof.
    """

    rule_id: str
    version: int
    commutative: bool = False

    def structural_key(self) -> tuple:
        return ("rule", self.rule_id, self.version, self.commutative)


@dataclass(frozen=True)
class Step:
    """A user-supplied derivation step.

    ``step_id`` is a stable external name for the step. It never participates
    in canonical identity, so randomly renaming internal nodes does not
    change canonical results.
    """

    step_id: str
    version: int
    rule: Rule
    premises: Tuple[Union[Fact, "Step"], ...]
    conclusion: Fact

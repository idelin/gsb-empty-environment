"""Core data model: facts, rules, steps and evidence rings.

Everything carries a stable ID and a version so that any derived state
can be traced back to the exact evidence that produced it.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum


class FactKind(str, Enum):
    """An explicit evidence edge between two entity keys."""

    EQUIV = "equiv"      # left and right are the same real-world entity
    NEQUIV = "nequiv"    # left and right are known to be different entities


class StepAction(str, Enum):
    REGISTER_ENTITY = "register-entity"
    ASSERT_FACT = "assert-fact"
    RETRACT_FACT = "retract-fact"


@dataclass(frozen=True)
class Fact:
    """A piece of explicit, replayable evidence.

    Entities are identified by opaque keys only. Display names are mere
    attributes and never participate in identity decisions.
    """

    fact_id: str
    version: int
    kind: FactKind
    left: str
    right: str
    evidence: str = ""

    def __post_init__(self) -> None:
        if not self.fact_id:
            raise ValueError("fact_id must be non-empty")
        if self.version < 1:
            raise ValueError("version must be >= 1")
        if not self.left or not self.right:
            raise ValueError("entity keys must be non-empty")


@dataclass(frozen=True)
class Rule:
    """A named, versioned inference rule used by derivation steps."""

    rule_id: str
    version: int
    description: str


@dataclass(frozen=True)
class Step:
    """One replayable mutation of the evidence store."""

    step_id: str
    version: int
    seq: int
    action: StepAction
    fact: Fact | None = None
    retract_fact_id: str | None = None
    entity_key: str | None = None
    entity_name: str | None = None
    rule_ids: tuple[str, ...] = field(default_factory=tuple)


@dataclass(frozen=True)
class EvidenceRing:
    """A conflict witness: an equivalence path closed by a non-equivalence.

    ``equiv_path_fact_ids`` lists the equiv fact IDs connecting the two
    endpoints of the conflicting nequiv fact, in endpoint order.
    """

    equiv_path_fact_ids: tuple[str, ...]
    nequiv_fact_id: str

    @property
    def fact_ids(self) -> tuple[str, ...]:
        return self.equiv_path_fact_ids + (self.nequiv_fact_id,)

"""Evidence-driven entity resolution with retractable equivalence edges."""

from .model import (
    Conflict,
    CycleEdge,
    DerivedState,
    Entity,
    Event,
    EventKind,
    Fact,
    FactKind,
    PremiseEdge,
    Rule,
    Step,
    make_fact_id,
    make_step_id,
)
from .repository import ConflictError, EntityRepository
from .rules import RULES, all_rules, rule

__all__ = [
    "Conflict",
    "ConflictError",
    "CycleEdge",
    "DerivedState",
    "Entity",
    "EntityRepository",
    "Event",
    "EventKind",
    "Fact",
    "FactKind",
    "PremiseEdge",
    "RULES",
    "Rule",
    "Step",
    "all_rules",
    "make_fact_id",
    "make_step_id",
    "rule",
]

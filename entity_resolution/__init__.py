"""Evidence-driven entity resolution with retraction and conflict rings."""
from .engine import (
    ConflictError,
    DuplicateFactError,
    Engine,
    FactNotActiveError,
    UnknownFactError,
)
from .model import EvidenceRing, Fact, FactKind, Rule, Step, StepAction

__all__ = [
    "ConflictError",
    "DuplicateFactError",
    "Engine",
    "EvidenceRing",
    "Fact",
    "FactKind",
    "FactNotActiveError",
    "Rule",
    "Step",
    "StepAction",
    "UnknownFactError",
]

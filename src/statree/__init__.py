"""State-constrained ordered tree automata over shared-subtree DAGs."""
from .conditions import Condition, ConditionError, parse_condition
from .dag import Dag, DagError, Node, StructuralCycleError
from .engine import (
    CancelToken,
    CancellationError,
    CountdownCancel,
    Evidence,
    LimitExceeded,
    Limits,
    Proof,
    Stats,
    explain,
    least_fixpoint,
)
from .model import Rule, RuleError, Signature
from .session import Automaton, EditReport, Snapshot

__all__ = [
    "Automaton",
    "CancelToken",
    "CancellationError",
    "Condition",
    "ConditionError",
    "CountdownCancel",
    "Dag",
    "DagError",
    "EditReport",
    "Evidence",
    "LimitExceeded",
    "Limits",
    "Node",
    "Proof",
    "Rule",
    "RuleError",
    "Signature",
    "Snapshot",
    "Stats",
    "StructuralCycleError",
    "explain",
    "least_fixpoint",
    "parse_condition",
]

"""Ordered tree automata with state constraints over shared-subtree DAGs."""

from .core import (Alphabet, AlphabetError, AttrCondition, Automaton,
                   DEFAULT_PREDICATES, MaxCountConstraint, MutexConstraint,
                   Node, NoProofError, PredicateRegistry, Rule, RuleError,
                   StateConstraint, StructuralCycleError, TreeAutomatonError,
                   Violation, occurrence_map, postorder_unique, validate_dag)
from .evaluation import (CancelToken, CancelledError, Derivation, EvalStats,
                         Limits, Proof, ResourceLimitError, RunResult,
                         compute, evaluate)
from .incremental import EditReport, RevokeReport, Session
from .counterexample import (Counterexample, ReplayResult, check_expectation,
                             tree_from_spec, tree_to_spec)

__all__ = [name for name in dir() if not name.startswith("_")]

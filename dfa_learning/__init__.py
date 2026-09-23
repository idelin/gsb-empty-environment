"""Query-based learning of unknown deterministic finite automata.

The learner sees only a membership oracle and a bounded equivalence oracle
over the domain of words of length <= L.  See README.md for an overview.
"""

from .dfa import DFA
from .enumerator import (
    brute_force_counterexample,
    domain_equivalent,
    iter_words,
)
from .learner import (
    BUDGET_EXHAUSTED,
    EQUIVALENT_IN_DOMAIN,
    HYPOTHESIS_READY,
    CheckpointMismatchError,
    LearnResult,
    Learner,
)
from .oracles import (
    BoundedEquivalenceOracle,
    DomainLimitError,
    MembershipOracle,
)

__all__ = [
    "BUDGET_EXHAUSTED",
    "BoundedEquivalenceOracle",
    "CheckpointMismatchError",
    "DFA",
    "DomainLimitError",
    "EQUIVALENT_IN_DOMAIN",
    "HYPOTHESIS_READY",
    "LearnResult",
    "Learner",
    "MembershipOracle",
    "brute_force_counterexample",
    "domain_equivalent",
    "iter_words",
]

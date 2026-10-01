"""DFA minimization with certificates, witnesses and incremental repair."""

from .core import (
    DEAD_STATE,
    Certificate,
    DFA,
    build_dfa,
    canonical_form,
    complete,
    minimize,
    prepare,
    reachable_states,
    refine_partition,
    shortest_distinguishing_strings,
    trim,
)
from .incremental import IncrementalMinimizer, RepairReport
from .verify import Verification, counterexample_script, product_verify, states_equivalent

__all__ = [
    "DEAD_STATE",
    "Certificate",
    "DFA",
    "IncrementalMinimizer",
    "RepairReport",
    "Verification",
    "build_dfa",
    "canonical_form",
    "complete",
    "counterexample_script",
    "minimize",
    "prepare",
    "product_verify",
    "reachable_states",
    "refine_partition",
    "shortest_distinguishing_strings",
    "states_equivalent",
    "trim",
]

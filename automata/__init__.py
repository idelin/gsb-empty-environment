"""Intersection-of-automata counting, k-th word, ranking and enumeration.

Public API:

* :class:`Automaton`        -- explicitly constructed, versioned DFA;
* :class:`ProductAutomaton` -- lazy (on-demand) product of DFAs;
* :class:`Counter`          -- exact / modular counting, kth, rank;
* :class:`LexEnumerator`    -- budgeted resumable lexicographic enumeration;
* :func:`find_infinite_evidence` / :func:`verify_evidence` -- infinitude.
"""

from .core import Automaton, ProductAutomaton
from .counting import Counter
from .enumerate import Checkpoint, LexEnumerator
from .infinite import CycleEvidence, find_infinite_evidence, verify_evidence

__all__ = [
    "Automaton",
    "ProductAutomaton",
    "Counter",
    "Checkpoint",
    "LexEnumerator",
    "CycleEvidence",
    "find_infinite_evidence",
    "verify_evidence",
]

"""Multi-DFA intersection: interval counting, k-th string/rank, infinitude."""

from .counters import ExactCounter, ModCounter
from .dfa import DFA, accepts_all
from .enumeration import Checkpoint, StringEnumerator
from .errors import AutomataError, CheckpointError, UnsupportedModeError
from .witness import InfiniteWitness

__all__ = [
    "DFA",
    "ExactCounter",
    "ModCounter",
    "Checkpoint",
    "StringEnumerator",
    "InfiniteWitness",
    "AutomataError",
    "CheckpointError",
    "UnsupportedModeError",
    "accepts_all",
]

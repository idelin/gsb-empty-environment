"""iterdrive: generic checkpointed iteration driver (stdlib only)."""

from .checkpoint import (
    CheckpointCorrupt,
    CheckpointError,
    CheckpointStore,
    InputMismatch,
)
from .control import ControlChannel
from .driver import IterationDriver, RunResult, StopReason
from .iterators import ConjugateGradientIterator, RichardsonIterator
from .problems import poisson_problem, reference_solve

__all__ = [
    "CheckpointCorrupt",
    "CheckpointError",
    "CheckpointStore",
    "ConjugateGradientIterator",
    "ControlChannel",
    "InputMismatch",
    "IterationDriver",
    "RichardsonIterator",
    "RunResult",
    "StopReason",
    "poisson_problem",
    "reference_solve",
]

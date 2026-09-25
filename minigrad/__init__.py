"""minigrad: a tiny pure-stdlib autograd engine.

Features: reverse-mode autodiff over independent graphs, retain/release
lifecycle, version-counter detection of in-place mutation, atomic
gradient transactions (rollback on failure or cancellation), recompute
nodes bound to input versions and random keys, and pin-counted storage
that survives release while other graphs still reference it.
"""

from .errors import (
    AutogradError,
    BackwardCancelled,
    GraphReleasedError,
    GraphStructureError,
    StaleInputError,
)
from .graph import CancelToken, Graph
from .tensor import Tensor
from . import ops

__all__ = [
    "AutogradError", "BackwardCancelled", "GraphReleasedError",
    "GraphStructureError", "StaleInputError",
    "CancelToken", "Graph", "Tensor", "ops",
]

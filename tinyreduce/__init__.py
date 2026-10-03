"""tinyreduce: multi-axis reductions with gradients, standard library only."""
from .tensor import Tensor
from .reduce import (SUM, MEAN, MIN, MAX, OPS, TIE_UNIFORM, TIE_FIRST,
                     ReductionResult, WorkspaceTracker, GradStore,
                     UndefinedReductionError, NonDifferentiableError,
                     normalize_axes, reduce, backward)

__all__ = ["Tensor", "SUM", "MEAN", "MIN", "MAX", "OPS", "TIE_UNIFORM",
           "TIE_FIRST", "ReductionResult", "WorkspaceTracker", "GradStore",
           "UndefinedReductionError", "NonDifferentiableError",
           "normalize_axes", "reduce", "backward"]

"""tinyreduce -- pure-stdlib multi-axis reductions with gradients."""
from .tensor import Tensor, Storage
from .errors import (ReductionError, AxisError, EmptyReductionError,
                     NonDifferentiableError)
from .ops import (ReductionStats, normalize_axes,
                  reduce_sum, reduce_mean, reduce_min, reduce_max,
                  value_and_grad)

__all__ = ["Tensor", "Storage", "ReductionError", "AxisError",
           "EmptyReductionError", "NonDifferentiableError",
           "ReductionStats", "normalize_axes",
           "reduce_sum", "reduce_mean", "reduce_min", "reduce_max",
           "value_and_grad"]

"""tinymat: strided-view batched matmul with VJP/JVP/HVP (stdlib only)."""
from .tensor import Tensor, default_strides, iter_indices
from .matmul import (
    DEFAULT_WORKSPACE_BUDGET,
    add,
    broadcast_shape,
    inner,
    matmul,
    matmul_backward,
    matmul_hvp,
    matmul_jvp,
)

__all__ = [
    "Tensor", "default_strides", "iter_indices",
    "DEFAULT_WORKSPACE_BUDGET", "add", "broadcast_shape", "inner",
    "matmul", "matmul_backward", "matmul_jvp", "matmul_hvp",
]

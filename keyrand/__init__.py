"""keyrand：显式键驱动随机张量库（纯标准库实现）。"""
from .prng import Key, fold_in, root_key, split
from .tensor import Tensor
from .random import bernoulli_tensor, normal_tensor, uniform_tensor
from .nn import (
    Checkpoint,
    dropout_backward,
    dropout_forward,
    gaussian_reparam_backward,
    gaussian_reparam_sample,
)
from .graph import Executor, Graph, KeyConflictError
from .batch import batched_map, sample_key

__all__ = [
    "Key", "root_key", "split", "fold_in", "Tensor",
    "uniform_tensor", "normal_tensor", "bernoulli_tensor",
    "dropout_forward", "dropout_backward",
    "gaussian_reparam_sample", "gaussian_reparam_backward", "Checkpoint",
    "Graph", "Executor", "KeyConflictError",
    "batched_map", "sample_key",
]

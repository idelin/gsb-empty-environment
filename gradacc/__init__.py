"""Gradient-accumulation trainer: pure standard library, CPU, no autodiff."""

from .accumulator import BatchStatus, CommitResult, GradientAccumulator
from .coordinator import Coordinator
from .model import forward, init_params, loss_and_grad
from .shard import Decision, Shard
from .tensor import Tensor
from .trainer import Trainer

__all__ = [
    "BatchStatus",
    "CommitResult",
    "Coordinator",
    "Decision",
    "GradientAccumulator",
    "Shard",
    "Tensor",
    "Trainer",
    "forward",
    "init_params",
    "loss_and_grad",
]

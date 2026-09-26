"""keyrng: explicit key-driven randomness for small CPU tensors.

Public API:
    PRNGKey, key, split, fold_in, random_bits, KeyRegistry, KeyCollisionError
    Tensor
    uniform, normal, bernoulli
    dropout, dropout_backward, dropout_mask,
    dropout_checkpoint_forward, dropout_checkpoint_backward, DropoutCheckpoint,
    reparameterized_normal, ReparamNormalSample
    Node, Executor, topo_sort
    sample_key, map_samples, map_samples_chunked
"""

from .batch import map_samples, map_samples_chunked, sample_key
from .distributions import bernoulli, normal, uniform
from .graph import Executor, Node, topo_sort
from .layers import (
    DropoutCheckpoint,
    ReparamNormalSample,
    dropout,
    dropout_backward,
    dropout_checkpoint_backward,
    dropout_checkpoint_forward,
    dropout_mask,
    reparameterized_normal,
)
from .prng import (
    KeyCollisionError,
    KeyRegistry,
    PRNGKey,
    fold_in,
    key,
    random_bits,
    split,
)
from .tensor import Tensor

__all__ = [
    "PRNGKey",
    "key",
    "split",
    "fold_in",
    "random_bits",
    "KeyRegistry",
    "KeyCollisionError",
    "Tensor",
    "uniform",
    "normal",
    "bernoulli",
    "dropout",
    "dropout_backward",
    "dropout_mask",
    "dropout_checkpoint_forward",
    "dropout_checkpoint_backward",
    "DropoutCheckpoint",
    "reparameterized_normal",
    "ReparamNormalSample",
    "Node",
    "Executor",
    "topo_sort",
    "sample_key",
    "map_samples",
    "map_samples_chunked",
]

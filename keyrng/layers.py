"""Layers built on explicit keys: dropout, checkpointed dropout, and
reparameterised sampling with hand-written (exact) gradients.

Dropout convention (inverted dropout):
    keep = 1 - rate
    mask_i = 1/keep  if u_i < keep   else 0.0,   u_i ~ U[0,1) drawn from key
    y = x * mask                       (forward)
    dx = dy * mask                     (backward: gradient flows only
                                        through kept units, scaled 1/keep)

Checkpointed dropout stores only the key (plus shape/rate), not the mask;
the backward pass recomputes the mask from the key.  Because sampling is
a pure function of the key, the recomputed mask is bit-identical to the
one used in the forward pass -- it is never re-drawn.
"""

from __future__ import annotations

from dataclasses import dataclass

from .distributions import bernoulli, normal
from .prng import PRNGKey
from .tensor import Tensor


def _check_rate(rate: float) -> None:
    if not (0.0 <= rate < 1.0):
        raise ValueError(f"dropout rate must be in [0, 1), got {rate}")


def dropout_mask(k: PRNGKey, shape, rate: float) -> Tensor:
    """The inverted-dropout mask: entries are 1/(1-rate) or 0.0."""
    _check_rate(rate)
    keep = 1.0 - rate
    scale = 1.0 / keep
    return bernoulli(k, keep, shape).map(lambda b: scale if b else 0.0)


def dropout(x: Tensor, k: PRNGKey, rate: float) -> tuple[Tensor, Tensor]:
    """Forward dropout; returns (output, mask) for the backward pass."""
    mask = dropout_mask(k, x.shape, rate)
    return x * mask, mask


def dropout_backward(dy: Tensor, mask: Tensor) -> Tensor:
    """Gradient of dropout w.r.t. its input: dx = dy * mask."""
    return dy * mask


@dataclass(frozen=True)
class DropoutCheckpoint:
    """Recomputation token: everything needed to rebuild the mask."""

    key: PRNGKey
    shape: tuple[int, ...]
    rate: float

    def recompute_mask(self) -> Tensor:
        """Bit-identical to the mask used in the forward pass."""
        return dropout_mask(self.key, self.shape, self.rate)


def dropout_checkpoint_forward(
    x: Tensor, k: PRNGKey, rate: float
) -> tuple[Tensor, DropoutCheckpoint]:
    """Forward pass that retains only the key, not the mask."""
    token = DropoutCheckpoint(k, x.shape, rate)
    return x * token.recompute_mask(), token


def dropout_checkpoint_backward(dy: Tensor, token: DropoutCheckpoint) -> Tensor:
    """Backward pass: recompute the mask from the stored key, then apply it."""
    return dy * token.recompute_mask()


@dataclass(frozen=True)
class ReparamNormalSample:
    """y = mean + std * z with z ~ N(0,1) drawn from an explicit key.

    Gradients (reparameterisation trick, exact):
        dL/dmean = sum(dy)
        dL/dstd  = sum(dy * z)
    """

    value: Tensor
    z: Tensor

    def backward(self, dy: Tensor) -> tuple[float, float]:
        return dy.sum(), (dy * self.z).sum()


def reparameterized_normal(
    k: PRNGKey, shape, mean: float, std: float
) -> ReparamNormalSample:
    """Sample N(mean, std**2) differentiably via the reparameterisation trick."""
    z = normal(k, shape)  # standard normal, pure function of the key
    return ReparamNormalSample(value=z * std + mean, z=z)

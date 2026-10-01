"""Tiny linear regression model with hand-written forward/backward passes.

No autodiff, no tensor libraries: gradients are derived by hand for
mean-squared error over a list of (x, target) samples.
"""

from __future__ import annotations

import random

from .tensor import Tensor

PARAM_NAMES = ("w", "b")


def init_params(in_dim, out_dim, seed=0):
    rng = random.Random(seed)
    w = Tensor([rng.uniform(-0.5, 0.5) for _ in range(out_dim * in_dim)],
               (out_dim, in_dim))
    b = Tensor([rng.uniform(-0.5, 0.5) for _ in range(out_dim)], (out_dim,))
    return {"w": w, "b": b}


def forward(params, x):
    w, b = params["w"], params["b"]
    out_dim, in_dim = w.shape
    if len(x) != in_dim:
        raise ValueError(f"expected input dim {in_dim}, got {len(x)}")
    return [sum(w.data[o * in_dim + i] * x[i] for i in range(in_dim)) + b.data[o]
            for o in range(out_dim)]


def loss_and_grad(params, samples):
    """Mean squared error over `samples`; returns (loss, grads).

    grads are the mean over the given samples (one micro-batch), so callers
    aggregating several micro-batches must weight by sample count.
    """
    if not samples:
        raise ValueError("empty sample list")
    w, b = params["w"], params["b"]
    out_dim, in_dim = w.shape
    gw = Tensor.zeros(w.shape)
    gb = Tensor.zeros(b.shape)
    total_loss = 0.0
    for x, target in samples:
        pred = forward(params, x)
        err = [pred[o] - target[o] for o in range(out_dim)]
        total_loss += sum(e * e for e in err)
        for o in range(out_dim):
            gb.data[o] += 2.0 * err[o]
            for i in range(in_dim):
                gw.data[o * in_dim + i] += 2.0 * err[o] * x[i]
    n = float(len(samples))
    return total_loss / n, {"w": gw.scale(1.0 / n), "b": gb.scale(1.0 / n)}

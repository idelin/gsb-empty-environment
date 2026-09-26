"""Independent numerical verification via central finite differences.

Uses only repeated forward evaluations (full-save mode, no backward pass), so
it is a genuinely independent check of the analytic gradients produced by the
engine's backward pass.
"""
from __future__ import annotations

from engine import Engine
from minitensor import Tensor


def scalar_output(nodes, inputs, seed=0):
    eng = Engine(nodes, seed=seed)
    all_ckpts = {n.name for n in nodes}
    out, _, _ = eng.run(inputs, all_ckpts)
    assert out.numel() == 1, "finite differences need a scalar output"
    return out[0]


def finite_difference_grad(nodes, inputs, target, eps=1e-5, seed=0):
    """Central-difference gradient of the scalar output w.r.t. ``inputs[target]``."""
    base = inputs[target]
    grad = Tensor.zeros(base.shape)
    for i in range(base.numel()):
        plus = {k: (v.clone() if k != target else v.clone()) for k, v in inputs.items()}
        minus = {k: v.clone() for k, v in inputs.items()}
        idx = plus[target].offset + i
        plus[target].storage.data[idx] += eps
        minus[target].storage.data[idx] -= eps
        f_plus = scalar_output(nodes, plus, seed=seed)
        f_minus = scalar_output(nodes, minus, seed=seed)
        grad.storage.data[i] = (f_plus - f_minus) / (2.0 * eps)
    return grad

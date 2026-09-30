"""SGD with momentum and staged (atomic) updates over pure-Python tensors.

The optimizer never mutates its live state while computing an update:
`compute_update` is a pure function of the current state, and `apply_update`
swaps the fully-computed new state in. A failed update therefore cannot
leave parameters or momentum buffers half-written.
"""

from __future__ import annotations

from tensor import Tensor


class NonFiniteGradientError(ValueError):
    """Raised when a gradient tensor contains NaN or +/-inf."""


class SGD:
    def __init__(self, params: dict, lr: float, momentum: float = 0.0):
        self.params = {name: value.copy() for name, value in params.items()}
        self.lr = float(lr)
        self.mu = float(momentum)
        self.momentum_buffers = {
            name: Tensor.zeros(len(value)) for name, value in params.items()
        }
        self.step_count = 0

    def compute_update(self, grads: dict):
        """Purely compute (new_params, new_momentum) without mutating state.

        Raises NonFiniteGradientError before anything is applied if any
        gradient component is NaN or infinite.
        """
        missing = set(self.params) - set(grads)
        if missing:
            raise KeyError(f"missing gradients for: {sorted(missing)}")
        for name, grad in grads.items():
            if name not in self.params:
                raise KeyError(f"gradient for unknown parameter {name!r}")
            if len(grad) != len(self.params[name]):
                raise ValueError(
                    f"gradient size mismatch for {name!r}: "
                    f"{len(grad)} != {len(self.params[name])}"
                )
            if not grad.is_finite():
                raise NonFiniteGradientError(
                    f"non-finite gradient for parameter {name!r}"
                )
        new_momentum = {}
        new_params = {}
        for name, param in self.params.items():
            buf = self.momentum_buffers[name].data
            grad = grads[name].data
            mom = [self.mu * b + g for b, g in zip(buf, grad)]
            new_momentum[name] = Tensor(mom)
            new_params[name] = Tensor(
                [p - self.lr * m for p, m in zip(param.data, mom)]
            )
        return new_params, new_momentum

    def apply_update(self, new_params: dict, new_momentum: dict) -> None:
        self.params = {n: t.copy() for n, t in new_params.items()}
        self.momentum_buffers = {n: t.copy() for n, t in new_momentum.items()}
        self.step_count += 1

    def snapshot(self) -> dict:
        return {
            "params": {n: list(t.data) for n, t in self.params.items()},
            "momentum_buffers": {
                n: list(t.data) for n, t in self.momentum_buffers.items()
            },
            "step_count": self.step_count,
            "lr": self.lr,
            "mu": self.mu,
        }

    @classmethod
    def from_snapshot(cls, snap: dict) -> "SGD":
        opt = cls(
            {n: Tensor(v) for n, v in snap["params"].items()},
            snap["lr"],
            snap["mu"],
        )
        opt.momentum_buffers = {
            n: Tensor(v) for n, v in snap["momentum_buffers"].items()
        }
        opt.step_count = snap["step_count"]
        return opt

"""Two iterative solvers with different internal state layouts.

Iterator protocol expected by IterationDriver:

* ``name``: str, bound into the input digest.
* ``state_dict()`` / ``load_state(state)``: serialize/restore the full
  internal state as JSON-compatible data.
* ``step(ctx) -> float``: advance exactly one iteration and return the
  residual norm. External (non-recomputable) callbacks must be invoked
  through ``ctx.invoke_external(name, fn)`` *before* mutating internal
  state, so the checkpoint persisted at callback-confirmation time
  always describes a consistent iteration boundary.

RichardsonIterator carries only the iterate ``x``;
ConjugateGradientIterator carries ``x, r, p, rs`` -- exercising the
protocol with genuinely different state shapes.
"""

import math

from .dense import dot, matvec, norm2, sub


class RichardsonIterator:
    """x_next = x + omega * (b - A x); omega comes from an external oracle."""

    name = "richardson"

    def __init__(self, n, a, b, omega_oracle, x0=None):
        self.n = n
        self.a = a
        self.b = b
        self._oracle = omega_oracle  # non-recomputable external callback
        self.x = list(x0) if x0 is not None else [0.0] * n

    def state_dict(self):
        return {"x": list(self.x)}

    def load_state(self, state):
        if len(state["x"]) != self.n:
            raise ValueError("state dimension mismatch")
        self.x = list(state["x"])

    def step(self, ctx):
        omega = ctx.invoke_external("omega", self._oracle)
        r = sub(self.n, self.b, matvec(self.n, self.a, self.x))
        self.x = [self.x[i] + omega * r[i] for i in range(self.n)]
        return norm2(self.n, r)


class ConjugateGradientIterator:
    """Standard CG for SPD systems; reports residuals to an external monitor."""

    name = "cg"

    def __init__(self, n, a, b, monitor, x0=None):
        self.n = n
        self.a = a
        self.b = b
        self._monitor = monitor  # non-recomputable external callback
        self.x = list(x0) if x0 is not None else [0.0] * n
        self.r = sub(n, b, matvec(n, a, self.x))
        self.p = list(self.r)
        self.rs = dot(n, self.r, self.r)

    def state_dict(self):
        return {
            "x": list(self.x),
            "r": list(self.r),
            "p": list(self.p),
            "rs": self.rs,
        }

    def load_state(self, state):
        for key in ("x", "r", "p"):
            if len(state[key]) != self.n:
                raise ValueError("state dimension mismatch")
        self.x = list(state["x"])
        self.r = list(state["r"])
        self.p = list(state["p"])
        self.rs = float(state["rs"])

    def step(self, ctx):
        n = self.n
        ctx.invoke_external(
            "monitor", lambda: self._monitor(ctx.iteration, math.sqrt(self.rs))
        )
        ap = matvec(n, self.a, self.p)
        alpha = self.rs / dot(n, self.p, ap)
        self.x = [self.x[i] + alpha * self.p[i] for i in range(n)]
        self.r = [self.r[i] - alpha * ap[i] for i in range(n)]
        rs_new = dot(n, self.r, self.r)
        beta = rs_new / self.rs
        self.p = [self.r[i] + beta * self.p[i] for i in range(n)]
        self.rs = rs_new
        return math.sqrt(rs_new)

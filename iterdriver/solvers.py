"""Two iterative solvers with deliberately different internal state.

* ``JacobiSolver``   -- state is a single vector ``x``.
* ``HeavyBallSolver`` -- state is two vectors ``x``, ``x_prev`` plus a
  local step counter, and each step consults a non-recomputable
  external step-size oracle through the driver's effect ledger.
"""
from __future__ import annotations

from .dense import (check_matrix, check_vector, matvec, residual_norm)


class JacobiSolver:
    """Damped Jacobi iteration for A x = b (square, nonzero diagonal)."""

    kind = "jacobi"

    def __init__(self, a, n_rows, n_cols, b, n, *, damping=1.0, x0=None):
        check_matrix(a, n_rows, n_cols)
        check_vector(b, n_rows)
        if n_rows != n_cols:
            raise ValueError("Jacobi requires a square matrix")
        for i in range(n_rows):
            if a[i][i] == 0.0:
                raise ValueError(f"zero diagonal entry at row {i}")
        self.a = a
        self.n = n_rows
        self.b = b
        self.damping = damping
        self.x = list(x0) if x0 is not None else [0.0] * n_rows
        check_vector(self.x, n_rows)

    def state_dict(self):
        return {"x": list(self.x)}

    def load_state_dict(self, state):
        x = list(state["x"])
        check_vector(x, self.n)
        self.x = x

    def step(self, effects):
        n = self.n
        ax = matvec(self.a, self.x, n, n)
        self.x = [self.x[i] + self.damping * (self.b[i] - ax[i])
                  / self.a[i][i] for i in range(n)]
        return residual_norm(self.a, self.x, self.b, n, n)


class HeavyBallSolver:
    """Heavy-ball (momentum) iteration with an external step-size oracle.

    The oracle models a non-recomputable external service (e.g. a
    hardware device or a remote policy). Its answer for each iteration
    is recorded in the checkpoint ledger; after a resume the recorded
    answer is replayed and the oracle is not called again for that
    iteration.
    """

    kind = "heavy-ball"

    def __init__(self, a, n_rows, n_cols, b, n, *, oracle, momentum=0.3,
                 x0=None):
        check_matrix(a, n_rows, n_cols)
        check_vector(b, n_rows)
        if n_rows != n_cols:
            raise ValueError("heavy-ball requires a square matrix")
        self.a = a
        self.n = n_rows
        self.b = b
        self.oracle = oracle
        self.momentum = momentum
        self.x = list(x0) if x0 is not None else [0.0] * n_rows
        check_vector(self.x, n_rows)
        self.x_prev = list(self.x)
        self.steps = 0

    def state_dict(self):
        return {"x": list(self.x),
                "x_prev": list(self.x_prev),
                "steps": self.steps}

    def load_state_dict(self, state):
        x = list(state["x"])
        x_prev = list(state["x_prev"])
        check_vector(x, self.n)
        check_vector(x_prev, self.n)
        self.x = x
        self.x_prev = x_prev
        self.steps = int(state["steps"])

    def step(self, effects):
        n = self.n
        ax = matvec(self.a, self.x, n, n)
        r = [self.b[i] - ax[i] for i in range(n)]
        alpha = effects.call(
            "step-size", {"step": self.steps},
            lambda payload: self.oracle(payload))
        beta = self.momentum
        x_new = [self.x[i] + alpha * r[i]
                 + beta * (self.x[i] - self.x_prev[i]) for i in range(n)]
        self.x_prev = self.x
        self.x = x_new
        self.steps += 1
        return residual_norm(self.a, self.x, self.b, n, n)

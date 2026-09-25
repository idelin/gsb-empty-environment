"""Independent dense reference implementation.

Written as a straightforward single-table loop, sharing no code with
the sharded implementation, so the two can cross-check each other
numerically. Implements the identical documented semantics:

* duplicate indices within a step are summed;
* weight decay applies only to touched rows;
* frozen rows never update;
* the step counter advances once per non-empty step.
"""

from minitensor import vec_zeros


class DenseReference:
    def __init__(self, weights, lr, beta, weight_decay, frozen_rows=()):
        self.w = [list(row) for row in weights]
        self.dim = len(weights[0])
        self.m = [vec_zeros(self.dim) for _ in weights]
        self.lr = lr
        self.beta = beta
        self.weight_decay = weight_decay
        self.frozen_rows = set(frozen_rows)
        self.step = 0

    def step_batch(self, micro_batches):
        """One optimization step from a list of (indices, grads)
        micro-batches."""
        merged = {}
        for indices, grads in micro_batches:
            for idx, g in zip(indices, grads):
                slot = merged.setdefault(idx, vec_zeros(self.dim))
                for j in range(self.dim):
                    slot[j] += g[j]
        touched = False
        for row, g in merged.items():
            if row in self.frozen_rows:
                continue
            touched = True
            w = self.w[row]
            m = self.m[row]
            for j in range(self.dim):
                gj = g[j] + self.weight_decay * w[j]
                m[j] = self.beta * m[j] + gj
                w[j] -= self.lr * m[j]
        if touched:
            self.step += 1

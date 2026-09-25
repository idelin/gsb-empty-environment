"""End-to-end sample: converge, cancel, resume, and reject stale checkpoints.

Run from the repository root:  python3.11 examples/run_sample.py
"""

import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from iterdrive import (
    CheckpointStore,
    ConjugateGradientIterator,
    ControlChannel,
    InputMismatch,
    IterationDriver,
    RichardsonIterator,
    poisson_problem,
    reference_solve,
)
from iterdrive.dense import norm2, sub


class OmegaOracle:
    """External, non-recomputable: the value depends on how often it was called."""

    def __init__(self):
        self.calls = 0

    def __call__(self):
        k = self.calls
        self.calls += 1
        return 0.35 + 0.05 * (k % 3)


def error_vs_direct(problem, x):
    n = problem["n"]
    return norm2(n, sub(n, x, reference_solve(problem)))


def main():
    n = 8
    problem, _ = poisson_problem(n)
    tmp = tempfile.mkdtemp(prefix="iterdrive-sample-")
    print(f"checkpoint dir: {tmp}\n")

    # 1) CG runs to convergence; verified against the dense direct solve.
    cg_store = CheckpointStore(Path(tmp) / "cg.json")
    cg = ConjugateGradientIterator(n, problem["a"], problem["b"],
                                   monitor=lambda k, r: f"ack-{k}")
    res = IterationDriver(cg_store).run(problem, cg, budget=500, tolerance=1e-12)
    print(f"[cg]         reason={res.reason.value:16s} iters={res.iterations:3d} "
          f"residual={res.residual_history[-1]:.3e} "
          f"err_vs_direct={error_vs_direct(problem, res.iterator_state['x']):.3e}")

    # 2) Richardson: continuous reference run with an external omega oracle.
    ref_oracle = OmegaOracle()
    ref_store = CheckpointStore(Path(tmp) / "richardson-continuous.json")
    ref = IterationDriver(ref_store).run(
        problem, RichardsonIterator(n, problem["a"], problem["b"], ref_oracle),
        budget=120, tolerance=1e-300,
    )
    print(f"[continuous] reason={ref.reason.value:16s} iters={ref.iterations:3d} "
          f"residual={ref.residual_history[-1]:.3e} oracle_calls={ref_oracle.calls}")

    # 3) Same run, but cancelled at iteration 50 and resumed from checkpoint.
    oracle = OmegaOracle()
    store = CheckpointStore(Path(tmp) / "richardson-resumed.json")
    control = ControlChannel()

    def canceller(k):
        if k == 50:
            control.request_cancel()

    first = IterationDriver(store, control).run(
        problem, RichardsonIterator(n, problem["a"], problem["b"], oracle),
        budget=120, tolerance=1e-300, observer=canceller,
    )
    print(f"[cancelled]  reason={first.reason.value:16s} iters={first.iterations:3d} "
          f"residual={first.residual_history[-1]:.3e} oracle_calls={oracle.calls}")

    second = IterationDriver(store, ControlChannel()).run(
        problem, RichardsonIterator(n, problem["a"], problem["b"], oracle),
        budget=120, tolerance=1e-300,
    )
    match = (second.residual_history == ref.residual_history
             and second.iterator_state == ref.iterator_state)
    print(f"[resumed]    reason={second.reason.value:16s} iters={second.iterations:3d} "
          f"residual={second.residual_history[-1]:.3e} oracle_calls={oracle.calls}")
    print(f"[resumed]    identical to continuous run: {match} "
          f"(side effects not repeated: {oracle.calls == ref_oracle.calls})")

    # 4) The input matrix changes -> the old checkpoint is rejected.
    changed = {"n": n, "a": [list(row) for row in problem["a"]],
               "b": list(problem["b"])}
    changed["a"][0][0] = 3.0
    try:
        IterationDriver(store).run(
            changed, RichardsonIterator(n, changed["a"], changed["b"], OmegaOracle()),
            budget=120, tolerance=1e-300,
        )
    except InputMismatch as exc:
        print(f"[stale]      checkpoint rejected after matrix change: {exc}")


if __name__ == "__main__":
    main()

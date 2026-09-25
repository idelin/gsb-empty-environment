"""End-to-end sample: cancel / resume a heavy-ball solve.

Demonstrates:
  * a solver stage with a non-recomputable external callback (oracle),
  * cancellation serviced at a safe point, checkpoint on cancel,
  * resume that replays confirmed oracle answers from the checkpoint
    ledger instead of calling the service again,
  * stop reasons (cancelled vs converged vs budget),
  * independent dense verification of the final iterate.

Run from the repository root:  python3 examples/run_sample.py
"""
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from iterdriver import (Budget, Driver, FileCheckpointStore,
                        HeavyBallSolver, JacobiSolver, input_digest,
                        residual_norm)

A = [[4.0, 1.0, 0.5, 0.0],
     [1.0, 3.5, 0.5, 0.25],
     [0.5, 0.5, 3.0, 0.5],
     [0.0, 0.25, 0.5, 2.5]]
B = [1.0, 2.0, 3.0, 4.0]
PARAMS = {"solver": "heavy-ball", "momentum": 0.3}
TOL = 1e-12


class StepSizeService:
    """External, non-recomputable step-size service.

    Its answer depends on private internal state (a call counter), so
    re-executing a solver stage would not reproduce earlier answers.
    """

    TABLE = [0.22, 0.18, 0.25, 0.20, 0.24, 0.19]

    def __init__(self):
        self.calls = 0

    def __call__(self, payload):
        value = self.TABLE[self.calls % len(self.TABLE)]
        self.calls += 1
        return value


class CancelAfter(HeavyBallSolver):
    """Posts a cancellation request once `cancel_after` steps complete."""

    def __init__(self, *args, driver, cancel_after, **kwargs):
        super().__init__(*args, **kwargs)
        self._driver = driver
        self._cancel_after = cancel_after

    def step(self, effects):
        residual = super().step(effects)
        if self.steps == self._cancel_after:
            self._driver.request_cancel()  # serviced at the safe point
        return residual


def main():
    digest = input_digest(A, 4, 4, B, 4, PARAMS)
    workdir = tempfile.mkdtemp(prefix="iterdriver-demo-")
    store = FileCheckpointStore(os.path.join(workdir, "run.ckpt.json"))
    service = StepSizeService()  # one external service across both legs

    print(f"checkpoint file: {store.path}")

    # ---- Leg 1: cancelled after 5 completed iterations ----
    driver1 = Driver(store=store, tolerance=TOL, budget=Budget(1000))
    solver1 = CancelAfter(A, 4, 4, B, 4, oracle=service, momentum=0.3,
                          driver=driver1, cancel_after=5)
    leg1 = driver1.run(solver1, digest=digest)
    print(f"leg 1: stop={leg1.stop_reason.value} "
          f"iterations={leg1.iterations} "
          f"residual={leg1.residual_history[-1]:.3e} "
          f"service_calls={service.calls}")

    # ---- Leg 2: resume from the checkpoint ----
    solver2 = HeavyBallSolver(A, 4, 4, B, 4, oracle=service, momentum=0.3)
    driver2 = Driver(store=store, tolerance=TOL, budget=Budget(1000))
    leg2 = driver2.run(solver2, digest=digest, resume_from=store.load())
    print(f"leg 2: stop={leg2.stop_reason.value} "
          f"iterations={leg2.iterations} "
          f"residual={leg2.residual_history[-1]:.3e} "
          f"service_calls={service.calls}")

    confirmed = leg2.checkpoint.callback_ledger
    print(f"ledger entries (confirmed side effects): {len(confirmed)}")
    print(f"  replayed on resume: 0..4, "
          f"newly confirmed: 5..{leg2.iterations - 1}")
    print(f"  no residual record duplicated: "
          f"{len(leg2.residual_history) == leg2.iterations}")

    # ---- Independent dense verification of the final iterate ----
    x = solver2.state_dict()["x"]
    check = residual_norm(A, x, B, 4, 4)
    print(f"dense verification ||b - A x|| = {check:.3e} "
          f"(tolerance {TOL:.0e})")
    print("final iterate x = [" + ", ".join(f"{v:.8f}" for v in x) + "]")

    # ---- Reference: continuous Jacobi run for comparison ----
    jacobi = JacobiSolver(A, 4, 4, B, 4, damping=0.9)
    jdigest = input_digest(A, 4, 4, B, 4,
                           {"solver": "jacobi", "damping": 0.9})
    ref = Driver(tolerance=TOL, budget=Budget(1000)).run(
        jacobi, digest=jdigest)
    print(f"jacobi reference: stop={ref.stop_reason.value} "
          f"iterations={ref.iterations}")


if __name__ == "__main__":
    main()

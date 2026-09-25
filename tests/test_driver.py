"""Protocol validation and fault-injection tests for iterdriver."""
from __future__ import annotations

import json
import os
import shutil
import tempfile
import threading
import unittest

from iterdriver import (Budget, CheckpointCorruptError, CheckpointError,
                        CheckpointInputMismatch, Driver,
                        FileCheckpointStore, HeavyBallSolver,
                        JacobiSolver, ShapeError, StopReason,
                        input_digest, residual_norm)

TOL = 1e-12
ALPHAS = [0.22, 0.18, 0.25, 0.20, 0.24, 0.19]
HB_PARAMS = {"solver": "heavy-ball", "momentum": 0.3}
JACOBI_PARAMS = {"solver": "jacobi", "damping": 0.9}


def make_system():
    a = [[4.0, 1.0, 0.5, 0.0],
         [1.0, 3.5, 0.5, 0.25],
         [0.5, 0.5, 3.0, 0.5],
         [0.0, 0.25, 0.5, 2.5]]
    b = [1.0, 2.0, 3.0, 4.0]
    return a, b


class SequenceOracle:
    """Non-recomputable external service: answer depends on call count."""

    def __init__(self, table):
        self._table = table
        self.calls = 0

    def __call__(self, payload):
        value = self._table[self.calls % len(self._table)]
        self.calls += 1
        return value


class CancellingHeavyBall(HeavyBallSolver):
    """Requests cancellation from inside a step (serviced at safe point)."""

    def __init__(self, *args, driver, cancel_after, **kwargs):
        super().__init__(*args, **kwargs)
        self._driver = driver
        self._cancel_after = cancel_after

    def step(self, effects):
        residual = super().step(effects)
        if self.steps == self._cancel_after:
            self._driver.request_cancel()
        return residual


class FlakyStore(FileCheckpointStore):
    """Crashes mid-write on the Nth save, leaving a partial temp file."""

    def __init__(self, path, fail_on_save):
        super().__init__(path)
        self._fail_on_save = fail_on_save
        self._saves = 0

    def _write_all(self, fh, data):
        self._saves += 1
        if self._saves == self._fail_on_save:
            fh.write(data[: len(data) // 3])
            fh.flush()
            raise OSError("injected crash during checkpoint save")
        super()._write_all(fh, data)


class CountingStore(FileCheckpointStore):
    def __init__(self, path):
        super().__init__(path)
        self.saves = 0

    def save(self, checkpoint):
        self.saves += 1
        super().save(checkpoint)


class DriverTestCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="iterdriver-test-")
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.a, self.b = make_system()

    def path(self, name="checkpoint.json"):
        return os.path.join(self.tmp, name)

    def jacobi(self, **kwargs):
        kwargs.setdefault("damping", 0.9)
        return JacobiSolver(self.a, 4, 4, self.b, 4, **kwargs)

    def heavy_ball(self, oracle, **kwargs):
        kwargs.setdefault("momentum", 0.3)
        return HeavyBallSolver(self.a, 4, 4, self.b, 4,
                               oracle=oracle, **kwargs)

    def digest(self, params):
        return input_digest(self.a, 4, 4, self.b, 4, params)


class TestConvergence(DriverTestCase):
    def test_jacobi_converges_and_dense_check_passes(self):
        solver = self.jacobi()
        result = Driver(tolerance=TOL, budget=Budget(1000)).run(
            solver, digest=self.digest(JACOBI_PARAMS))
        self.assertEqual(result.stop_reason, StopReason.CONVERGED)
        self.assertEqual(result.iterations, len(result.residual_history))
        x = solver.state_dict()["x"]
        # Independent dense verification of the returned iterate.
        self.assertLessEqual(residual_norm(self.a, x, self.b, 4, 4), TOL)

    def test_heavy_ball_converges_and_dense_check_passes(self):
        oracle = SequenceOracle(ALPHAS)
        solver = self.heavy_ball(oracle)
        result = Driver(tolerance=TOL, budget=Budget(2000)).run(
            solver, digest=self.digest(HB_PARAMS))
        self.assertEqual(result.stop_reason, StopReason.CONVERGED)
        x = solver.state_dict()["x"]
        self.assertLessEqual(residual_norm(self.a, x, self.b, 4, 4), TOL)
        self.assertEqual(oracle.calls, result.iterations)


class TestCancelResumeConsistency(DriverTestCase):
    def test_cancel_resume_matches_continuous_run(self):
        digest = self.digest(HB_PARAMS)

        # Continuous reference run.
        oracle_ref = SequenceOracle(ALPHAS)
        solver_ref = self.heavy_ball(oracle_ref)
        ref = Driver(tolerance=TOL, budget=Budget(2000)).run(
            solver_ref, digest=digest)
        self.assertEqual(ref.stop_reason, StopReason.CONVERGED)

        # The external oracle is a singleton service whose internal
        # state persists across restarts; both legs talk to the same
        # service instance.
        service = SequenceOracle(ALPHAS)

        # Leg 1: cancelled after 7 completed iterations.
        store = FileCheckpointStore(self.path())
        driver1 = Driver(store=store, tolerance=TOL, budget=Budget(2000))
        solver1 = CancellingHeavyBall(self.a, 4, 4, self.b, 4,
                                      oracle=service, momentum=0.3,
                                      driver=driver1, cancel_after=7)
        leg1 = driver1.run(solver1, digest=digest)
        self.assertEqual(leg1.stop_reason, StopReason.CANCELLED)
        self.assertEqual(leg1.iterations, 7)
        self.assertEqual(service.calls, 7)

        # Leg 2: resume from the checkpoint with a fresh solver.
        solver2 = self.heavy_ball(service)
        driver2 = Driver(store=store, tolerance=TOL, budget=Budget(2000))
        leg2 = driver2.run(solver2, digest=digest,
                           resume_from=store.load())

        self.assertEqual(leg2.stop_reason, StopReason.CONVERGED)
        # Identical trajectory to the continuous run.
        self.assertEqual(leg2.iterations, ref.iterations)
        self.assertEqual(leg2.residual_history, ref.residual_history)
        self.assertEqual(solver2.state_dict(), solver_ref.state_dict())
        # No duplicated residual records for completed iterations.
        self.assertEqual(len(leg2.residual_history), leg2.iterations)
        self.assertEqual(leg2.residual_history[:7], leg1.residual_history)
        # Confirmed side effects are replayed from the ledger, never
        # re-invoked: the service saw each iteration exactly once.
        self.assertEqual(service.calls, ref.iterations)

    def test_callback_results_are_checkpoint_evidence(self):
        store = FileCheckpointStore(self.path())
        oracle = SequenceOracle(ALPHAS)
        solver = self.heavy_ball(oracle)
        Driver(store=store, tolerance=TOL, budget=Budget(5)).run(
            solver, digest=self.digest(HB_PARAMS))
        ledger = store.load().callback_ledger
        self.assertEqual(len(ledger), 5)
        for k in range(5):
            entry = ledger[f"{k}:step-size"]
            self.assertEqual(entry["payload"], {"step": k})
            self.assertEqual(entry["result"], ALPHAS[k % len(ALPHAS)])


class TestStopReasons(DriverTestCase):
    def test_budget_exhaustion_then_resume(self):
        digest = self.digest(JACOBI_PARAMS)
        store = FileCheckpointStore(self.path())

        solver1 = self.jacobi()
        leg1 = Driver(store=store, tolerance=TOL, budget=Budget(3)).run(
            solver1, digest=digest)
        self.assertEqual(leg1.stop_reason, StopReason.BUDGET_EXHAUSTED)
        self.assertEqual(leg1.iterations, 3)
        self.assertEqual(store.load().iteration, 3)

        solver2 = self.jacobi()
        leg2 = Driver(store=store, tolerance=TOL,
                      budget=Budget(1000)).run(
            solver2, digest=digest, resume_from=store.load())
        self.assertEqual(leg2.stop_reason, StopReason.CONVERGED)
        self.assertEqual(leg2.residual_history[:3], leg1.residual_history)
        self.assertEqual(len(leg2.residual_history), leg2.iterations)

    def test_zero_budget_stops_immediately(self):
        solver = self.jacobi()
        result = Driver(tolerance=TOL, budget=Budget(0)).run(
            solver, digest=self.digest(JACOBI_PARAMS))
        self.assertEqual(result.stop_reason, StopReason.BUDGET_EXHAUSTED)
        self.assertEqual(result.iterations, 0)


class TestConcurrentRequests(DriverTestCase):
    def test_budget_topup_checkpoint_and_cancel_at_safe_points(self):
        digest = self.digest(JACOBI_PARAMS)
        at_second = threading.Event()
        gate = threading.Event()
        at_sixth = threading.Event()
        gate_two = threading.Event()
        solver = self.jacobi()
        hooked_step = solver.step
        done = {"n": 0}

        def step(effects):
            residual = hooked_step(effects)
            done["n"] += 1
            if done["n"] == 2:
                at_second.set()
                # Block mid-iteration until the controller has fired its
                # concurrent requests; they must apply at the safe point.
                self.assertTrue(gate.wait(timeout=5))
            if done["n"] == 6:
                at_sixth.set()
                # Hold the iteration boundary until the cancellation has
                # been posted, so the stop is observed deterministically.
                self.assertTrue(gate_two.wait(timeout=5))
            return residual

        solver.step = step
        budget = Budget(3)  # would stop after 3 without the top-up
        store = CountingStore(self.path())
        driver = Driver(store=store, tolerance=1e-18, budget=budget)
        outcome = {}

        def controller():
            self.assertTrue(at_second.wait(timeout=5))
            budget.add(50)
            driver.request_checkpoint()
            gate.set()
            self.assertTrue(at_sixth.wait(timeout=5))
            driver.request_cancel()
            gate_two.set()

        def run():
            outcome["result"] = driver.run(solver, digest=digest)

        worker = threading.Thread(target=run)
        worker.start()
        controller()
        worker.join(timeout=5)

        result = outcome["result"]
        self.assertEqual(result.stop_reason, StopReason.CANCELLED)
        self.assertEqual(result.iterations, 6)
        # Explicit request + final cancel save both happened.
        self.assertGreaterEqual(store.saves, 2)
        self.assertEqual(store.load().iteration, result.iterations)


class TestFaultInjection(DriverTestCase):
    def test_corrupted_checkpoint_is_rejected(self):
        store = FileCheckpointStore(self.path())
        Driver(store=store, tolerance=TOL, budget=Budget(2)).run(
            self.jacobi(), digest=self.digest(JACOBI_PARAMS))

        with open(self.path(), "wb") as fh:  # truncated garbage
            fh.write(b'{"sha256": "abc", "payload": {"format_')
        with self.assertRaises(CheckpointCorruptError):
            store.load()

        blob = {"sha256": "0" * 64, "payload": {"format_version": 1}}
        with open(self.path(), "wb") as fh:  # valid JSON, bad checksum
            fh.write(json.dumps(blob).encode())
        with self.assertRaises(CheckpointCorruptError):
            store.load()

    def test_input_change_invalidates_checkpoint(self):
        store = FileCheckpointStore(self.path())
        Driver(store=store, tolerance=TOL, budget=Budget(3)).run(
            self.jacobi(), digest=self.digest(JACOBI_PARAMS))

        a2 = [row[:] for row in self.a]
        a2[0][0] += 1.0e-9  # matrix changed after the checkpoint
        digest2 = input_digest(a2, 4, 4, self.b, 4, JACOBI_PARAMS)
        solver2 = JacobiSolver(a2, 4, 4, self.b, 4, damping=0.9)
        with self.assertRaises(CheckpointInputMismatch):
            Driver(store=store, tolerance=TOL, budget=Budget(100)).run(
                solver2, digest=digest2, resume_from=store.load())

    def test_interrupted_save_preserves_previous_checkpoint(self):
        store = FlakyStore(self.path(), fail_on_save=3)
        solver = self.jacobi()
        with self.assertRaises(OSError):
            Driver(store=store, tolerance=1e-18, budget=Budget(10),
                   autosave_interval=1).run(
                solver, digest=self.digest(JACOBI_PARAMS))
        # The crash on save #3 left the save-#2 checkpoint intact.
        self.assertEqual(store.load().iteration, 2)

    def test_stray_temp_file_does_not_affect_load(self):
        store = FileCheckpointStore(self.path())
        Driver(store=store, tolerance=TOL, budget=Budget(4)).run(
            self.jacobi(), digest=self.digest(JACOBI_PARAMS))
        stray = os.path.join(self.tmp, ".ckpt-leftover.tmp")
        with open(stray, "wb") as fh:
            fh.write(b"partial garbage from a crashed process")
        self.assertEqual(store.load().iteration, 4)

    def test_solver_kind_mismatch_is_rejected(self):
        store = FileCheckpointStore(self.path())
        digest = self.digest(JACOBI_PARAMS)
        Driver(store=store, tolerance=TOL, budget=Budget(2)).run(
            self.jacobi(), digest=digest)
        oracle = SequenceOracle(ALPHAS)
        wrong_solver = HeavyBallSolver(self.a, 4, 4, self.b, 4,
                                       oracle=oracle)
        with self.assertRaises(CheckpointError):
            Driver(store=store, tolerance=TOL, budget=Budget(10)).run(
                wrong_solver, digest=digest, resume_from=store.load())


class TestInputDigest(DriverTestCase):
    def test_digest_tracks_every_input(self):
        base = self.digest(JACOBI_PARAMS)
        a2 = [row[:] for row in self.a]
        a2[2][3] += 1.0
        self.assertNotEqual(base, input_digest(a2, 4, 4, self.b, 4,
                                               JACOBI_PARAMS))
        b2 = list(self.b)
        b2[1] += 1.0
        self.assertNotEqual(base, input_digest(self.a, 4, 4, b2, 4,
                                               JACOBI_PARAMS))
        other_params = dict(JACOBI_PARAMS, damping=1.0)
        self.assertNotEqual(base, input_digest(self.a, 4, 4, self.b, 4,
                                               other_params))

    def test_explicit_sizes_are_validated(self):
        with self.assertRaises(ShapeError):
            input_digest(self.a, 3, 4, self.b, 4)
        with self.assertRaises(ShapeError):
            input_digest(self.a, 4, 4, self.b, 3)
        with self.assertRaises(ShapeError):
            JacobiSolver(self.a, 4, 4, self.b, 4, x0=[0.0, 0.0])


if __name__ == "__main__":
    unittest.main()

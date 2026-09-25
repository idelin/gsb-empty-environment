import json
import os
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from iterdrive import (
    CheckpointCorrupt,
    CheckpointStore,
    ConjugateGradientIterator,
    ControlChannel,
    InputMismatch,
    IterationDriver,
    RichardsonIterator,
    StopReason,
    poisson_problem,
    reference_solve,
)
from iterdrive.dense import norm2, sub

N = 6
TOL = 1e-8
BUDGET = 2000
NEVER = -1.0  # tolerance sentinel: convergence can never trigger

# CG converges in at most n steps in exact arithmetic, so CG protocol tests
# use a problem larger than the iteration budget to stay mid-convergence.
CG_N = 64


class OmegaOracle:
    """Non-recomputable external callback: result depends on call count."""

    def __init__(self):
        self.calls = 0

    def __call__(self):
        k = self.calls
        self.calls += 1
        return 0.35 + 0.05 * (k % 3)


class Monitor:
    """Non-recomputable external callback: acks are sequenced."""

    def __init__(self):
        self.records = []

    def __call__(self, iteration, residual):
        self.records.append((iteration, residual))
        return f"ack-{len(self.records)}"


def make_richardson(problem, oracle):
    return RichardsonIterator(problem["n"], problem["a"], problem["b"], oracle)


def make_cg(problem, monitor):
    return ConjugateGradientIterator(problem["n"], problem["a"], problem["b"], monitor)


class DriverTestCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.problem, self.x_true = poisson_problem(N)

    def store(self, name="checkpoint.json"):
        return CheckpointStore(self.dir / name)

    def problem_for(self, make_iter):
        if make_iter is make_cg:
            problem, _ = poisson_problem(CG_N)
            return problem
        return self.problem

    # -- convergence verified against self-written dense solve -------------

    def test_richardson_converges_matches_dense(self):
        oracle = OmegaOracle()
        driver = IterationDriver(self.store())
        result = driver.run(
            self.problem, make_richardson(self.problem, oracle),
            budget=BUDGET, tolerance=TOL,
        )
        self.assertIs(result.reason, StopReason.CONVERGED)
        self.assertLessEqual(result.residual_history[-1], TOL)
        x_ref = reference_solve(self.problem)
        err = norm2(N, sub(N, result.iterator_state["x"], x_ref))
        self.assertLess(err, 1e-5)
        self.assertEqual(oracle.calls, result.iterations)

    def test_cg_converges_matches_dense(self):
        monitor = Monitor()
        driver = IterationDriver(self.store())
        result = driver.run(
            self.problem, make_cg(self.problem, monitor),
            budget=BUDGET, tolerance=1e-12,
        )
        self.assertIs(result.reason, StopReason.CONVERGED)
        x_ref = reference_solve(self.problem)
        err = norm2(N, sub(N, result.iterator_state["x"], x_ref))
        self.assertLess(err, 1e-8)
        self.assertEqual(len(monitor.records), result.iterations)

    # -- stop reasons -------------------------------------------------------

    def test_stop_reason_budget(self):
        driver = IterationDriver(self.store())
        result = driver.run(
            self.problem, make_richardson(self.problem, OmegaOracle()),
            budget=10, tolerance=NEVER,
        )
        self.assertIs(result.reason, StopReason.BUDGET_EXHAUSTED)
        self.assertEqual(result.iterations, 10)
        self.assertEqual(len(result.residual_history), 10)

    def test_stop_reason_cancel(self):
        control = ControlChannel()
        control.request_cancel()
        driver = IterationDriver(self.store(), control)
        result = driver.run(
            self.problem, make_richardson(self.problem, OmegaOracle()),
            budget=BUDGET, tolerance=TOL,
        )
        self.assertIs(result.reason, StopReason.CANCELLED)
        self.assertEqual(result.iterations, 0)

    def test_concurrent_budget_extension(self):
        control = ControlChannel()
        driver = IterationDriver(self.store(), control)

        def top_up(k):
            if k == 0:
                control.add_budget(7)

        result = driver.run(
            self.problem, make_richardson(self.problem, OmegaOracle()),
            budget=5, tolerance=NEVER, observer=top_up,
        )
        self.assertIs(result.reason, StopReason.BUDGET_EXHAUSTED)
        self.assertEqual(result.iterations, 12)

    # -- cancel/resume equivalence with continuous run ----------------------

    def _continuous(self, make_iter, budget):
        oracle_or_mon = OmegaOracle() if make_iter is make_richardson else Monitor()
        store = CheckpointStore(self.dir / f"continuous-{make_iter.__name__}.json")
        driver = IterationDriver(store)
        problem = self.problem_for(make_iter)
        result = driver.run(
            problem, make_iter(problem, oracle_or_mon),
            budget=budget, tolerance=NEVER,
        )
        return result, oracle_or_mon

    def _cancel_resume(self, make_iter, budget, cancel_at):
        ext = OmegaOracle() if make_iter is make_richardson else Monitor()
        store = CheckpointStore(self.dir / f"resumed-{make_iter.__name__}.json")
        control = ControlChannel()
        problem = self.problem_for(make_iter)

        def canceller(k):
            if k == cancel_at:
                control.request_cancel()

        first = IterationDriver(store, control).run(
            problem, make_iter(problem, ext),
            budget=budget, tolerance=NEVER, observer=canceller,
        )
        self.assertIs(first.reason, StopReason.CANCELLED)
        self.assertEqual(first.iterations, cancel_at)
        second = IterationDriver(store, ControlChannel()).run(
            problem, make_iter(problem, ext),
            budget=budget, tolerance=NEVER,
        )
        self.assertIs(second.reason, StopReason.BUDGET_EXHAUSTED)
        return second, ext

    def test_cancel_resume_matches_continuous(self):
        budget, cancel_at = 60, 25
        for make_iter in (make_richardson, make_cg):
            with self.subTest(iterator=make_iter.__name__):
                continuous, _ = self._continuous(make_iter, budget)
                resumed, _ = self._cancel_resume(make_iter, budget, cancel_at)
                self.assertEqual(continuous.iterations, resumed.iterations)
                self.assertEqual(continuous.residual_history, resumed.residual_history)
                self.assertEqual(continuous.iterator_state, resumed.iterator_state)

    def test_resume_does_not_duplicate_or_recompute(self):
        budget, cancel_at = 40, 17
        for make_iter, ext_kind in ((make_richardson, "oracle"), (make_cg, "monitor")):
            with self.subTest(iterator=make_iter.__name__):
                continuous, cont_ext = self._continuous(make_iter, budget)
                resumed, res_ext = self._cancel_resume(make_iter, budget, cancel_at)
                # external side effects fired exactly once per iteration
                if ext_kind == "oracle":
                    self.assertEqual(res_ext.calls, budget)
                    self.assertEqual(cont_ext.calls, budget)
                else:
                    self.assertEqual(len(res_ext.records), budget)
                    fired = [k for k, _ in res_ext.records]
                    self.assertEqual(fired, list(range(budget)))
                # history holds exactly one residual per completed iteration
                self.assertEqual(len(resumed.residual_history), resumed.iterations)
                self.assertEqual(resumed.residual_history, continuous.residual_history)
                # evidence keys are unique and cover every iteration
                keys = [e["key"] for e in resumed.callback_evidence]
                self.assertEqual(len(keys), len(set(keys)))
                self.assertEqual(len(keys), budget)

    # -- callback evidence --------------------------------------------------

    def test_callback_evidence_persisted_in_checkpoint(self):
        store = self.store()
        monitor = Monitor()
        problem, _ = poisson_problem(CG_N)
        result = IterationDriver(store).run(
            problem, make_cg(problem, monitor),
            budget=5, tolerance=NEVER,
        )
        payload = store.load()
        evidence = payload["callback_evidence"]
        self.assertEqual(len(evidence), result.iterations)
        self.assertEqual(result.iterations, 5)
        for k, entry in enumerate(evidence):
            self.assertEqual(entry["key"], f"iter{k}:monitor")
            self.assertEqual(entry["result"], f"ack-{k + 1}")

    def test_checkpoint_contains_required_fields(self):
        store = self.store()
        IterationDriver(store).run(
            self.problem, make_richardson(self.problem, OmegaOracle()),
            budget=3, tolerance=NEVER,
        )
        payload = store.load()
        for field in ("input_digest", "iterator", "iteration",
                      "residual_history", "iterator_state",
                      "callback_evidence", "stop_reason"):
            self.assertIn(field, payload)
        self.assertEqual(payload["iteration"], 3)
        self.assertEqual(payload["stop_reason"], "budget_exhausted")

    # -- corrupted / tampered / interrupted checkpoints ---------------------

    def test_corrupt_checkpoint_rejected(self):
        store = self.store()
        store.path.write_bytes(b"{not valid json")
        with self.assertRaises(CheckpointCorrupt):
            store.load()

    def test_tampered_checkpoint_rejected(self):
        store = self.store()
        IterationDriver(store).run(
            self.problem, make_richardson(self.problem, OmegaOracle()),
            budget=3, tolerance=1e-300,
        )
        blob = json.loads(store.path.read_text())
        blob["envelope"]["payload"]["iteration"] = 999  # tamper, keep checksum
        store.path.write_text(json.dumps(blob))
        with self.assertRaises(CheckpointCorrupt):
            store.load()

    def test_interrupted_save_keeps_last_valid_checkpoint(self):
        store = self.store()
        store.save({"iteration": 1, "mark": "old"})
        with mock.patch("os.replace", side_effect=RuntimeError("crash mid-save")):
            with self.assertRaises(RuntimeError):
                store.save({"iteration": 2, "mark": "new"})
        # target file untouched; only the tmp file may linger
        self.assertTrue(store.tmp_path.exists())
        self.assertEqual(store.load(), {"iteration": 1, "mark": "old"})

    def test_crash_during_run_then_resume_matches_continuous(self):
        budget = 30
        continuous, _ = self._continuous(make_richardson, budget)

        real_store = CheckpointStore(self.dir / "flaky.json")
        inner_save = real_store.save
        calls = {"n": 0}

        def flaky_save(payload):
            calls["n"] += 1
            # Richardson persists twice per iteration (callback confirmation,
            # then end-of-iteration). Call #12 is the end-of-iteration save of
            # iteration 5: crash exactly at an iteration boundary.
            if calls["n"] == 12:
                raise RuntimeError("simulated crash during save")
            inner_save(payload)

        oracle = OmegaOracle()
        with mock.patch.object(real_store, "save", side_effect=flaky_save):
            with self.assertRaises(RuntimeError):
                IterationDriver(real_store).run(
                    self.problem, make_richardson(self.problem, oracle),
                    budget=budget, tolerance=NEVER,
                )
        survived = real_store.load()
        self.assertIsNotNone(survived)
        self.assertLess(survived["iteration"], budget)

        resumed = IterationDriver(real_store).run(
            self.problem, make_richardson(self.problem, oracle),
            budget=budget, tolerance=NEVER,
        )
        self.assertEqual(resumed.iterations, budget)
        self.assertEqual(resumed.residual_history, continuous.residual_history)
        self.assertEqual(resumed.iterator_state, continuous.iterator_state)
        self.assertEqual(oracle.calls, budget)  # no side effect repeated

    def test_crash_at_callback_confirmation_retries_only_unconfirmed(self):
        # If the crash hits *after* the external side effect fired but *before*
        # its confirmation save completed, that call was never committed to
        # the evidence log. Exactly-once is impossible for such a call; the
        # driver guarantees it is the only one retried, and every confirmed
        # callback is replayed from evidence instead of being re-invoked.
        budget = 20
        real_store = CheckpointStore(self.dir / "flaky-cb.json")
        inner_save = real_store.save
        calls = {"n": 0}

        def flaky_save(payload):
            calls["n"] += 1
            if calls["n"] == 11:  # callback-confirmation save of iteration 5
                raise RuntimeError("simulated crash during save")
            inner_save(payload)

        oracle = OmegaOracle()
        with mock.patch.object(real_store, "save", side_effect=flaky_save):
            with self.assertRaises(RuntimeError):
                IterationDriver(real_store).run(
                    self.problem, make_richardson(self.problem, oracle),
                    budget=budget, tolerance=NEVER,
                )
        survived = real_store.load()
        self.assertEqual(survived["iteration"], 5)  # iter 5 never committed
        self.assertEqual(len(survived["callback_evidence"]), 5)

        resumed = IterationDriver(real_store).run(
            self.problem, make_richardson(self.problem, oracle),
            budget=budget, tolerance=NEVER,
        )
        self.assertEqual(resumed.iterations, budget)
        # exactly one retry (the unconfirmed iter-5 call), nothing else repeated
        self.assertEqual(oracle.calls, budget + 1)
        keys = [e["key"] for e in resumed.callback_evidence]
        self.assertEqual(len(keys), len(set(keys)))
        self.assertEqual(len(keys), budget)

    # -- input change invalidates checkpoint --------------------------------

    def test_input_matrix_change_rejects_checkpoint(self):
        store = self.store()
        control = ControlChannel()

        def canceller(k):
            if k == 5:
                control.request_cancel()

        IterationDriver(store, control).run(
            self.problem, make_richardson(self.problem, OmegaOracle()),
            budget=BUDGET, tolerance=TOL, observer=canceller,
        )
        changed = {"n": N, "a": [list(row) for row in self.problem["a"]],
                   "b": list(self.problem["b"])}
        changed["a"][0][0] = 3.0
        with self.assertRaises(InputMismatch):
            IterationDriver(store).run(
                changed, make_richardson(changed, OmegaOracle()),
                budget=BUDGET, tolerance=TOL,
            )

    def test_rhs_change_rejects_checkpoint(self):
        store = self.store()
        IterationDriver(store).run(
            self.problem, make_richardson(self.problem, OmegaOracle()),
            budget=3, tolerance=1e-300,
        )
        changed = {"n": N, "a": [list(row) for row in self.problem["a"]],
                   "b": [v + 1.0 for v in self.problem["b"]]}
        with self.assertRaises(InputMismatch):
            IterationDriver(store).run(
                changed, make_richardson(changed, OmegaOracle()),
                budget=BUDGET, tolerance=TOL,
            )

    # -- concurrent control arrivals ----------------------------------------

    def test_concurrent_budget_cancel_checkpoint_requests(self):
        control = ControlChannel()
        store = self.store()
        oracle = OmegaOracle()
        done = threading.Event()

        def budget_producer():
            for _ in range(20):
                control.add_budget(1000)
                time.sleep(0.001)

        def checkpoint_producer():
            while not done.is_set():
                control.request_checkpoint()
                time.sleep(0.002)

        def canceller():
            time.sleep(0.15)
            control.request_cancel()

        threads = [
            threading.Thread(target=budget_producer),
            threading.Thread(target=checkpoint_producer),
            threading.Thread(target=canceller),
        ]
        for t in threads:
            t.start()
        result = IterationDriver(store, control).run(
            self.problem, make_richardson(self.problem, oracle),
            budget=10_000_000, tolerance=NEVER,
        )
        done.set()
        for t in threads:
            t.join()

        self.assertIs(result.reason, StopReason.CANCELLED)
        self.assertGreater(result.iterations, 0)
        # on-disk checkpoint is exactly the returned final state
        payload = store.load()
        self.assertEqual(payload["iteration"], result.iterations)
        self.assertEqual(payload["residual_history"], result.residual_history)
        self.assertEqual(payload["stop_reason"], "cancelled")
        self.assertEqual(len(payload["residual_history"]), payload["iteration"])

        # resume from the concurrently-written checkpoint converges cleanly
        resumed = IterationDriver(store, ControlChannel()).run(
            self.problem, make_richardson(self.problem, oracle),
            budget=10_000_000, tolerance=TOL,
        )
        self.assertIs(resumed.reason, StopReason.CONVERGED)
        # every iteration triggered exactly one oracle call across both runs
        self.assertEqual(oracle.calls, resumed.iterations)


if __name__ == "__main__":
    unittest.main()

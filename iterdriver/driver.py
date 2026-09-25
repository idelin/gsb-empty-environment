"""Generic iteration driver.

The driver runs any solver exposing the protocol::

    solver.kind                       -> str, recorded in the checkpoint
    solver.state_dict()               -> JSON-serializable internal state
    solver.load_state_dict(state)     -> restore internal state
    solver.step(effects)              -> one iteration, returns residual

Safe points
-----------
Cancellation, explicit checkpoint requests and budget top-ups may
arrive concurrently from any thread. They are *serviced only at safe
points*: the boundary between two iterations, never in the middle of a
solver step. Precedence at a safe point:

    1. cancellation       -> save final checkpoint, stop CANCELLED
    2. checkpoint request -> save, keep running
    3. convergence        -> save final checkpoint, stop CONVERGED
    4. budget             -> if empty, save checkpoint, stop
                             BUDGET_EXHAUSTED; else consume one unit

External side effects
---------------------
A solver stage may call a non-recomputable external callback through
``effects.call(name, payload, fn)``. The result is written into the
checkpoint's callback ledger (keyed by ``"<iteration>:<name>"``)
*before* the iteration completes, so a confirmed side effect is never
invoked again after a resume -- its recorded result is replayed from
the ledger instead.
"""
from __future__ import annotations

import enum
import threading
from dataclasses import dataclass

from .checkpoint import (Checkpoint, CheckpointError,
                         CheckpointInputMismatch)


class StopReason(enum.Enum):
    CONVERGED = "converged"
    BUDGET_EXHAUSTED = "budget_exhausted"
    CANCELLED = "cancelled"


class Budget:
    """Thread-safe iteration budget; may be topped up concurrently."""

    def __init__(self, units):
        if units < 0:
            raise ValueError("budget must be non-negative")
        self._units = units
        self._lock = threading.Lock()

    def add(self, units):
        if units < 0:
            raise ValueError("cannot add a negative budget")
        with self._lock:
            self._units += units

    def try_consume(self):
        with self._lock:
            if self._units <= 0:
                return False
            self._units -= 1
            return True

    @property
    def remaining(self):
        with self._lock:
            return self._units


class EffectInvoker:
    """Ledger-backed invoker for non-recomputable external callbacks."""

    def __init__(self, ledger, iteration):
        self._ledger = ledger
        self._iteration = iteration

    def call(self, name, payload, fn):
        """Return the confirmed result for (iteration, name).

        If this callback was already confirmed in a previous run, the
        recorded result is replayed from the ledger and ``fn`` is NOT
        invoked. Otherwise ``fn(payload)`` runs exactly once and its
        JSON-serializable result becomes checkpoint evidence.
        """
        key = f"{self._iteration}:{name}"
        entry = self._ledger.get(key)
        if entry is not None:
            return entry["result"]
        result = fn(payload)
        self._ledger[key] = {"payload": payload, "result": result}
        return result


@dataclass
class RunResult:
    stop_reason: StopReason
    iterations: int
    residual_history: list
    checkpoint: Checkpoint


class Driver:
    """Drives one run leg of an iterative solver.

    One ``Driver`` instance per run leg; create a fresh instance to
    resume from a checkpoint.
    """

    def __init__(self, *, store=None, tolerance, budget,
                 autosave_interval=None):
        if tolerance <= 0:
            raise ValueError("tolerance must be positive")
        self.store = store
        self.tolerance = tolerance
        self.budget = budget
        self.autosave_interval = autosave_interval
        self._cancel_requested = threading.Event()
        self._checkpoint_requested = threading.Event()

    def request_cancel(self):
        """Thread-safe: ask the driver to stop at the next safe point."""
        self._cancel_requested.set()

    def request_checkpoint(self):
        """Thread-safe: ask for a checkpoint at the next safe point."""
        self._checkpoint_requested.set()

    def run(self, solver, *, digest, resume_from=None):
        if resume_from is not None:
            if resume_from.input_digest != digest:
                raise CheckpointInputMismatch(
                    "checkpoint digest does not match current inputs; "
                    "refusing to resume from a stale checkpoint")
            if resume_from.solver_kind != solver.kind:
                raise CheckpointError(
                    f"checkpoint belongs to solver kind "
                    f"{resume_from.solver_kind!r}, not {solver.kind!r}")
            solver.load_state_dict(resume_from.solver_state)
            iteration = resume_from.iteration
            history = list(resume_from.residual_history)
            ledger = {k: dict(v)
                      for k, v in resume_from.callback_ledger.items()}
        else:
            iteration = 0
            history = []
            ledger = {}

        def snapshot():
            return Checkpoint(
                input_digest=digest,
                solver_kind=solver.kind,
                iteration=iteration,
                solver_state=solver.state_dict(),
                residual_history=list(history),
                callback_ledger={k: dict(v) for k, v in ledger.items()})

        def save():
            cp = snapshot()
            if self.store is not None:
                self.store.save(cp)
            return cp

        last = None
        while True:
            # ---- safe point: service pending requests ----
            if self._cancel_requested.is_set():
                last = save()
                return RunResult(StopReason.CANCELLED, iteration,
                                 history, last)
            if self._checkpoint_requested.is_set():
                self._checkpoint_requested.clear()
                last = save()
            if history and history[-1] <= self.tolerance:
                last = save()
                return RunResult(StopReason.CONVERGED, iteration,
                                 history, last)
            if not self.budget.try_consume():
                last = save()
                return RunResult(StopReason.BUDGET_EXHAUSTED, iteration,
                                 history, last)
            # ---- one full iteration; no requests serviced here ----
            effects = EffectInvoker(ledger, iteration)
            residual = solver.step(effects)
            history.append(residual)
            iteration += 1
            if (self.autosave_interval
                    and iteration % self.autosave_interval == 0):
                last = save()

"""Generic iteration driver with checkpointing and concurrent control.

Safe points
-----------
Control arrivals (cancel / budget / checkpoint request) and persistence
are only acted upon at well-defined safe points:

1. **Top-of-iteration safe point** -- before each iteration the driver
   polls the ControlChannel. Cancellation takes effect here, the
   effective budget (base + concurrently added) is re-evaluated here,
   and queued checkpoint requests are honored here.
2. **Callback-confirmation safe point** -- immediately after an external
   (non-recomputable) callback returns, its result is appended to the
   checkpoint evidence and the checkpoint is persisted *before* the
   solver continues. A crash or cancel afterwards can therefore never
   re-invoke an already-confirmed side effect: on resume the recorded
   result is replayed from the evidence log instead of calling out again.
3. **End-of-iteration safe point** -- after each completed iteration the
   full state (iteration counter, residual history, iterator state,
   evidence log) is persisted atomically.

Checkpoint contents
----------------
input digest, iterator name, completed-iteration counter, residual
history, full iterator state, callback evidence log, stop reason.
The residual-history length is validated against the iteration counter
on load, so a resume can never re-record an already completed iteration.
"""

import enum
from dataclasses import dataclass

from .checkpoint import CheckpointCorrupt, CheckpointStore, InputMismatch
from .control import ControlChannel
from .digest import input_digest


class StopReason(enum.Enum):
    CONVERGED = "converged"
    CANCELLED = "cancelled"
    BUDGET_EXHAUSTED = "budget_exhausted"


@dataclass
class RunResult:
    reason: StopReason
    iterations: int  # total completed iterations (cumulative across resumes)
    residual_history: list
    iterator_state: dict
    input_digest: str
    callback_evidence: list


class _CallbackContext:
    """Passed to Iterator.step(); routes external calls through the evidence log."""

    def __init__(self, driver, iteration):
        self._driver = driver
        self.iteration = iteration

    def invoke_external(self, name, fn):
        return self._driver._invoke_external(self.iteration, name, fn)


class IterationDriver:
    def __init__(self, store=None, control=None):
        self.store = store if store is not None else CheckpointStore("checkpoint.json")
        self.control = control if control is not None else ControlChannel()
        self._reset_runtime()

    def _reset_runtime(self):
        self._iterator = None
        self._digest = ""
        self._iteration = 0
        self._history = []
        self._evidence = []
        self._evidence_map = {}

    # -- callback evidence -------------------------------------------------

    def _invoke_external(self, iteration, name, fn):
        key = f"iter{iteration}:{name}"
        if key in self._evidence_map:
            return self._evidence_map[key]  # replay confirmed result, no side effect
        result = fn()
        self._evidence_map[key] = result
        self._evidence.append({"key": key, "result": result})
        # Callback-confirmation safe point: persist the evidence before the
        # solver is allowed to continue, so the side effect is never repeated.
        self._save(stop_reason=None)
        return result

    # -- persistence -------------------------------------------------------

    def _payload(self, stop_reason):
        return {
            "input_digest": self._digest,
            "iterator": self._iterator.name,
            "iteration": self._iteration,
            "residual_history": list(self._history),
            "iterator_state": self._iterator.state_dict(),
            "callback_evidence": list(self._evidence),
            "stop_reason": stop_reason.value if stop_reason else None,
        }

    def _save(self, stop_reason):
        self.store.save(self._payload(stop_reason))

    def _restore(self, cp, iterator):
        if cp["input_digest"] != self._digest:
            raise InputMismatch(
                "checkpoint inputs differ from current problem/iterator"
            )
        if cp["iterator"] != iterator.name:
            raise InputMismatch("checkpoint belongs to a different iterator")
        if len(cp["residual_history"]) != cp["iteration"]:
            raise CheckpointCorrupt(
                "residual history length does not match iteration counter"
            )
        iterator.load_state(cp["iterator_state"])
        self._iteration = cp["iteration"]
        self._history = list(cp["residual_history"])
        self._evidence = [dict(e) for e in cp["callback_evidence"]]
        self._evidence_map = {e["key"]: e["result"] for e in self._evidence}

    # -- main loop ---------------------------------------------------------

    def run(self, problem, iterator, *, budget, tolerance, observer=None):
        """Drive ``iterator`` to convergence, cancellation, or budget exhaustion.

        ``observer`` (optional) is invoked with the completed-iteration count
        at every top-of-iteration safe point; tests and supervisors use it to
        inject control arrivals deterministically.
        """
        self._reset_runtime()
        self._iterator = iterator
        self._digest = input_digest(problem, iterator.name)

        cp = self.store.load()
        if cp is not None:
            self._restore(cp, iterator)
            if cp["stop_reason"] == StopReason.CONVERGED.value:
                return self._result(StopReason.CONVERGED)

        reason = None
        while True:
            # ---- safe point: top of iteration ----
            if observer is not None:
                observer(self._iteration)
            decision = self.control.poll()
            if decision.cancel_requested:
                reason = StopReason.CANCELLED
            elif self._iteration >= budget + decision.extra_budget:
                reason = StopReason.BUDGET_EXHAUSTED
            if reason is not None:
                break
            if decision.checkpoint_requested:
                self._save(stop_reason=None)

            ctx = _CallbackContext(self, self._iteration)
            residual = iterator.step(ctx)

            # ---- safe point: end of iteration ----
            self._history.append(residual)
            self._iteration += 1
            self._save(stop_reason=None)
            if residual <= tolerance:
                reason = StopReason.CONVERGED
                break

        self._save(stop_reason=reason)
        return self._result(reason)

    def _result(self, reason):
        return RunResult(
            reason=reason,
            iterations=self._iteration,
            residual_history=list(self._history),
            iterator_state=self._iterator.state_dict(),
            input_digest=self._digest,
            callback_evidence=list(self._evidence),
        )

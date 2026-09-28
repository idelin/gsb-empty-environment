"""linqueue: linearizability checker for a bounded FIFO queue.

Given a finite call history (invoke/response times, results, exceptions,
pending calls), find a sequential explanation consistent with real-time
order, or produce a minimal replayable counterexample.
"""
from .model import QueueModel, Ret, OK, EMPTY, FULL
from .history import Op, enq, deq, pending_enq, pending_deq
from .checker import Verdict, CheckResult, check, predecessors
from .counterexample import (
    minimize,
    is_minimal,
    replay_counterexample,
    replay_witness,
    ReplayReport,
    Failure,
)

__all__ = [
    "QueueModel", "Ret", "OK", "EMPTY", "FULL",
    "Op", "enq", "deq", "pending_enq", "pending_deq",
    "Verdict", "CheckResult", "check", "predecessors",
    "minimize", "is_minimal", "replay_counterexample", "replay_witness",
    "ReplayReport", "Failure",
]

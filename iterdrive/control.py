"""Thread-safe control channel for concurrent control arrivals.

Budget top-ups, cancellation requests, and checkpoint requests may arrive
at any time from any thread. The driver consumes them only at defined
safe points (see driver.py), so control state and iteration state are
never observed mid-update.
"""

import threading
from dataclasses import dataclass


@dataclass(frozen=True)
class ControlDecision:
    cancel_requested: bool
    checkpoint_requested: bool
    extra_budget: int  # cumulative budget added on top of the base budget


class ControlChannel:
    def __init__(self):
        self._lock = threading.Lock()
        self._cancel = False
        self._checkpoint_requests = 0
        self._extra_budget = 0

    def request_cancel(self):
        with self._lock:
            self._cancel = True

    def request_checkpoint(self):
        with self._lock:
            self._checkpoint_requests += 1

    def add_budget(self, n):
        if n <= 0:
            raise ValueError("budget increment must be positive")
        with self._lock:
            self._extra_budget += n

    def poll(self):
        """Snapshot the pending control state (called at safe points only)."""
        with self._lock:
            decision = ControlDecision(
                cancel_requested=self._cancel,
                checkpoint_requested=self._checkpoint_requests > 0,
                extra_budget=self._extra_budget,
            )
            self._checkpoint_requests = 0
            return decision

"""Bounded request-response protocol on top of the finite-state checker.

Lifecycle of one accepted request:

    ACCEPTED --dispatch--> DISPATCHED --respond-->  RESPONDED   (terminal)
        |                      |  \
        |cancel                |   \--timeout--> RETRYING --dispatch--> DISPATCHED
        v                      |cancel                |cancel
    CANCELLED (terminal) <-----+----------------------+

Rules (explicit):
- Terminal states are RESPONDED (success) and CANCELLED; cancellation is a
  declared terminal state, so a cancelled request counts as complete.
- `respond` is a fair action: a run in which a dispatched request is
  respondable forever but never responded to is considered unfair and is
  excluded from the must-complete verdict.
- Duplicate request IDs are independent: every `accept()` call creates a
  fresh instance keyed by (request_id, occurrence); the state of one
  instance never affects another, even under the same ID.
- The completion check is bounded; if the reachable space is not fully
  explored within `max_states`, the verdict stays Status.PENDING.
"""

from __future__ import annotations

from dataclasses import dataclass

from model import CheckResult, Status, TransitionSystem, check

ACCEPTED = "ACCEPTED"
DISPATCHED = "DISPATCHED"
RETRYING = "RETRYING"
RESPONDED = "RESPONDED"    # terminal: completed successfully
CANCELLED = "CANCELLED"    # terminal: cancelled
LOST = "LOST"              # non-terminal dead end (faulty variant only)

TERMINALS = (RESPONDED, CANCELLED)


def build_request_system(include_loss: bool = False, fair_respond: bool = True) -> TransitionSystem:
    """Build the request lifecycle as a finite transition system.

    include_loss adds a `lose` transition into the non-terminal dead end
    LOST (a modelling fault used to demonstrate the BLOCKED verdict).
    fair_respond controls whether `respond` is a fair action; with it off,
    the timeout/retry cycle makes completion possible but not guaranteed.
    """
    ts = TransitionSystem(initial=ACCEPTED, terminals=TERMINALS)
    ts.add(ACCEPTED, "dispatch", DISPATCHED)
    ts.add(ACCEPTED, "cancel", CANCELLED)
    ts.add(DISPATCHED, "respond", RESPONDED, fair=fair_respond)
    ts.add(DISPATCHED, "timeout", RETRYING)
    ts.add(DISPATCHED, "cancel", CANCELLED)
    ts.add(RETRYING, "dispatch", DISPATCHED)
    ts.add(RETRYING, "cancel", CANCELLED)
    if include_loss:
        ts.add(DISPATCHED, "lose", LOST)
    return ts


@dataclass(frozen=True)
class Handle:
    """Identity of one accepted request instance.

    The same request_id may be accepted repeatedly; each acceptance gets
    its own occurrence index and fully independent state.
    """

    request_id: str
    occurrence: int

    def __str__(self) -> str:
        return f"{self.request_id}#{self.occurrence}"


class RequestTracker:
    """Tracks accepted requests and checks each one for completion."""

    def __init__(self, system: TransitionSystem | None = None, max_states: int = 10_000):
        self.system = system if system is not None else build_request_system()
        self.max_states = max_states
        self._counts: dict[str, int] = {}
        self._states: dict[Handle, str] = {}

    def accept(self, request_id: str) -> Handle:
        """Accept a request. Duplicate IDs yield independent instances."""
        occurrence = self._counts.get(request_id, 0)
        self._counts[request_id] = occurrence + 1
        handle = Handle(request_id, occurrence)
        self._states[handle] = self.system.initial
        return handle

    def apply(self, handle: Handle, action: str) -> str:
        """Apply `action` to one instance; return its new state."""
        state = self._states[handle]
        for t in self.system.transitions(state):
            if t.action == action:
                self._states[handle] = t.target
                return t.target
        raise ValueError(f"action {action!r} not enabled in state {state!r}")

    def state_of(self, handle: Handle) -> str:
        return self._states[handle]

    def is_complete(self, handle: Handle) -> bool:
        return self._states[handle] in self.system.terminals

    def check(self, handle: Handle) -> CheckResult:
        """Check may/must(fair) completion from the instance's current state."""
        return check(self.system, start=self._states[handle], max_states=self.max_states)

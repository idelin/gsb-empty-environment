"""Replayable counterexamples: re-execute a transition sequence step by step."""
from __future__ import annotations

from .engine import fire, is_enabled


class ReplayError(Exception):
    def __init__(self, step, transition_id, marking):
        super().__init__(
            f"replay failed at step {step}: transition {transition_id!r} "
            f"is not enabled at {marking}")
        self.step = step
        self.transition_id = transition_id
        self.marking = marking


def replay(net, initial, transition_ids):
    """Re-execute transition_ids from `initial` (dict of real place ids).

    Returns the final marking as a dict keyed by real place ids.
    Raises ReplayError if any step is not enabled -- a counterexample that
    cannot be replayed is not a counterexample.
    """
    m = net.encode(initial)
    for i, tid in enumerate(transition_ids):
        t = net.transitions.get(tid)
        if t is None or not is_enabled(net, m, t):
            raise ReplayError(i, tid, net.decode(m))
        m = fire(net, m, t)
    return net.decode(m)

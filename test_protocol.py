import unittest

from model import Status, TransitionSystem, check, replay
from protocol import (
    ACCEPTED,
    CANCELLED,
    DISPATCHED,
    LOST,
    RESPONDED,
    RequestTracker,
    build_request_system,
)


class TestCompletionVerdicts(unittest.TestCase):
    def test_fair_respond_guarantees_completion(self):
        system = build_request_system()
        result = check(system, start=DISPATCHED)
        self.assertEqual(result.status, Status.GUARANTEED)
        self.assertTrue(result.may_complete)
        self.assertTrue(result.must_complete_fair)
        self.assertEqual(result.blocked_states, frozenset())

    def test_unfair_respond_is_possible_only(self):
        system = build_request_system(fair_respond=False)
        result = check(system, start=DISPATCHED)
        self.assertEqual(result.status, Status.POSSIBLE_ONLY)
        self.assertTrue(result.may_complete)
        self.assertFalse(result.must_complete_fair)

    def test_success_path_alone_does_not_guarantee_completion(self):
        # A model with a success path and an unfair infinite loop must not
        # be reported as guaranteed.
        ts = TransitionSystem(initial="S", terminals={"T"})
        ts.add("S", "finish", "T")
        ts.add("S", "spin", "S")
        result = check(ts)
        self.assertEqual(result.status, Status.POSSIBLE_ONLY)
        self.assertFalse(result.must_complete_fair)

    def test_lossy_model_is_blocked(self):
        system = build_request_system(include_loss=True)
        result = check(system, start=DISPATCHED)
        self.assertEqual(result.status, Status.BLOCKED)
        self.assertIn(LOST, result.blocked_states)
        self.assertFalse(result.must_complete_fair)

    def test_no_terminal_reachable_is_blocked(self):
        ts = TransitionSystem(initial="S", terminals={"T"})
        ts.add("S", "spin", "S")
        result = check(ts)
        self.assertEqual(result.status, Status.BLOCKED)
        self.assertFalse(result.may_complete)

    def test_bounded_search_stays_pending(self):
        system = build_request_system()
        result = check(system, start=ACCEPTED, max_states=1)
        self.assertEqual(result.status, Status.PENDING)
        self.assertIsNone(result.may_complete)
        self.assertIsNone(result.must_complete_fair)

    def test_cancelled_state_is_terminal(self):
        system = build_request_system()
        result = check(system, start=CANCELLED)
        self.assertEqual(result.status, Status.GUARANTEED)
        self.assertTrue(result.may_complete)


class TestCounterexamples(unittest.TestCase):
    def test_blocked_counterexample_replays_to_dead_end(self):
        system = build_request_system(include_loss=True)
        result = check(system, start=DISPATCHED)
        self.assertIsNotNone(result.counterexample)
        final = replay(system, DISPATCHED, result.counterexample)
        self.assertEqual(final, LOST)
        self.assertEqual(system.transitions(final), [])

    def test_possible_only_counterexample_replays_into_cycle(self):
        system = build_request_system(fair_respond=False)
        result = check(system, start=DISPATCHED)
        self.assertTrue(result.cycle)
        final = replay(system, DISPATCHED, result.counterexample)
        self.assertNotIn(final, system.terminals)
        # The cycle suffix is repeatable: replaying it again ends in the
        # same state, so the trace can loop forever.
        prefix, cycle = _split_at_cycle(result.counterexample)
        cycle_entry = replay(system, DISPATCHED, prefix)
        again = replay(system, cycle_entry, cycle)
        self.assertEqual(again, cycle_entry)
        self.assertEqual(final, cycle_entry)

    def test_bad_trace_is_rejected(self):
        system = build_request_system()
        with self.assertRaises(ValueError):
            replay(system, ACCEPTED, [_step("respond", RESPONDED)])


def _step(action, target):
    from model import Step

    return Step(action, target)


def _split_at_cycle(trace):
    """Split a counterexample into (prefix, cycle) at the first arrival at
    the trace's final state."""
    final = trace[-1].target
    for i, step in enumerate(trace):
        if step.target == final:
            return trace[: i + 1], trace[i + 1 :]
    raise AssertionError("no cycle found")


class TestRequestTracker(unittest.TestCase):
    def test_duplicate_request_ids_are_independent(self):
        tracker = RequestTracker()
        first = tracker.accept("r")
        second = tracker.accept("r")
        self.assertNotEqual(first, second)
        tracker.apply(first, "cancel")
        tracker.apply(second, "dispatch")
        self.assertEqual(tracker.state_of(first), CANCELLED)
        self.assertEqual(tracker.state_of(second), DISPATCHED)
        self.assertTrue(tracker.is_complete(first))
        self.assertFalse(tracker.is_complete(second))

    def test_each_accepted_request_checked_from_current_state(self):
        tracker = RequestTracker()
        handle = tracker.accept("r")
        tracker.apply(handle, "dispatch")
        tracker.apply(handle, "timeout")
        result = tracker.check(handle)
        self.assertEqual(result.status, Status.GUARANTEED)

    def test_cancel_counts_as_completion(self):
        tracker = RequestTracker()
        handle = tracker.accept("r")
        tracker.apply(handle, "cancel")
        self.assertTrue(tracker.is_complete(handle))
        self.assertEqual(tracker.check(handle).status, Status.GUARANTEED)

    def test_unknown_action_rejected(self):
        tracker = RequestTracker()
        handle = tracker.accept("r")
        with self.assertRaises(ValueError):
            tracker.apply(handle, "explode")

    def test_tracker_respects_search_bound(self):
        tracker = RequestTracker(max_states=1)
        handle = tracker.accept("r")
        self.assertEqual(tracker.check(handle).status, Status.PENDING)


if __name__ == "__main__":
    unittest.main()

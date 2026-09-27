"""Unit tests for the integer-clock timed automaton checker."""

import unittest

from timed_automaton import (
    Guard,
    Model,
    ReplayError,
    REACHABLE,
    UNREACHABLE,
    UNKNOWN,
    Step,
    Transition,
    check_reachability,
    replay,
)


def simple_model(guard, time_bound=10):
    """a --act[guard]--> b, single clock x."""
    return Model(
        clocks=("x",),
        initial="a",
        time_bound=time_bound,
        transitions=(Transition("a", "b", guard=guard, label="act"),),
    )


class StrictnessTest(unittest.TestCase):
    """x < 3 and x <= 3 must give different verdicts."""

    def test_strict_vs_nonstrict_boundary(self):
        # The only way to "goal" is: wait until x>=3 (first possible at
        # exactly t=3, the time bound), then immediately fire "go".
        # With x<3 the boundary value 3 is excluded -> UNREACHABLE.
        # With x<=3 the boundary value 3 is accepted -> REACHABLE.
        def make(op):
            return Model(
                clocks=("x",),
                initial="a",
                time_bound=3,
                transitions=(
                    Transition(
                        "a", "mid",
                        guard=(Guard("x", ">=", 3),),
                        label="wait3",
                    ),
                    Transition(
                        "mid", "goal",
                        guard=(Guard("x", op, 3),),
                        label="go",
                    ),
                ),
            )

        self.assertEqual(check_reachability(make("<"), "goal").status,
                         UNREACHABLE)
        self.assertEqual(check_reachability(make("<="), "goal").status,
                         REACHABLE)


class ResetTest(unittest.TestCase):
    """A reset discards the old clock value entirely."""

    def test_reset_clears_old_value(self):
        model = Model(
            clocks=("x",),
            initial="a",
            time_bound=10,
            transitions=(
                Transition("a", "b", resets=("x",), label="rst"),
                Transition(
                    "b", "c", guard=(Guard("x", "<", 1),), label="quick"
                ),
            ),
        )
        result = check_reachability(model, "c")
        self.assertEqual(result.status, REACHABLE)
        # The witness must contain a delay before "rst" is impossible:
        # reset happens at some t, then x=0 so x<1 holds immediately.
        final = replay(model, result.trace)
        self.assertEqual(final[0], "c")

    def test_old_value_not_retained_after_reset(self):
        # Wait 5 units, reset, then a guard x<2 must still hold (old
        # value 5 is gone); a guard x>=5 right after reset must fail.
        # Bound 7 leaves at most 2 time units after the reset, so x can
        # never climb back to 5: "stale" is reachable only if the old
        # value survives the reset.
        model = Model(
            clocks=("x",),
            initial="a",
            time_bound=7,
            transitions=(
                Transition(
                    "a", "b",
                    guard=(Guard("x", ">=", 5),),
                    resets=("x",),
                    label="rst",
                ),
                Transition(
                    "b", "c", guard=(Guard("x", "<", 2),), label="fresh"
                ),
                Transition(
                    "b", "d", guard=(Guard("x", ">=", 5),), label="stale"
                ),
            ),
        )
        self.assertEqual(check_reachability(model, "c").status, REACHABLE)
        # d would only be reachable if the pre-reset value survived.
        self.assertEqual(check_reachability(model, "d").status, UNREACHABLE)


class ZeroTimeLoopTest(unittest.TestCase):
    """Zero-time loops must terminate, not expand forever."""

    def test_zero_time_self_loop_terminates(self):
        model = Model(
            clocks=("x",),
            initial="a",
            time_bound=5,
            transitions=(
                Transition("a", "a", label="spin"),  # no guard, no reset
                Transition("a", "b", label="exit"),
            ),
        )
        result = check_reachability(model, "b")
        self.assertEqual(result.status, REACHABLE)
        # The self-loop adds exactly one state (itself), already visited.
        self.assertLessEqual(result.explored_states, 2 * (5 + 1) + 1)

    def test_zero_time_cycle_without_reset_terminates(self):
        model = Model(
            clocks=("x",),
            initial="a",
            time_bound=3,
            transitions=(
                Transition("a", "b", label="ab"),
                Transition("b", "a", label="ba"),
            ),
        )
        result = check_reachability(model, "nowhere")
        self.assertEqual(result.status, UNREACHABLE)
        # 2 locations x (cap+1 values) x (bound+1 times) is the hard cap.
        self.assertLessEqual(result.explored_states, 2 * (model.cap + 1) * 4)


class VerdictTest(unittest.TestCase):
    def test_unreachable_is_conclusive(self):
        model = simple_model((Guard("x", ">", 100),), time_bound=5)
        result = check_reachability(model, "b")
        self.assertEqual(result.status, UNREACHABLE)
        self.assertEqual(result.pending_states, 0)

    def test_unknown_when_budget_exhausted(self):
        model = simple_model((), time_bound=50)
        result = check_reachability(model, "b", max_states=1)
        self.assertEqual(result.status, UNKNOWN)
        self.assertGreater(result.pending_states, 0)

    def test_time_bound_respected(self):
        # b needs x>=4 but the bound is 3: unreachable.
        model = simple_model((Guard("x", ">=", 4),), time_bound=3)
        self.assertEqual(check_reachability(model, "b").status, UNREACHABLE)
        # With bound 4 it becomes reachable.
        model2 = simple_model((Guard("x", ">=", 4),), time_bound=4)
        self.assertEqual(check_reachability(model2, "b").status, REACHABLE)


class ReplayTest(unittest.TestCase):
    """Witnesses must be independently replayable."""

    def test_witness_replays_to_target(self):
        model = Model(
            clocks=("x",),
            initial="idle",
            time_bound=10,
            transitions=(
                Transition("idle", "run", resets=("x",), label="start"),
                Transition(
                    "run",
                    "done",
                    guard=(Guard("x", ">=", 2), Guard("x", "<", 4)),
                    label="finish",
                ),
            ),
        )
        result = check_reachability(model, "done")
        self.assertEqual(result.status, REACHABLE)
        final = replay(model, result.trace)
        self.assertEqual(final[0], "done")

    def test_corrupted_trace_rejected(self):
        model = simple_model((Guard("x", ">=", 2),), time_bound=5)
        result = check_reachability(model, "b")
        self.assertEqual(result.status, REACHABLE)
        bad = [Step(kind="delay", detail=99, state=result.trace[-1].state)]
        with self.assertRaises(ReplayError):
            replay(model, bad)

    def test_guard_violating_trace_rejected(self):
        model = simple_model((Guard("x", ">=", 2),), time_bound=5)
        # Take the transition immediately at x=0: guard must fail.
        trans = model.transitions[0]
        state = ("b", (0,), 0)
        bad = [Step(kind="transition", detail=trans, state=state)]
        with self.assertRaises(ReplayError):
            replay(model, bad)


class TruncationTest(unittest.TestCase):
    """Values beyond the cap are equivalent and keep the space finite."""

    def test_truncation_keeps_state_space_finite(self):
        model = Model(
            clocks=("x",),
            initial="a",
            time_bound=20,
            transitions=(
                Transition(
                    "a", "a", guard=(Guard("x", ">=", 0),), label="tick"
                ),
            ),
        )
        result = check_reachability(model, "zzz")
        self.assertEqual(result.status, UNREACHABLE)
        # x is capped at cap=21, time at 20: at most 22*21 states.
        self.assertLessEqual(result.explored_states, (model.cap + 1) * 21)

    def test_truncated_value_satisfies_large_guards(self):
        # Guard x>=5 with cap 6: once x hits 5..6 the transition fires;
        # truncation must not break this.
        model = simple_model((Guard("x", ">=", 5),), time_bound=20)
        self.assertEqual(check_reachability(model, "b").status, REACHABLE)


class TraceContentTest(unittest.TestCase):
    def test_trace_shows_time_advancement(self):
        model = simple_model((Guard("x", ">=", 3),), time_bound=5)
        result = check_reachability(model, "b")
        delays = [s.detail for s in result.trace if s.kind == "delay"]
        self.assertEqual(sum(delays), 3)
        text = result.summary(model)
        self.assertIn("delay 1", text)
        self.assertIn("take act", text)


if __name__ == "__main__":
    unittest.main(verbosity=2)

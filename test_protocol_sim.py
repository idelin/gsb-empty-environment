from __future__ import annotations

import unittest
from dataclasses import replace

from protocol_sim import (
    Action,
    Alphabet,
    Direction,
    FailurePath,
    ReplayError,
    StateMachine,
    Step,
    Transition,
    check_alternating_simulation,
    iter_replay_failure,
    replay_failure,
)


IN = Direction.INPUT
OUT = Direction.OUTPUT


def machine(
    name: str,
    transitions: set[Transition],
    states: set[str] | None = None,
    initial: str = "s0",
    alphabet: Alphabet | None = None,
) -> StateMachine:
    if states is None:
        states = {"s0", "s1", "s2", "s3", "s4"}
    if alphabet is None:
        actions = {transition.action for transition in transitions}
        alphabet = Alphabet(actions)
    return StateMachine(name, states, initial, alphabet, frozenset(transitions))


class AlternatingSimulationTest(unittest.TestCase):
    def test_compatible_when_candidate_adds_input_and_omits_optional_output(self) -> None:
        receive = Action("receive", IN)
        send = Action("send", OUT)
        extra_receive = Action("admin", IN)

        spec = machine(
            "spec",
            {
                Transition("s0", receive, "s1"),
                Transition("s1", send, "s0"),
            },
            alphabet=Alphabet({receive, send}),
        )
        candidate = machine(
            "candidate",
            {
                Transition("s0", receive, "s1"),
                Transition("s0", extra_receive, "s2"),
            },
            alphabet=Alphabet({receive, send, extra_receive}),
        )

        result = check_alternating_simulation(spec, candidate)

        self.assertTrue(result.satisfied)
        self.assertIsNone(result.failure)

    def test_missing_required_input_returns_terminal_failure_path(self) -> None:
        receive = Action("receive", IN)
        spec = machine("spec", {Transition("s0", receive, "s1")})
        candidate = machine("candidate", set(), alphabet=Alphabet({receive}))

        result = check_alternating_simulation(spec, candidate)

        self.assertFalse(result.satisfied)
        self.assertIsNotNone(result.failure)
        assert result.failure is not None
        self.assertEqual(result.failure.terminal_kind, "missing required input")
        frames = []
        for frame in iter_replay_failure(spec, candidate, result.failure):
            frames.append(frame)
        self.assertEqual(len(frames), 1)
        self.assertEqual(frames[0].kind, "required input")
        self.assertIsNone(frames[0].candidate_transition)
        self.assertIn("required input", frames[0].render())

    def test_extra_output_is_incompatible(self) -> None:
        allowed = Action("allowed", OUT)
        surprise = Action("surprise", OUT)
        spec = machine("spec", {Transition("s0", allowed, "s1")})
        candidate = machine(
            "candidate",
            {
                Transition("s0", allowed, "s1"),
                Transition("s0", surprise, "s2"),
            },
            alphabet=Alphabet({allowed, surprise}),
        )

        result = check_alternating_simulation(spec, candidate)

        self.assertFalse(result.satisfied)
        assert result.failure is not None
        self.assertEqual(result.failure.terminal_kind, "extra output")
        self.assertEqual(result.failure.action, surprise)
        frames = replay_failure(spec, candidate, result.failure)
        self.assertEqual(frames[0].kind, "candidate output")
        self.assertIsNone(frames[0].spec_transition)

    def test_loop_reaches_greatest_fixed_point_for_valid_cycle(self) -> None:
        receive = Action("receive", IN)
        send = Action("send", OUT)
        transitions = {
            Transition("s0", receive, "s1"),
            Transition("s1", send, "s0"),
        }
        spec = machine("spec", transitions, states={"s0", "s1"})
        candidate = machine("candidate", transitions, states={"s0", "s1"})

        result = check_alternating_simulation(spec, candidate)

        self.assertTrue(result.satisfied)
        self.assertGreaterEqual(result.iterations, 2)

    def test_bad_cycle_is_removed_by_fixed_point_and_replayed(self) -> None:
        receive = Action("receive", IN)
        send = Action("send", OUT)
        alarm = Action("alarm", OUT)
        spec = machine(
            "spec",
            {
                Transition("s0", receive, "s1"),
                Transition("s1", send, "s2"),
                Transition("s2", receive, "s1"),
            },
        )
        candidate = machine(
            "candidate",
            {
                Transition("s0", receive, "s1"),
                Transition("s1", send, "s3"),
                Transition("s3", receive, "s3"),
                Transition("s3", alarm, "s0"),
            },
            alphabet=Alphabet({receive, send, alarm}),
        )

        result = check_alternating_simulation(spec, candidate)

        self.assertFalse(result.satisfied)
        self.assertGreaterEqual(result.iterations, 2)
        assert result.failure is not None
        frames = replay_failure(spec, candidate, result.failure)
        self.assertEqual(len(frames), 3)
        self.assertEqual(
            [frame.kind for frame in frames],
            [
                "required input branch",
                "candidate output branch",
                "candidate output",
            ],
        )
        self.assertEqual(result.failure.input_trace, (receive,))
        self.assertEqual(result.failure.output_trace, (send, alarm))

    def test_shared_successor_can_cover_multiple_required_input_branches(self) -> None:
        receive = Action("receive", IN)
        spec = machine(
            "spec",
            {
                Transition("s0", receive, "s1"),
                Transition("s0", receive, "s2"),
            },
        )
        candidate = machine(
            "candidate",
            {Transition("s0", receive, "s1")},
        )

        result = check_alternating_simulation(spec, candidate)

        self.assertTrue(result.satisfied)

    def test_replay_rejects_tampered_path(self) -> None:
        receive = Action("receive", IN)
        spec = machine("spec", {Transition("s0", receive, "s1")})
        candidate = machine("candidate", set(), alphabet=Alphabet({receive}))
        failure = check_alternating_simulation(spec, candidate).failure
        assert failure is not None
        tampered = replace(failure, terminal_state_pair=("s1", "s0"))

        with self.assertRaises(ReplayError):
            replay_failure(spec, candidate, tampered)

    def test_invalid_state_machine_is_rejected(self) -> None:
        receive = Action("receive", IN)
        alphabet = Alphabet({receive})
        with self.assertRaises(ValueError):
            StateMachine(
                "bad",
                {"s0"},
                "missing",
                alphabet,
                frozenset(),
            )
        with self.assertRaises(ValueError):
            StateMachine(
                "bad",
                {"s0", "s1"},
                "s0",
                alphabet,
                frozenset({Transition("s0", receive, "s2")}),
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)

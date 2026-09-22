from __future__ import annotations

from protocol_sim import (
    Action,
    Alphabet,
    Direction,
    StateMachine,
    Transition,
    check_alternating_simulation,
    iter_replay_failure,
)


OPEN = Action("open", Direction.INPUT)
CLOSE = Action("close", Direction.INPUT)
PING = Action("ping", Direction.INPUT)
DATA = Action("data", Direction.OUTPUT)
DONE = Action("done", Direction.OUTPUT)
RESET = Action("reset", Direction.OUTPUT)


def specification() -> StateMachine:
    alphabet = Alphabet({OPEN, CLOSE, PING, DATA, DONE})
    transitions = {
        Transition("ready", OPEN, "open"),
        Transition("open", DATA, "waiting"),
        Transition("open", DONE, "closed"),
        Transition("waiting", PING, "open"),
        Transition("open", CLOSE, "closed"),
    }
    return StateMachine("spec", {"ready", "open", "waiting", "closed"},
                        "ready", alphabet, transitions)


def compatible_candidate() -> StateMachine:
    alphabet = Alphabet({OPEN, CLOSE, PING, DATA, DONE})
    transitions = {
        Transition("ready", OPEN, "open"),
        Transition("open", DATA, "waiting"),
        Transition("waiting", PING, "open"),
        Transition("open", CLOSE, "closed"),
    }
    return StateMachine("compatible", {"ready", "open", "waiting", "closed"},
                        "ready", alphabet, transitions)


def missing_input_candidate() -> StateMachine:
    alphabet = Alphabet({OPEN, CLOSE, PING, DATA, DONE})
    transitions = {
        Transition("ready", OPEN, "open"),
        Transition("open", DATA, "waiting"),
        Transition("open", CLOSE, "closed"),
    }
    return StateMachine("missing-input", {"ready", "open", "waiting", "closed"},
                        "ready", alphabet, transitions)


def extra_output_candidate() -> StateMachine:
    alphabet = Alphabet({OPEN, CLOSE, PING, DATA, DONE, RESET})
    transitions = {
        Transition("ready", OPEN, "open"),
        Transition("open", DATA, "waiting"),
        Transition("open", RESET, "ready"),
        Transition("waiting", PING, "open"),
        Transition("open", CLOSE, "closed"),
    }
    return StateMachine("extra-output", {"ready", "open", "waiting", "closed"},
                        "ready", alphabet, transitions)


def bad_loop_candidate() -> StateMachine:
    alphabet = Alphabet({OPEN, CLOSE, PING, DATA, DONE})
    transitions = {
        Transition("ready", OPEN, "open"),
        Transition("open", DATA, "bad"),
        Transition("bad", PING, "bad"),
        Transition("open", CLOSE, "closed"),
    }
    return StateMachine("bad-loop", {"ready", "open", "bad", "closed"},
                        "ready", alphabet, transitions)


def main() -> None:
    spec = specification()
    candidates = [
        compatible_candidate(),
        missing_input_candidate(),
        extra_output_candidate(),
        bad_loop_candidate(),
    ]

    for candidate in candidates:
        result = check_alternating_simulation(spec, candidate)
        verdict = "PASS" if result.satisfied else "FAIL"
        print(
            f"[{verdict}] {candidate.name} "
            f"(fixed-point iterations: {result.iterations})"
        )
        if result.failure is None:
            print("  no failure interaction path")
            continue
        print(result.failure.render())
        frame_count = 0
        for frame in iter_replay_failure(spec, candidate, result.failure):
            frame_count += 1
            print(f"replay {frame.render()}")
        print(f"replayed {frame_count} step(s) independently")
        print()


if __name__ == "__main__":
    main()

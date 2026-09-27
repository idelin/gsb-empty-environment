"""Runnable samples for the finite-state deadlock checker (Python 3.11)."""
from fsmcheck import ActionSpec, LocalTransition, Model, Participant, Status, check, verify


def show(title, model, budget=None):
    print(f"=== {title} ===")
    result = check(model, budget=budget)
    print(f"status: {result.status.value} (expanded={result.expanded})")
    for warning in result.warnings:
        print(f"warning: {warning}")
    if result.status is Status.PENDING:
        print(f"pending frontier: {result.frontier}")
    if result.counterexample:
        ev = result.counterexample
        print(f"initial: {ev.initial}")
        for i, step in enumerate(ev.steps):
            print(f"  step {i}: {step.action} {list(step.moves)} -> {step.state}")
        diag = ev.diagnosis
        print(f"deadlock state: {diag.state}")
        for blocked in diag.blocked_actions:
            print(f"  blocked action: {blocked.action}; missing: {blocked.missing}")
            for pre in blocked.preconditions():
                print(f"    precondition: {pre}")
        if diag.minimal_cycle:
            print(f"  minimal blocking cycle: {' -> '.join(diag.minimal_cycle)}")
        if diag.no_provider:
            print(f"  no-provider participants: {diag.no_provider}")
    report = verify(model, result)
    print("verification:")
    print("  " + report.summary().replace("\n", "\n  "))
    print()


def sample_circular_wait():
    a = Participant("A", [LocalTransition("ab", "a0", "a1")])
    b = Participant("B", [LocalTransition("ba", "b0", "b1")])
    return Model(
        participants=[a, b],
        actions=[ActionSpec("ab", ["A", "B"]), ActionSpec("ba", ["A", "B"])],
        initials=[("a0", "b0")],
        terminals=[],
    )


def sample_no_provider():
    client = Participant("Client", [
        LocalTransition("request", "idle", "waiting"),
        LocalTransition("reply", "waiting", "idle"),
    ])
    server = Participant("Server", [
        LocalTransition("request", "ready", "busy"),
        LocalTransition("reply", "busy", "ready"),
    ])
    return Model(
        participants=[client, server],
        actions=[ActionSpec("request", ["Client", "Server"]),
                 ActionSpec("reply", ["Client", "Server"])],
        initials=[("idle", "ready"), ("waiting", "down")],
        terminals=[],
    )


def sample_terminal_with_actions():
    worker = Participant("Worker", [
        LocalTransition("work", "w0", "w1"),
        LocalTransition("finish", "w1", "done"),
        LocalTransition("overtime", "done", "done"),
    ])
    return Model(
        participants=[worker],
        actions=[ActionSpec("work", ["Worker"]), ActionSpec("spin", ["Worker"]),
                 ActionSpec("finish", ["Worker"]), ActionSpec("overtime", ["Worker"])],
        initials=[("w0",)],
        terminals=[("done",)],
    )


def sample_livelock_candidate():
    machine = Participant("Machine", [
        LocalTransition("run", "m0", "m1"),
        LocalTransition("spin", "m1", "m1"),
    ])
    return Model(
        participants=[machine],
        actions=[ActionSpec("run", ["Machine"]), ActionSpec("spin", ["Machine"])],
        initials=[("m0",)],
        terminals=[],
    )


def sample_clean():
    p = Participant("P", [LocalTransition("go", "s0", "s1")])
    q = Participant("Q", [LocalTransition("go", "t0", "t1")])
    return Model(
        participants=[p, q],
        actions=[ActionSpec("go", ["P", "Q"])],
        initials=[("s0", "t0")],
        terminals=[("s1", "t1")],
    )


def sample_deep_chain():
    p = Participant("P", [
        LocalTransition(f"m{i}", f"s{i}", f"s{i+1}") for i in range(5)
    ])
    return Model(
        participants=[p],
        actions=[ActionSpec(f"m{i}", ["P"]) for i in range(5)],
        initials=[("s0",)],
        terminals=[],
    )


if __name__ == "__main__":
    show("circular wait (minimal blocking cycle)", sample_circular_wait())
    show("no provider, multi-initial shortest evidence", sample_no_provider())
    show("declared terminal with actions", sample_terminal_with_actions())
    show("self-loop livelock candidate", sample_livelock_candidate())
    show("clean model, normal terminal", sample_clean())
    show("budget pending (unexpanded frontier is not termination)", sample_deep_chain(), budget=2)

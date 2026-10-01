"""Demo: check weak/strong fairness constraints on sample finite-state models.

Run with:  python3.11 main.py
"""

from fairness import (
    FairnessKind,
    Verdict,
    check_fairness,
    replay,
    TransitionSystem,
)


def fmt_transitions(transitions):
    if not transitions:
        return "(empty)"
    return "  ".join(f"{s} --{a}--> {d}" for s, a, d in transitions)


def show(report, ts):
    print(f"[{report.verdict.value.upper():8}] {report.kind.value} fairness of "
          f"{report.action!r}  "
          f"(explored {report.states_explored} states, "
          f"exhausted={report.search_exhausted})")
    witness = report.witness
    if witness is None:
        if report.verdict is Verdict.PENDING:
            print("           search incomplete -> verdict stays pending")
        return
    print(f"           stem:  {fmt_transitions(witness.stem)}")
    print(f"           cycle: {fmt_transitions(witness.cycle)}")
    print(f"           enablement of {witness.action!r}: "
          f"{witness.stats.describe()}")
    result = replay(ts, witness)
    status = "OK (loop re-verified, repeatable)" if result.ok \
        else f"FAILED: {result.error}"
    print(f"           replay: {status}")


def main():
    print("=== Model A: worker (weak fairness violation) ===")
    # 'reset' is enabled in both states but the loop can do 'work' forever.
    worker = TransitionSystem(
        initials=["idle"],
        transitions=[
            ("idle", "work", "busy"),
            ("busy", "work", "idle"),
            ("idle", "reset", "idle"),
            ("busy", "reset", "busy"),
        ],
    )
    for report in check_fairness(worker, [("reset", FairnessKind.WEAK)]):
        show(report, worker)

    print()
    print("=== Model B: ping-pong (intermittent != continuous) ===")
    # 'recover' is enabled only in s0, so the s0->s1->s0 loop enables it
    # intermittently: weak fairness HOLDS, strong fairness is VIOLATED.
    pingpong = TransitionSystem(
        initials=["s0"],
        transitions=[
            ("s0", "ping", "s1"),
            ("s1", "pong", "s0"),
            ("s0", "recover", "s0"),
        ],
    )
    constraints = [("recover", FairnessKind.WEAK), ("recover", FairnessKind.STRONG)]
    for report in check_fairness(pingpong, constraints):
        show(report, pingpong)

    print()
    print("=== Model C: toggle (fairness holds) ===")
    # The only cycle takes 'reset', so no fair-loop counterexample exists.
    toggle = TransitionSystem(
        initials=["off"],
        transitions=[
            ("off", "step", "on"),
            ("on", "reset", "off"),
        ],
    )
    constraints = [("reset", FairnessKind.WEAK), ("reset", FairnessKind.STRONG)]
    for report in check_fairness(toggle, constraints):
        show(report, toggle)

    print()
    print("=== Model D: worker with max_states=1 (search cut short) ===")
    for report in check_fairness(worker, [("reset", FairnessKind.WEAK)],
                                 max_states=1):
        show(report, worker)


if __name__ == "__main__":
    main()

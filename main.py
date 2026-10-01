"""Demo: full enumeration vs POR on the sample models, with evidence."""
from por import examples, explore
from por.model import format_state


def run(model):
    full = explore.explore(model, use_por=False)
    por = explore.explore(model, use_por=True)
    print(f"== {model.name} ==")
    if model.property_fn:
        print(f"   safety property: {model.property_name}")
    print(f"   deadlock check:  {model.check_deadlock}")
    for label, res in (("full", full), ("por ", por)):
        line = (
            f"   {label}: verdict={res.verdict:<7} states={res.states_explored:<4} "
            f"transitions={res.transitions:<4}"
        )
        if res.kind:
            line += f" counterexample={res.kind} trace={res.trace}"
        print(line)
    if por.verdict == explore.UNSAFE:
        final = explore.replay(model, por.trace)
        print(f"   replay: trace reaches {format_state(final)}")
        if por.kind == "property_violation":
            assert not model.property_fn(final)
        else:
            assert not explore.enabled_actions(model, final)
        print("   replay: counterexample confirmed")
    print()
    return full, por


def main():
    results = {}
    for factory in examples.ALL_EXAMPLES:
        model = factory()
        results[model.name] = run(model)

    # Show the ignoring problem and how the cycle proviso fixes it.
    model = examples.flag_race()
    naive = explore.explore(model, use_por=True, cycle_proviso=False)
    print("== flag_race without cycle proviso (demonstrates the ignoring problem) ==")
    print(
        f"   naive POR: verdict={naive.verdict} states={naive.states_explored}"
        "  <-- WRONG: set_y is ignored forever along the toggle loop"
    )
    print()
    print("== reduction evidence for flag_race (with proviso) ==")
    por = results[model.name][1]
    print(explore.format_evidence(por))
    print()
    print("== reduction evidence for independent_counters (first 4 states) ==")
    por = results["independent_counters(n=2,k=3)"][1]
    print(explore.format_evidence(por, max_steps=4))


if __name__ == "__main__":
    main()

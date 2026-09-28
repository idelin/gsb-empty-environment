"""Runnable demonstration: symmetry condition, reduction, counterexample replay."""

from symcheck import PENDING, SAFE, VIOLATED, replay
from symcheck.examples import (
    at_most_one_critical,
    make_hidden_leader_model,
    make_mutex_model,
)
from symcheck.symmetry import check_symmetry_condition
from symcheck.checker import verify


def _format_state(state):
    return f"shared={state.shared} locals={state.locals}"


def main() -> None:
    print("== 1. Symmetry condition on the declared models ==")
    good = make_mutex_model(num_workers=2)
    holds, witness = check_symmetry_condition(good)
    print(f"mutex (1 leader + 2 workers): condition holds = {holds}")

    bad_model = make_hidden_leader_model()
    holds, witness = check_symmetry_condition(bad_model)
    print(f"hidden-leader model:          condition holds = {holds}")
    if witness is not None:
        state, perm, action, target, required_target = witness
        print(f"  witness: {_format_state(state)} --{action}--> {_format_state(target)}")
        print(f"  under permutation {perm} the matching transition is missing")

    print()
    print("== 2. Safe model: reduced vs unreduced exploration ==")
    reduced = verify(good, at_most_one_critical, use_symmetry=True)
    unreduced = verify(good, at_most_one_critical, use_symmetry=False)
    print(f"symmetry-reduced states explored: {reduced.explored}, verdict={reduced.status}")
    print(f"plain states explored:            {unreduced.explored}, verdict={unreduced.status}")

    print()
    print("== 3. Buggy model: counterexample with preserved ID mapping ==")
    buggy = make_mutex_model(num_workers=2, buggy=True)
    verdict = verify(buggy, at_most_one_critical, use_symmetry=True)
    print(f"verdict={verdict.status}, states explored={verdict.explored}")
    cex = verdict.counterexample
    for index, (state, perm) in enumerate(zip(cex.states, cex.id_maps)):
        print(f"  [{index}] {_format_state(state)}  id_map(orig->canonical)={perm}")
    print("  actions:", " -> ".join(f"{pid}:{name}" for pid, name in cex.actions))

    replayed = replay(buggy, cex.actions)
    print(f"  replay reproduces the trace: {replayed == cex.states}")
    print(f"  final state violates invariant: {not at_most_one_critical(replayed[-1])}")

    print()
    print("== 4. Unfinished search stays pending ==")
    larger = make_mutex_model(num_workers=3)
    pending = verify(
        larger,
        at_most_one_critical,
        use_symmetry=True,
        max_states=5,
    )
    print(f"budget=5 states explored={pending.explored}, verdict={pending.status}")
    assert pending.status == PENDING


if __name__ == "__main__":
    main()

"""Runnable samples for the transducer package.

Run from anywhere:  python3 examples/run_samples.py
"""
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from transducer import FST, Transition, k_best_outputs, min_cost_transduction


def show_steps(steps):
    for s in steps:
        consumed = repr(s.consumed) if s.consumed else "eps"
        produced = repr(s.produced) if s.produced else "''"
        print(
            f"    edge {s.edge_id}: {s.src} -> {s.dst}"
            f"  in {consumed}  out {produced}  cost {s.cost}"
            f"  (pos {s.pos_before}->{s.pos_after})"
        )


def sample_1_leet_encoder():
    print("== sample 1: weighted 'leet' encoder, input 'ab' ==")
    fst = FST(
        states={"q0"},
        alphabet={"a", "b"},
        transitions=[
            Transition(1, "q0", "q0", "a", "4", 1),
            Transition(2, "q0", "q0", "a", "a", 2),
            Transition(3, "q0", "q0", "b", "8", 1),
            Transition(4, "q0", "q0", "b", "b", 3),
            Transition(5, "q0", "q0", None, "!", 5),  # epsilon insertion
        ],
        start="q0",
        accepts={"q0"},
    )
    res = min_cost_transduction(fst, "ab")
    print(f"  min: cost={res.cost} output={res.output!r} (pops={res.pops})")
    show_steps(res.steps)
    kb = k_best_outputs(fst, "ab", k=6)
    print("  top-6 distinct outputs:")
    for r in kb.results:
        ids = [s.edge_id for s in r.steps]
        print(f"    #{r.rank} cost={r.cost} output={r.output!r} edges={ids}")
    print()


def sample_2_zero_cost_cycles():
    print("== sample 2: zero-cost silent cycle + zero-cost output loop ==")
    fst = FST(
        states={"q0", "q1", "q2"},
        alphabet={"a"},
        transitions=[
            Transition(1, "q0", "q1", None, "", 0),   # silent, cost 0
            Transition(2, "q1", "q0", None, "", 0),   # closes a silent cycle
            Transition(3, "q0", "q2", None, "", 0),   # straight to accept
            Transition(4, "q2", "q2", "a", "x", 0),   # zero-cost input loop
        ],
        start="q0",
        accepts={"q2"},
    )
    print(f"  zero-cost silent cycle diagnostic: {fst.find_zero_cost_silent_cycle()}")
    res = min_cost_transduction(fst, "aaa")
    print(f"  min: cost={res.cost} output={res.output!r} (pops={res.pops})")
    show_steps(res.steps)
    print("  (the silent cycle is normalised away; the search terminates)")
    print()


def sample_3_dedup_and_budget():
    print("== sample 3: duplicate outputs counted once; budget keeps frontier ==")
    diamond = FST(
        states={"q0", "q1", "q2", "q3"},
        alphabet={"a"},
        transitions=[
            Transition(1, "q0", "q1", "a", "x", 1),
            Transition(2, "q0", "q2", "a", "x", 1),
            Transition(3, "q1", "q3", None, "", 0),
            Transition(4, "q2", "q3", None, "", 0),
        ],
        start="q0",
        accepts={"q3"},
    )
    kb = k_best_outputs(diamond, "a", k=5)
    print(f"  diamond: distinct outputs = {kb.distinct_outputs} (paths agree on 'x')")
    for r in kb.results:
        ids = [s.edge_id for s in r.steps]
        print(f"    #{r.rank} cost={r.cost} output={r.output!r} edges={ids}")

    loop = FST(
        states={"q0"},
        alphabet={"a"},
        transitions=[Transition(1, "q0", "q0", None, "a", 0)],
        start="q0",
        accepts={"q0"},
    )
    kb2 = k_best_outputs(loop, "", k=10, budget=5)
    print(
        f"  zero-cost output loop, k=10 budget=5: got {kb2.distinct_outputs},"
        f" budget_exhausted={kb2.budget_exhausted}"
    )
    print(f"  frontier (pending keys): {kb2.frontier}")
    print()


def sample_4_failure():
    print("== sample 4: no accepting path -> failure ==")
    fst = FST(
        states={"q0", "q1"},
        alphabet={"a", "b"},
        transitions=[Transition(1, "q0", "q1", "a", "a", 1)],
        start="q0",
        accepts={"q1"},
    )
    res = min_cost_transduction(fst, "ab")
    print(f"  input 'ab': ok={res.ok} reason={res.reason!r}")


if __name__ == "__main__":
    sample_1_leet_encoder()
    sample_2_zero_cost_cycles()
    sample_3_dedup_and_budget()
    sample_4_failure()

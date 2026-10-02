"""Independently executable counterexamples.

Run:  python3 examples/counterexamples.py

Each cex_* function isolates one case where a naive algorithm misbehaves
(no minimum, no termination, overcounting, ...) and shows how the solver in
this repo handles it.  Every function is self-contained and can also be
imported and run on its own.
"""
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from transducer import FST, Transition, k_best_outputs, min_cost_transduction


def cex_1_zero_cost_silent_cycle_has_no_naive_minimum():
    """Edge-id tie-break alone has NO minimum on zero-cost silent cycles."""
    print("== cex 1: zero-cost silent cycle -> naive minimum does not exist ==")
    fst = FST(
        states={"q0", "q1", "q2"},
        alphabet={"a"},
        transitions=[
            Transition(1, "q0", "q1", None, "", 0),
            Transition(2, "q1", "q0", None, "", 0),
            Transition(3, "q0", "q2", None, "", 0),
        ],
        start="q0",
        accepts={"q2"},
    )
    print(f"  diagnostic find_zero_cost_silent_cycle -> {fst.find_zero_cost_silent_cycle()}")
    print("  all these paths have cost 0 and output '':")
    print("    edges (3,)            key (0, 0, '', (3,))")
    print("    edges (1, 2, 3)       key (0, 0, '', (1, 2, 3))       <- smaller")
    print("    edges (1, 2, 1, 2, 3) key (0, 0, '', (1, 2, 1, 2, 3)) <- smaller still ...")
    print("  => descending chain, no naive minimum; looping must be normalised away")
    res = min_cost_transduction(fst, "")
    ids = [s.edge_id for s in res.steps]
    print(f"  solver: ok={res.ok} cost={res.cost} output={res.output!r} edges={ids}")
    assert res.ok and res.output == "" and ids == [3]
    print()


def cex_2_zero_cost_output_cycle_has_infinitely_many_outputs():
    """A zero-cost loop that emits 'a' yields '', 'a', 'aa', ... forever."""
    print("== cex 2: zero-cost output cycle -> infinitely many distinct outputs ==")
    fst = FST(
        states={"q0"},
        alphabet={"a"},
        transitions=[Transition(1, "q0", "q0", None, "a", 0)],
        start="q0",
        accepts={"q0"},
    )
    print("  enumerating ALL outputs would never finish; the minimum still")
    print("  exists because output length is compared before the string")
    res = min_cost_transduction(fst, "")
    print(f"  min: cost={res.cost} output={res.output!r}")
    kb = k_best_outputs(fst, "", k=4)
    print(f"  top-4: {[r.output for r in kb.results]} (each popped in key order)")
    assert [r.output for r in kb.results] == ["", "a", "aa", "aaa"]
    print()


def cex_3_naive_k_shortest_paths_overcounts_duplicates():
    """k-shortest-paths counts paths; we count distinct outputs."""
    print("== cex 3: duplicate outputs must not be counted twice ==")
    fst = FST(
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
    print("  two paths (1,3) and (2,4) both produce 'x' at cost 1")
    print("  naive 2-shortest-paths would report 'x' twice")
    kb = k_best_outputs(fst, "a", k=2)
    print(f"  solver: distinct={kb.distinct_outputs} complete={kb.complete}")
    for r in kb.results:
        print(f"    #{r.rank} output={r.output!r} edges={[s.edge_id for s in r.steps]}")
    assert kb.distinct_outputs == 1
    print()


def cex_4_silent_cycle_naive_search_never_terminates():
    """Silent cycles make naive path enumeration spin forever."""
    print("== cex 4: silent cycle -> naive enumeration never terminates ==")
    fst = FST(
        states={"q0", "q1"},
        alphabet={"a"},
        transitions=[
            Transition(1, "q0", "q0", None, "", 2),   # silent self-loop, cost 2
            Transition(2, "q0", "q1", "a", "a", 1),
        ],
        start="q0",
        accepts={"q1"},
    )
    print("  edge 1 can be taken any number of times without changing the")
    print("  output; naive enumeration keeps generating longer paths forever")
    res = min_cost_transduction(fst, "a")
    ids = [s.edge_id for s in res.steps]
    print(f"  solver: cost={res.cost} output={res.output!r} edges={ids} pops={res.pops}")
    assert res.ok and res.cost == 1 and ids == [2]
    print()


def cex_5_no_accepting_path_returns_failure():
    """No path consumes the whole input and reaches an accept state."""
    print("== cex 5: no accepting path -> explicit failure ==")
    fst = FST(
        states={"q0", "q1"},
        alphabet={"a"},
        transitions=[Transition(1, "q0", "q1", "a", "a", 1)],
        start="q0",
        accepts={"q1"},
    )
    res = min_cost_transduction(fst, "aa")
    print(f"  input 'aa': ok={res.ok} reason={res.reason!r}")
    assert not res.ok
    print()


if __name__ == "__main__":
    cex_1_zero_cost_silent_cycle_has_no_naive_minimum()
    cex_2_zero_cost_output_cycle_has_infinitely_many_outputs()
    cex_3_naive_k_shortest_paths_overcounts_duplicates()
    cex_4_silent_cycle_naive_search_never_terminates()
    cex_5_no_accepting_path_returns_failure()
    print("all counterexamples behaved as documented")

"""Replayable end-to-end scenarios for the symbolic automata library.

Run from the repository root:   python3.11 examples/replay.py
Writes counterexample artifacts to examples/out/ and exits non-zero if any
scenario check fails.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from automata import (
    INCLUDED, NOT_INCLUDED, UNKNOWN,
    Complement, EmptinessSearch, InclusionCache, Interval, NFA,
    Status, SubsetConstruction, char_interval, check_inclusion, decode,
    encode, run_word, unified_cells,
)
from automata.lazy import Product

OUT_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "out")
FAILURES = []


def report(name, ok, detail=""):
    print(f"[{'PASS' if ok else 'FAIL'}] {name}" + (f" -- {detail}" if detail else ""))
    if not ok:
        FAILURES.append(name)


def scenario_overlapping_char_classes():
    # A: one char in [a-z]; B: one char in [c-q].  Neither includes the other.
    a = NFA(char_interval("a", "z"), name="A:[a-z]")
    a.set_initial(0); a.add_accept(1)
    a.add_transition(0, char_interval("a", "z"), 1)
    b = NFA(char_interval("a", "z"), name="B:[c-q]")
    b.set_initial(0); b.add_accept(1)
    b.add_transition(0, char_interval("c", "q"), 1)
    cells = unified_cells(a, b)
    report("partition of overlapping classes",
           [str(c) for c in cells] == ["[97,98]", "[99,113]", "[114,122]"],
           f"cells={cells}")
    res_ab = check_inclusion(a, b)
    res_ba = check_inclusion(b, a)
    ok = (res_ab.status == NOT_INCLUDED and decode(res_ab.witness.symbols) == "a"
          and res_ba.status == INCLUDED and res_ab.verified)
    report("A:[a-z] not subset of B:[c-q]; B subset of A", ok,
           f"A\\B witness={decode(res_ab.witness.symbols)!r}, B<=A: {res_ba.status}")


def scenario_epsilon_cycle():
    # epsilon cycle 0 -> 1 -> 0 plus 1 -> 2; accepts "", "x".
    a = NFA(char_interval("a", "z"), name="A:eps-cycle")
    a.set_initial(0)
    a.add_epsilon(0, 1); a.add_epsilon(1, 0); a.add_epsilon(1, 2)
    a.add_accept(2)
    a.add_transition(2, char_interval("x"), 3)
    a.add_accept(3)
    b = NFA(char_interval("a", "z"), name="B:x-only")
    b.set_initial(0); b.add_accept(1)
    b.add_transition(0, char_interval("x"), 1)
    res = check_inclusion(a, b)
    ok = (res.status == NOT_INCLUDED and res.witness.symbols == () and res.verified
          and a.simulate(()) and a.simulate(encode("x")))
    report("epsilon cycle, empty-string counterexample", ok,
           f"witness={res.witness.symbols!r} (empty word)")


def scenario_large_intervals():
    # Alphabet [0, 2^32]; A accepts exactly one symbol in [0, 2^31],
    # B accepts exactly one symbol in [2^20, 2^40-ish capped].
    big = Interval(0, 2**32)
    a = NFA(big, name="A:[0,2^31]")
    a.set_initial(0); a.add_accept(1)
    a.add_transition(0, Interval(0, 2**31), 1)
    b = NFA(big, name="B:[2^20,2^32]")
    b.set_initial(0); b.add_accept(1)
    b.add_transition(0, Interval(2**20, 2**32), 1)
    res = check_inclusion(a, b)
    ok = (res.status == NOT_INCLUDED and res.witness.symbols == (0,)
          and res.verified and res.states_explored < 20)
    report("huge alphabet handled by partition (no enumeration)", ok,
           f"witness symbol={res.witness.symbols[0]}, states={res.states_explored}")


def scenario_operations():
    # Intersection / union / difference / complement membership checks.
    a = NFA(char_interval("a", "z"), name="A:len1")
    a.set_initial(0); a.add_accept(1)
    a.add_transition(0, char_interval("a", "z"), 1)
    b = NFA(char_interval("a", "z"), name="B:len1or2")
    b.set_initial(0); b.add_accept(1); b.add_accept(2)
    b.add_transition(0, char_interval("a", "z"), 1)
    b.add_transition(1, char_interval("a", "z"), 2)
    cells = unified_cells(a, b)
    da, db = SubsetConstruction(a, cells), SubsetConstruction(b, cells)
    inter = Product(da, db, "inter")
    union = Product(da, db, "union")
    diff = Product(db, da, "diff")
    comp = Complement(SubsetConstruction(a))
    ok = (run_word(inter, encode("q")) and not run_word(inter, encode("qq"))
          and run_word(union, encode("qq")) and not run_word(union, encode("qqq"))
          and run_word(diff, encode("qq")) and not run_word(diff, encode("q"))
          and run_word(comp, ()) and run_word(comp, encode("qq"))
          and not run_word(comp, encode("q")))
    report("intersection/union/difference/complement", ok)


def scenario_blowup_budget_resume():
    # NFA whose determinization has 2^12 reachable subsets, empty language.
    nfa = NFA(Interval(0, 1), name="blowup12")
    nfa.set_initial(0)
    nfa.add_transition(0, Interval(0, 1), 0)
    nfa.add_transition(0, Interval(1, 1), 1)
    for i in range(1, 12):
        nfa.add_transition(i, Interval(0, 1), i + 1)
    search = EmptinessSearch(SubsetConstruction(nfa), state_budget=1000)
    status1 = search.run()
    explored_at_unknown = search.states_explored
    status2 = search.resume(state_budget=100_000)   # pause -> resume
    ok = (status1 is Status.UNKNOWN and explored_at_unknown == 1000
          and status2 is Status.EMPTY and search.states_explored == 2**12)
    report("exponential blowup: budget -> UNKNOWN, resume -> decided", ok,
           f"UNKNOWN at {explored_at_unknown} states, "
           f"resumed to EMPTY with {search.states_explored} states")


def scenario_pause_resume():
    nfa = NFA(Interval(0, 1), name="nth-from-end-6")
    nfa.set_initial(0)
    nfa.add_transition(0, Interval(0, 1), 0)
    nfa.add_transition(0, Interval(1, 1), 1)
    for i in range(1, 6):
        nfa.add_transition(i, Interval(0, 1), i + 1)
    nfa.add_accept(6)
    incremental = EmptinessSearch(SubsetConstruction(nfa))
    while incremental.step(1) is Status.RUNNING:
        pass
    ok = (incremental.status is Status.NONEMPTY
          and incremental.witness.symbols == (1, 0, 0, 0, 0, 0)
          and nfa.simulate(incremental.witness.symbols))
    report("step-by-step pausable search finds shortest word", ok,
           f"witness={incremental.witness.symbols}")


def scenario_cache_invalidation():
    a = NFA(char_interval("a", "z"), name="A:[a-z]")
    a.set_initial(0); a.add_accept(1)
    a.add_transition(0, char_interval("a", "z"), 1)
    b = NFA(char_interval("a", "z"), name="B:[a-m]")
    b.set_initial(0); b.add_accept(1)
    edge = b.add_transition(0, char_interval("a", "m"), 1)
    cache = InclusionCache()
    first = cache.check(a, b)
    cached = cache.check(a, b)          # cache hit, same object
    b.replace_transition(0, edge, interval=char_interval("a", "z"))
    third = cache.check(a, b)           # version bumped -> re-decided
    ok = (first.status == NOT_INCLUDED and cached is first
          and third.status == INCLUDED and third is not first
          and cache.hits == 1 and cache.misses == 2)
    report("edge edit locally invalidates cached inclusion verdict", ok,
           f"before={first.status} after={third.status} "
           f"hits={cache.hits} misses={cache.misses}")


def scenario_counterexample_artifact():
    # Produce a counterexample with per-step evidence, dump it, and let the
    # standalone verifier (independent interpreter) re-check it.
    a = NFA(char_interval("a", "z"), name="A:len<=3")
    a.set_initial(0)
    for i in range(3):
        a.add_transition(i, char_interval("a", "z"), i + 1)
        a.add_accept(i + 1)
    b = NFA(char_interval("a", "z"), name="B:len<=2")
    b.set_initial(0)
    for i in range(2):
        b.add_transition(i, char_interval("a", "z"), i + 1)
        b.add_accept(i + 1)
    res = check_inclusion(a, b)
    os.makedirs(OUT_DIR, exist_ok=True)
    case_path = os.path.join(OUT_DIR, "counterexample.json")
    with open(case_path, "w", encoding="utf-8") as fh:
        json.dump({
            "nfa_a": a.to_dict(),
            "nfa_b": b.to_dict(),
            "word": list(res.witness.symbols),
            "witness": res.witness.to_dict(),
        }, fh, indent=2)
    evidence_ok = all(
        step.cell.contains(step.symbol) for step in res.witness.steps
    )
    ok = (res.status == NOT_INCLUDED and res.witness.symbols == encode("aaa")
          and res.verified and evidence_ok)
    report("counterexample artifact with per-step edge evidence", ok,
           f"witness={decode(res.witness.symbols)!r} -> {case_path}")
    print("       verify standalone: python3.11 -m automata.verify " + case_path)


def main():
    print("== symbolic automata replay ==")
    scenario_overlapping_char_classes()
    scenario_epsilon_cycle()
    scenario_large_intervals()
    scenario_operations()
    scenario_blowup_budget_resume()
    scenario_pause_resume()
    scenario_cache_invalidation()
    scenario_counterexample_artifact()
    if FAILURES:
        print(f"FAILED: {len(FAILURES)} scenario(s): {FAILURES}")
        return 1
    print("all scenarios passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())

"""Replayable end-to-end samples: ``python -m automata.samples``.

Each sample builds its automata from explicit alphabets and transitions,
runs the inclusion machinery, verifies any counterexample with the
independent interpreter, prints a one-line summary and returns a dict.
"""
from __future__ import annotations

import json

from .determinize import LazyDFA
from .inclusion import InclusionChecker, InclusionSearch, Status
from .nfa import NFA
from .symbols import Interval, SymbolSet
from .verify import verify_counterexample


def _fmt_word(word) -> str:
    if not word:
        return "(empty string)"
    if all(32 <= s < 127 for s in word):
        return repr("".join(chr(s) for s in word))
    return str(list(word))


def _single_symbol_nfa(universe: SymbolSet, lo: int, hi: int, name: str) -> NFA:
    nfa = NFA(universe, 2, 0, finals={1}, name=name)
    nfa.add_edge(0, SymbolSet.range(lo, hi), 1)
    return nfa


def make_nth_from_last(n: int) -> NFA:
    """NFA over {0,1} accepting words whose n-th symbol from the end is 1.

    The minimal DFA needs 2^(n-1) states: the classic exponential blowup.
    """
    universe = SymbolSet.range(0, 1)
    nfa = NFA(universe, n, 0, finals={n - 1}, name=f"nth-from-last-{n}")
    nfa.add_edge(0, SymbolSet.point(0), 0)
    nfa.add_edge(0, SymbolSet.point(1), 0)
    nfa.add_edge(0, SymbolSet.point(1), 1)
    for i in range(1, n - 1):
        nfa.add_edge(i, SymbolSet.range(0, 1), i + 1)
    return nfa


def sample_overlapping_char_classes() -> dict:
    """Overlapping classes [A-Za-z_] vs [a-z]: boundaries are unified first."""
    uni = SymbolSet.range(0, 127)
    ident = NFA(uni, 2, 0, finals={1}, name="identifier")
    ident.add_edge(0, SymbolSet([Interval(65, 90), Interval(97, 122), Interval(95, 95)]), 1)
    ident.add_edge(1, SymbolSet([Interval(65, 90), Interval(97, 122),
                                 Interval(48, 57), Interval(95, 95)]), 1)
    lower = NFA(uni, 2, 0, finals={1}, name="lower-word")
    lower.add_edge(0, SymbolSet.char_range("a", "z"), 1)
    lower.add_edge(1, SymbolSet([Interval(97, 122), Interval(48, 57)]), 1)

    r_ab = InclusionChecker(ident, lower).check()
    r_ba = InclusionChecker(lower, ident).check()
    problems = verify_counterexample(ident, lower, r_ab.counterexample)
    assert r_ab.status is Status.NOT_INCLUDED and not problems
    assert r_ba.status is Status.INCLUDED
    print(f"[1] overlapping char classes: ident⊆lower -> {r_ab.status.value}, "
          f"cex={_fmt_word(r_ab.counterexample.word)} (verified); "
          f"lower⊆ident -> {r_ba.status.value}")
    return {"name": "overlapping_char_classes", "ab": r_ab.status.value,
            "ba": r_ba.status.value, "cex": list(r_ab.counterexample.word)}


def sample_epsilon_loops() -> dict:
    """Epsilon cycle 0<->1 must not hang closure; both inclusions hold."""
    uni = SymbolSet.range(0, 127)
    a = NFA(uni, 3, 0, finals={2}, name="eps-loop")
    a.add_epsilon(0, 1)
    a.add_epsilon(1, 0)          # epsilon cycle
    a.add_edge(1, SymbolSet.chars("a"), 2)
    b = NFA(uni, 2, 0, finals={1}, name="plain")
    b.add_edge(0, SymbolSet.chars("a"), 1)
    r_ab = InclusionChecker(a, b).check()
    r_ba = InclusionChecker(b, a).check()
    assert r_ab.status is Status.INCLUDED and r_ba.status is Status.INCLUDED
    print(f"[2] epsilon loops: eps-loop≡plain, both inclusions "
          f"{r_ab.status.value}/{r_ba.status.value}")
    return {"name": "epsilon_loops", "ab": r_ab.status.value, "ba": r_ba.status.value}


def sample_large_intervals() -> dict:
    """Alphabet of 2^32 symbols handled by boundary arithmetic only."""
    uni = SymbolSet.range(0, 2**32 - 1)
    a = _single_symbol_nfa(uni, 2**20, 2**21, "A=[2^20,2^21]")
    b = _single_symbol_nfa(uni, 2**19, 2**21 + 5, "B=[2^19,2^21+5]")
    r_ab = InclusionChecker(a, b).check()
    r_ba = InclusionChecker(b, a).check()
    problems = verify_counterexample(b, a, r_ba.counterexample)
    assert r_ab.status is Status.INCLUDED
    assert r_ba.status is Status.NOT_INCLUDED and not problems
    print(f"[3] large intervals (|Σ|=2^32): A⊆B -> {r_ab.status.value}; "
          f"B⊆A -> {r_ba.status.value}, cex={_fmt_word(r_ba.counterexample.word)} (verified)")
    return {"name": "large_intervals", "ab": r_ab.status.value,
            "ba": r_ba.status.value, "cex": list(r_ba.counterexample.word)}


def sample_empty_string_counterexample() -> dict:
    """A accepts ε, B does not: the empty string is the counterexample."""
    uni = SymbolSet.range(0, 255)
    a = NFA(uni, 1, 0, finals={0}, name="accepts-eps")
    b = NFA(uni, 1, 0, finals=set(), name="accepts-nothing")
    r = InclusionChecker(a, b).check()
    problems = verify_counterexample(a, b, r.counterexample)
    assert r.status is Status.NOT_INCLUDED and r.counterexample.word == () and not problems
    print(f"[4] empty string: cex={_fmt_word(r.counterexample.word)} (verified)")
    return {"name": "empty_string", "status": r.status.value, "cex": []}


def sample_dead_state() -> dict:
    """B lacks a transition on 'b': the search falls into the dead state."""
    uni = SymbolSet.range(0, 127)
    a = NFA(uni, 3, 0, finals={2}, name="ab")
    a.add_edge(0, SymbolSet.chars("a"), 1)
    a.add_edge(1, SymbolSet.chars("b"), 2)
    b = NFA(uni, 2, 0, finals={1}, name="a-only")
    b.add_edge(0, SymbolSet.chars("a"), 1)
    r = InclusionChecker(a, b).check()
    problems = verify_counterexample(a, b, r.counterexample)
    last = r.counterexample.steps[-1]
    assert r.status is Status.NOT_INCLUDED and not problems
    assert r.counterexample.final_b == frozenset() and last.edges_b == ()
    print(f"[5] dead state: cex={_fmt_word(r.counterexample.word)}, "
          f"B-side dies (edges_b at last step: {len(last.edges_b)}) (verified)")
    return {"name": "dead_state", "status": r.status.value,
            "cex": list(r.counterexample.word)}


def sample_unified_alphabets() -> dict:
    """A and B declare different alphabet boundaries; partitions are unified."""
    a = _single_symbol_nfa(SymbolSet.range(0, 200), 0, 200, "A(Σ=0..200)")
    b = _single_symbol_nfa(SymbolSet.range(0, 300), 0, 300, "B(Σ=0..300)")
    r_ab = InclusionChecker(a, b).check()
    r_ba = InclusionChecker(b, a).check()
    problems = verify_counterexample(b, a, r_ba.counterexample)
    assert r_ab.status is Status.INCLUDED
    assert r_ba.status is Status.NOT_INCLUDED and not problems
    assert r_ba.counterexample.word == (201,)
    print(f"[6] unified alphabets: A⊆B -> {r_ab.status.value}; "
          f"B⊆A -> {r_ba.status.value}, cex={_fmt_word(r_ba.counterexample.word)} (verified)")
    return {"name": "unified_alphabets", "ab": r_ab.status.value,
            "ba": r_ba.status.value, "cex": list(r_ba.counterexample.word)}


def sample_exponential_blowup() -> dict:
    """nth-from-last NFA: budget exhaustion -> UNDECIDED, resume -> INCLUDED."""
    n = 11
    a, b = make_nth_from_last(n), make_nth_from_last(n)
    dfa_a, dfa_b = LazyDFA(a), LazyDFA(b)
    search = InclusionSearch(dfa_a, dfa_b, budget=100)
    st1 = search.run()
    assert st1 is Status.UNDECIDED and search.expanded == 100
    search.budget = 10_000_000   # resume with a larger budget
    st2 = search.run()
    assert st2 is Status.INCLUDED
    print(f"[7] exponential blowup: n={n} NFA states -> {dfa_a.computed_states} "
          f"determinized states; budget=100 -> {st1.value}, "
          f"resumed -> {st2.value} after {search.expanded} expansions")
    return {"name": "exponential_blowup", "first": st1.value, "second": st2.value,
            "expanded": search.expanded, "dfa_states": dfa_a.computed_states}


def sample_pause_resume() -> dict:
    """Pause mid-search, snapshot to JSON, restore, finish with same result."""
    uni = SymbolSet.range(0, 9)
    a = NFA(uni, 4, 0, finals={3}, name="any-3-symbols")
    for i in range(3):
        a.add_edge(i, SymbolSet.range(0, 9), i + 1)
    b = NFA(uni, 4, 0, finals={3}, name="first-symbol<=5")
    b.add_edge(0, SymbolSet.range(0, 5), 1)
    b.add_edge(1, SymbolSet.range(0, 9), 2)
    b.add_edge(2, SymbolSet.range(0, 9), 3)

    direct = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=10_000)
    direct.run()
    stepped = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=10_000)
    stepped.step(2)                       # pause after two expansions
    snap = json.loads(json.dumps(stepped.snapshot()))   # JSON round-trip
    restored = InclusionSearch.restore(stepped.dfa_a, stepped.dfa_b, snap)
    restored.run()
    assert direct.status is Status.NOT_INCLUDED
    assert restored.status == direct.status
    assert restored.counterexample.word == direct.counterexample.word
    assert restored.expanded == direct.expanded
    problems = verify_counterexample(a, b, restored.counterexample)
    assert not problems
    print(f"[8] pause/resume: snapshot after 2 expansions, JSON round-trip, "
          f"resumed -> {restored.status.value}, "
          f"cex={_fmt_word(restored.counterexample.word)} == direct run (verified)")
    return {"name": "pause_resume", "status": restored.status.value,
            "cex": list(restored.counterexample.word)}


def sample_cache_invalidation() -> dict:
    """Editing one symbol edge locally invalidates the product cache."""
    uni = SymbolSet.range(0, 127)

    def build(name: str) -> NFA:
        nfa = NFA(uni, 3, 0, finals={2}, name=name)
        nfa.add_edge(0, SymbolSet.chars("a"), 1)
        nfa.add_edge(1, SymbolSet.chars("b"), 2)
        return nfa

    a, b = build("A:ab"), build("B:ab")
    checker = InclusionChecker(a, b)
    r1 = checker.check()
    assert r1.status is Status.INCLUDED
    cache_before = len(checker.product_cache)

    edge = b.edges[1][0]                       # the B-edge 1 -b-> 2
    b.replace_edge_symbols(edge.id, SymbolSet.chars("c"))
    r2 = checker.check()                       # conclusion recomputed, not reused
    assert r2.status is Status.NOT_INCLUDED
    problems = verify_counterexample(a, b, r2.counterexample)
    assert not problems
    log = checker.invalidation_log[-1]
    assert log["removed"] > 0 and log["survived"] > 0
    print(f"[9] cache invalidation: INCLUDED -> edit B edge 1-b->2 to 1-c->2 -> "
          f"{r2.status.value}, cex={_fmt_word(r2.counterexample.word)} (verified); "
          f"product cache {cache_before} entries, {log['removed']} locally "
          f"invalidated, {log['survived']} survived")
    return {"name": "cache_invalidation", "before": r1.status.value,
            "after": r2.status.value, "cex": list(r2.counterexample.word),
            "invalidated": log["removed"], "survived": log["survived"]}


ALL_SAMPLES = [
    sample_overlapping_char_classes,
    sample_epsilon_loops,
    sample_large_intervals,
    sample_empty_string_counterexample,
    sample_dead_state,
    sample_unified_alphabets,
    sample_exponential_blowup,
    sample_pause_resume,
    sample_cache_invalidation,
]


def run_all() -> list:
    return [sample() for sample in ALL_SAMPLES]


if __name__ == "__main__":
    results = run_all()
    print(f"\n{len(results)} samples replayed successfully.")

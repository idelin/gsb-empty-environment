"""Unittest suite for the symbolic automata library (Python 3.11, stdlib only)."""
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from automata import (
    INCLUDED, NOT_INCLUDED, UNKNOWN,
    Complement, EmptinessSearch, InclusionCache, Interval, NFA, Product,
    Status, SubsetConstruction, Witness, char_interval, check_inclusion,
    decode, encode, partition, run_word, unified_cells, verify_counterexample,
)
from automata.verify import verify_case


def char_nfa(name="A"):
    return NFA(char_interval(chr(0), chr(0x10FFFF)), name=name)


class TestPartition(unittest.TestCase):
    def test_overlapping_char_classes(self):
        cells = partition([char_interval("a", "z"), char_interval("c", "q")])
        self.assertEqual(
            cells,
            [char_interval("a", "b"), char_interval("c", "q"), char_interval("r", "z")],
        )

    def test_large_intervals_not_enumerated(self):
        big = Interval(0, 2**40)
        cells = partition(
            [Interval(0, 2**39 - 1), Interval(2**39, 2**40 - 1)], domain=big
        )
        self.assertEqual(
            cells,
            [Interval(0, 2**39 - 1), Interval(2**39, 2**40 - 1), Interval(2**40, 2**40)],
        )
        self.assertLess(len(cells), 10)

    def test_unify_inconsistent_alphabet_bounds(self):
        a = NFA(Interval(0, 100), name="A")
        a.set_initial(0)
        a.add_transition(0, Interval(10, 50), 0)
        b = NFA(Interval(20, 200), name="B")
        b.set_initial(0)
        b.add_transition(0, Interval(30, 70), 0)
        cells = unified_cells(a, b)
        # Unified domain [0,200] refined by all four boundaries.
        self.assertEqual(cells[0], Interval(0, 9))
        self.assertEqual(cells[-1], Interval(71, 200))
        self.assertIn(Interval(10, 29), cells)
        self.assertIn(Interval(30, 50), cells)
        self.assertIn(Interval(51, 70), cells)
        # Cells are disjoint and contiguous over the unified domain.
        for left, right in zip(cells, cells[1:]):
            self.assertEqual(left.hi + 1, right.lo)


class TestNFAInterpreter(unittest.TestCase):
    def test_simulate_basic(self):
        nfa = char_nfa()
        nfa.set_initial(0)
        nfa.add_accept(1)
        nfa.add_transition(0, char_interval("a", "z"), 1)
        self.assertTrue(nfa.simulate(encode("q")))
        self.assertFalse(nfa.simulate(encode("qq")))
        self.assertFalse(nfa.simulate(encode("Q")))

    def test_epsilon_cycle_closure_terminates(self):
        nfa = char_nfa()
        nfa.set_initial(0)
        nfa.add_epsilon(0, 1)
        nfa.add_epsilon(1, 0)   # epsilon cycle
        nfa.add_epsilon(1, 2)
        nfa.add_accept(2)
        nfa.add_transition(2, char_interval("x"), 3)
        nfa.add_accept(3)
        self.assertEqual(nfa.epsilon_closure({0}), frozenset({0, 1, 2}))
        self.assertTrue(nfa.simulate(()))            # empty word accepted
        self.assertTrue(nfa.simulate(encode("x")))

    def test_serialization_roundtrip(self):
        nfa = char_nfa("R")
        nfa.set_initial(0)
        nfa.add_accept(2)
        nfa.add_transition(0, char_interval("a", "m"), 1)
        nfa.add_epsilon(1, 2)
        clone = NFA.from_dict(json.loads(json.dumps(nfa.to_dict())))
        for word in [(), encode("a"), encode("z"), encode("aa")]:
            self.assertEqual(nfa.simulate(word), clone.simulate(word))


class TestLazyOperations(unittest.TestCase):
    def _pair(self):
        # A: words over [a-z] of length exactly 1
        a = NFA(char_interval("a", "z"), name="A")
        a.set_initial(0)
        a.add_accept(1)
        a.add_transition(0, char_interval("a", "z"), 1)
        # B: words over [a-z] of length 1 or 2
        b = NFA(char_interval("a", "z"), name="B")
        b.set_initial(0)
        b.add_accept(1)
        b.add_accept(2)
        b.add_transition(0, char_interval("a", "z"), 1)
        b.add_transition(1, char_interval("a", "z"), 2)
        return a, b

    def test_intersection_union_difference(self):
        a, b = self._pair()
        cells = unified_cells(a, b)
        da, db = SubsetConstruction(a, cells), SubsetConstruction(b, cells)
        inter = Product(da, db, "inter")
        union = Product(da, db, "union")
        diff = Product(da, db, "diff")
        self.assertTrue(run_word(inter, encode("x")))
        self.assertFalse(run_word(inter, encode("xy")))
        self.assertTrue(run_word(union, encode("xy")))
        self.assertFalse(run_word(union, encode("xyz")))
        self.assertFalse(run_word(diff, encode("x")))
        self.assertFalse(run_word(diff, encode("xy")))
        # B \ A accepts exactly the length-2 words.
        diff_ba = Product(db, da, "diff")
        self.assertTrue(run_word(diff_ba, encode("xy")))
        self.assertFalse(run_word(diff_ba, encode("x")))

    def test_complement_with_dead_state(self):
        # Complement of "exactly one [a-z] char" over alphabet [a-z]:
        # accepts empty word and words of length >= 2.  The dead state
        # (no outgoing transition) becomes accepting.
        a, _ = self._pair()
        comp = Complement(SubsetConstruction(a))
        self.assertTrue(run_word(comp, ()))
        self.assertFalse(run_word(comp, encode("x")))
        self.assertTrue(run_word(comp, encode("xyz")))

    def test_missing_transition_goes_to_dead_state(self):
        nfa = char_nfa()
        nfa.set_initial(0)
        nfa.add_accept(1)
        nfa.add_transition(0, char_interval("a", "c"), 1)  # no edge for 'z'
        det = SubsetConstruction(nfa)
        dead, evidence = det.step(det.initial(), char_interval("z"))
        self.assertEqual(dead, frozenset())     # explicit dead state
        self.assertEqual(evidence, ())          # no original edge fired
        self.assertFalse(det.is_accept(dead))


class TestInclusion(unittest.TestCase):
    def test_included_and_not_included(self):
        a, b = TestLazyOperations()._pair()
        self.assertEqual(check_inclusion(a, b).status, INCLUDED)
        result = check_inclusion(b, a)
        self.assertEqual(result.status, NOT_INCLUDED)
        self.assertTrue(result.verified)
        self.assertEqual(len(result.witness.symbols), 2)  # shortest

    def test_empty_string_counterexample(self):
        a = char_nfa("A")
        a.set_initial(0)
        a.add_accept(0)          # L(A) contains the empty word
        b = char_nfa("B")
        b.set_initial(0)
        b.add_transition(0, char_interval("a"), 0)  # L(B) does not
        result = check_inclusion(a, b)
        self.assertEqual(result.status, NOT_INCLUDED)
        self.assertEqual(result.witness.symbols, ())
        self.assertTrue(result.verified)
        self.assertTrue(verify_counterexample(a, b, ()))

    def test_witness_evidence_matches_original_edges(self):
        a, b = TestLazyOperations()._pair()
        result = check_inclusion(b, a)
        witness = result.witness
        self.assertEqual(len(witness.steps), 2)
        nfas = {"A": a, "B": b}
        for step, symbol in zip(witness.steps, witness.symbols):
            self.assertTrue(step.cell.contains(symbol))
            for edge in step.edges:
                original = nfas[edge.automaton].transitions[edge.src]
                self.assertIn((edge.interval, edge.dst), original)
                self.assertTrue(edge.interval.contains(symbol))
        # Second step: A has no edge out of state 1 -> dead-state evidence.
        last_step_a_edges = [
            e for e in witness.steps[-1].edges if e.automaton == "A"
        ]
        self.assertEqual(last_step_a_edges, [])

    def test_shortest_distinguishing_string(self):
        # A accepts "a", "ab", "abc"; B accepts only "a" and "ab".
        a = char_nfa("A")
        a.set_initial(0)
        for i in range(3):
            a.add_transition(i, char_interval(chr(ord("a") + i)), i + 1)
            a.add_accept(i + 1)
        b = char_nfa("B")
        b.set_initial(0)
        for i in range(2):
            b.add_transition(i, char_interval(chr(ord("a") + i)), i + 1)
            b.add_accept(i + 1)
        result = check_inclusion(a, b)
        self.assertEqual(result.status, NOT_INCLUDED)
        self.assertEqual(result.witness.symbols, encode("abc"))  # shortest
        self.assertEqual(decode(result.witness.symbols), "abc")

    def test_witness_serialization_roundtrip(self):
        a, b = TestLazyOperations()._pair()
        witness = check_inclusion(b, a).witness
        clone = Witness.from_dict(json.loads(json.dumps(witness.to_dict())))
        self.assertEqual(witness, clone)


class TestBudgetPauseResume(unittest.TestCase):
    @staticmethod
    def blowup_nfa(n, with_accept=True):
        # Classic NFA whose minimal DFA has 2^n states: words over {0,1}
        # whose n-th symbol from the end is 1.
        nfa = NFA(Interval(0, 1), name=f"blowup{n}")
        nfa.set_initial(0)
        nfa.add_transition(0, Interval(0, 1), 0)
        nfa.add_transition(0, Interval(1, 1), 1)
        for i in range(1, n):
            nfa.add_transition(i, Interval(0, 1), i + 1)
        if with_accept:
            nfa.add_accept(n)
        return nfa

    def test_determinization_exponential_blowup_unknown(self):
        nfa = self.blowup_nfa(12, with_accept=False)  # empty language
        search = EmptinessSearch(SubsetConstruction(nfa), state_budget=1000)
        self.assertIs(search.run(), Status.UNKNOWN)   # budget exhausted
        self.assertEqual(search.states_explored, 1000)
        # Resume with a raised budget: all 2^12 subsets get explored.
        self.assertIs(search.resume(state_budget=100_000), Status.EMPTY)
        self.assertEqual(search.states_explored, 2**12)

    def test_pause_resume_equivalence(self):
        nfa = self.blowup_nfa(6)
        # Word 10...0 (1 followed by 5 zeros) is the shortest accepted word.
        cells = nfa.cells()
        incremental = EmptinessSearch(SubsetConstruction(nfa, cells))
        while incremental.step(1) is Status.RUNNING:
            pass
        direct = EmptinessSearch(SubsetConstruction(nfa, cells))
        direct.run()
        self.assertEqual(incremental.status, direct.status)
        self.assertEqual(incremental.witness, direct.witness)
        self.assertEqual(incremental.witness.symbols, (1, 0, 0, 0, 0, 0))
        self.assertTrue(nfa.simulate(incremental.witness.symbols))

    def test_inclusion_unknown_on_budget(self):
        a = self.blowup_nfa(10)
        b = self.blowup_nfa(10)
        result = check_inclusion(a, b, state_budget=50)
        self.assertEqual(result.status, UNKNOWN)
        result = check_inclusion(a, b)
        self.assertEqual(result.status, INCLUDED)


class TestCacheInvalidation(unittest.TestCase):
    def test_edge_modification_invalidates_cached_verdict(self):
        a = char_nfa("A")
        a.set_initial(0)
        a.add_accept(1)
        a.add_transition(0, char_interval("a", "z"), 1)
        b = char_nfa("B")
        b.set_initial(0)
        b.add_accept(1)
        edge = b.add_transition(0, char_interval("a", "m"), 1)  # B: only [a-m]
        cache = InclusionCache()
        first = cache.check(a, b)
        self.assertEqual(first.status, NOT_INCLUDED)  # e.g. "z"
        self.assertEqual(first.witness.symbols, encode("n"))  # shortest
        second = cache.check(a, b)
        self.assertIs(second, first)                  # cached, same object
        self.assertEqual(cache.hits, 1)
        # Modify one symbolic edge of B: now B covers all of [a-z].
        b.replace_transition(0, edge, interval=char_interval("a", "z"))
        third = cache.check(a, b)
        self.assertEqual(third.status, INCLUDED)      # fresh verdict
        self.assertIsNot(third, first)
        self.assertEqual(cache.misses, 2)
        # A cached miss must not resurrect the stale conclusion.
        fourth = cache.check(a, b)
        self.assertIs(fourth, third)


class TestStandaloneVerification(unittest.TestCase):
    def test_verify_case_file(self):
        a, b = TestLazyOperations()._pair()
        result = check_inclusion(b, a)
        case = {
            "nfa_a": b.to_dict(),
            "nfa_b": a.to_dict(),
            "word": list(result.witness.symbols),
        }
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "case.json")
            with open(path, "w", encoding="utf-8") as fh:
                json.dump(case, fh)
            self.assertTrue(verify_case(path))
            # A non-counterexample must be rejected by the verifier.
            case["word"] = list(encode("x"))
            with open(path, "w", encoding="utf-8") as fh:
                json.dump(case, fh)
            self.assertFalse(verify_case(path))


if __name__ == "__main__":
    unittest.main()

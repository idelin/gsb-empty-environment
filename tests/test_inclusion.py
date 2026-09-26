import json
import unittest

from automata.determinize import LazyDFA
from automata.inclusion import InclusionChecker, InclusionSearch, Status
from automata.nfa import NFA
from automata.samples import make_nth_from_last
from automata.symbols import Interval, SymbolSet
from automata.verify import verify_counterexample

UNI = SymbolSet.range(0, 127)


def single_symbol(lo, hi, universe=UNI):
    nfa = NFA(universe, 2, 0, finals={1})
    nfa.add_edge(0, SymbolSet.range(lo, hi), 1)
    return nfa


class TestBasicInclusion(unittest.TestCase):
    def test_included(self):
        a, b = single_symbol(10, 20), single_symbol(0, 30)
        result = InclusionChecker(a, b).check()
        self.assertIs(result.status, Status.INCLUDED)
        self.assertIsNone(result.counterexample)

    def test_not_included_shortest(self):
        a, b = single_symbol(0, 30), single_symbol(10, 20)
        result = InclusionChecker(a, b).check()
        self.assertIs(result.status, Status.NOT_INCLUDED)
        cex = result.counterexample
        self.assertEqual(len(cex.word), 1)  # shortest distinguishing string
        self.assertIn(cex.word[0], set(range(0, 10)) | set(range(21, 31)))
        self.assertEqual(verify_counterexample(a, b, cex), [])

    def test_empty_string_counterexample(self):
        a = NFA(UNI, 1, 0, finals={0})
        b = NFA(UNI, 1, 0, finals=set())
        result = InclusionChecker(a, b).check()
        self.assertIs(result.status, Status.NOT_INCLUDED)
        self.assertEqual(result.counterexample.word, ())
        self.assertEqual(result.counterexample.steps, ())
        self.assertEqual(verify_counterexample(a, b, result.counterexample), [])

    def test_shortest_word_length_two(self):
        # A: any 2-symbol word over {0,1}; B: words starting with 0.
        uni = SymbolSet.range(0, 1)
        a = NFA(uni, 3, 0, finals={2})
        a.add_edge(0, SymbolSet.range(0, 1), 1)
        a.add_edge(1, SymbolSet.range(0, 1), 2)
        b = NFA(uni, 3, 0, finals={2})
        b.add_edge(0, SymbolSet.point(0), 1)
        b.add_edge(1, SymbolSet.range(0, 1), 2)
        result = InclusionChecker(a, b).check()
        self.assertIs(result.status, Status.NOT_INCLUDED)
        self.assertEqual(result.counterexample.word, (1, 0))
        self.assertEqual(verify_counterexample(a, b, result.counterexample), [])


class TestBoundariesAndDeadStates(unittest.TestCase):
    def test_boundary_unification_atoms(self):
        # A uses [0,100], B uses [50,150]: the product must refine to
        # [0,49] [50,100] [101,150] [151,200].
        uni = SymbolSet.range(0, 200)
        a, b = single_symbol(0, 100, uni), single_symbol(50, 150, uni)
        checker = InclusionChecker(a, b)
        result = checker.check()
        self.assertIs(result.status, Status.NOT_INCLUDED)
        cex = result.counterexample
        self.assertEqual(cex.steps[0].atom, Interval(0, 49))
        self.assertEqual(cex.word, (0,))
        start = (frozenset({0}), frozenset({0}))
        atoms = [atom for atom, _node, _ev in checker.product_cache[start][2]]
        self.assertEqual(atoms, [Interval(0, 49), Interval(50, 100),
                                 Interval(101, 150), Interval(151, 200)])
        self.assertEqual(verify_counterexample(a, b, cex, ), [])

    def test_missing_transition_dead_state(self):
        a = NFA(UNI, 3, 0, finals={2})
        a.add_edge(0, SymbolSet.chars("a"), 1)
        a.add_edge(1, SymbolSet.chars("b"), 2)
        b = NFA(UNI, 2, 0, finals={1})
        b.add_edge(0, SymbolSet.chars("a"), 1)  # no 'b' transition
        result = InclusionChecker(a, b).check()
        self.assertIs(result.status, Status.NOT_INCLUDED)
        cex = result.counterexample
        self.assertEqual(cex.word, (97, 98))
        self.assertEqual(cex.final_b, frozenset())          # dead state
        self.assertEqual(cex.steps[-1].edges_b, ())         # no B-edge evidence
        self.assertEqual(verify_counterexample(a, b, cex), [])

    def test_different_alphabet_boundaries(self):
        a = single_symbol(0, 200, SymbolSet.range(0, 200))
        b = single_symbol(0, 300, SymbolSet.range(0, 300))
        r_ab = InclusionChecker(a, b).check()
        r_ba = InclusionChecker(b, a).check()
        self.assertIs(r_ab.status, Status.INCLUDED)
        self.assertIs(r_ba.status, Status.NOT_INCLUDED)
        self.assertEqual(r_ba.counterexample.word, (201,))
        self.assertEqual(verify_counterexample(b, a, r_ba.counterexample), [])

    def test_epsilon_loops(self):
        a = NFA(UNI, 3, 0, finals={2})
        a.add_epsilon(0, 1)
        a.add_epsilon(1, 0)
        a.add_edge(1, SymbolSet.chars("a"), 2)
        b = NFA(UNI, 2, 0, finals={1})
        b.add_edge(0, SymbolSet.chars("a"), 1)
        self.assertIs(InclusionChecker(a, b).check().status, Status.INCLUDED)
        self.assertIs(InclusionChecker(b, a).check().status, Status.INCLUDED)


class TestBudgetPauseResume(unittest.TestCase):
    def test_budget_exhaustion_is_undecided(self):
        a, b = single_symbol(10, 20), single_symbol(0, 30)
        search = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=0)
        self.assertIs(search.run(), Status.UNDECIDED)

    def test_resume_with_larger_budget(self):
        n = 11
        a, b = make_nth_from_last(n), make_nth_from_last(n)
        search = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=100)
        self.assertIs(search.run(), Status.UNDECIDED)
        self.assertEqual(search.expanded, 100)
        search.budget = 10_000_000
        self.assertIs(search.run(), Status.INCLUDED)
        self.assertEqual(search.expanded, 2 ** (n - 1))

    def test_stepwise_equals_direct(self):
        a, b = make_nth_from_last(8), make_nth_from_last(8)
        direct = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=10_000)
        direct.run()
        stepped = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=10_000)
        while stepped.status is Status.RUNNING:
            stepped.step(7)
        self.assertIs(stepped.status, direct.status)
        self.assertEqual(stepped.expanded, direct.expanded)

    def test_snapshot_restore_json_roundtrip(self):
        uni = SymbolSet.range(0, 9)
        a = NFA(uni, 4, 0, finals={3})
        for i in range(3):
            a.add_edge(i, SymbolSet.range(0, 9), i + 1)
        b = NFA(uni, 4, 0, finals={3})
        b.add_edge(0, SymbolSet.range(0, 5), 1)
        b.add_edge(1, SymbolSet.range(0, 9), 2)
        b.add_edge(2, SymbolSet.range(0, 9), 3)
        direct = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=10_000)
        direct.run()
        stepped = InclusionSearch(LazyDFA(a), LazyDFA(b), budget=10_000)
        stepped.step(2)
        self.assertIs(stepped.status, Status.RUNNING)
        snap = json.loads(json.dumps(stepped.snapshot()))
        restored = InclusionSearch.restore(stepped.dfa_a, stepped.dfa_b, snap)
        restored.run()
        self.assertIs(restored.status, direct.status)
        self.assertEqual(restored.counterexample.word, direct.counterexample.word)
        self.assertEqual(restored.expanded, direct.expanded)
        self.assertEqual(verify_counterexample(a, b, restored.counterexample), [])


class TestCacheInvalidation(unittest.TestCase):
    def _build(self):
        nfa = NFA(UNI, 3, 0, finals={2})
        nfa.add_edge(0, SymbolSet.chars("a"), 1)
        nfa.add_edge(1, SymbolSet.chars("b"), 2)
        return nfa

    def test_edge_edit_locally_invalidates_and_recomputes(self):
        a, b = self._build(), self._build()
        checker = InclusionChecker(a, b)
        r1 = checker.check()
        self.assertIs(r1.status, Status.INCLUDED)
        self.assertEqual(len(checker.product_cache), 4)

        edge = b.edges[1][0]
        b.replace_edge_symbols(edge.id, SymbolSet.chars("c"))
        # Sync performs the local invalidation before any recomputation.
        checker._sync()
        log = checker.invalidation_log[-1]
        self.assertEqual(log["dirty_b"], [1])
        self.assertEqual(log["removed"], 1)
        self.assertEqual(log["survived"], 3)
        self.assertIn((frozenset({0}), frozenset({0})), checker.product_cache)
        self.assertNotIn((frozenset({1}), frozenset({1})), checker.product_cache)
        r2 = checker.check()
        # The old INCLUDED conclusion must not be reused.
        self.assertIs(r2.status, Status.NOT_INCLUDED)
        self.assertEqual(r2.counterexample.word, (97, 98))
        self.assertEqual(verify_counterexample(a, b, r2.counterexample), [])
        # The invalidated entry was recomputed with the new edge, not restored.
        expansions = checker.product_cache[(frozenset({1}), frozenset({1}))][2]
        atoms = [atom for atom, _node, _ev in expansions]
        self.assertIn(Interval(99, 99), atoms)

    def test_check_is_cached_until_mutation(self):
        a, b = self._build(), self._build()
        checker = InclusionChecker(a, b)
        r1 = checker.check()
        expanded = checker.search.expanded
        r2 = checker.check()  # no mutation: cached result, no re-expansion
        self.assertIs(r1, r2)
        self.assertEqual(checker.search.expanded, expanded)


class TestEvidence(unittest.TestCase):
    def test_per_step_original_edge_evidence(self):
        a = NFA(UNI, 3, 0, finals={2})
        e0 = a.add_edge(0, SymbolSet.chars("a"), 1)
        e1 = a.add_edge(1, SymbolSet.chars("b"), 2)
        b = NFA(UNI, 1, 0, finals=set())
        result = InclusionChecker(a, b).check()
        cex = result.counterexample
        self.assertEqual([s.symbol for s in cex.steps], [97, 98])
        self.assertEqual(cex.steps[0].edges_a, (e0,))
        self.assertEqual(cex.steps[1].edges_a, (e1,))
        self.assertEqual(cex.steps[0].atom, Interval(97, 97))
        self.assertEqual(verify_counterexample(a, b, cex), [])

    def test_tampered_counterexample_rejected(self):
        a = NFA(UNI, 3, 0, finals={2})
        a.add_edge(0, SymbolSet.chars("a"), 1)
        a.add_edge(1, SymbolSet.chars("b"), 2)
        b = NFA(UNI, 1, 0, finals=set())
        cex = InclusionChecker(a, b).check().counterexample
        tampered = type(cex)(word=(97, 99), steps=cex.steps,
                             final_a=cex.final_a, final_b=cex.final_b)
        problems = verify_counterexample(a, b, tampered)
        self.assertTrue(problems)


if __name__ == "__main__":
    unittest.main()

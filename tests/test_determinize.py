import unittest

from automata.determinize import LazyDFA
from automata.nfa import NFA
from automata.samples import make_nth_from_last
from automata.symbols import Interval, SymbolSet

UNI = SymbolSet.range(0, 127)


class TestLazyDFA(unittest.TestCase):
    def test_lazy_partition_and_moves(self):
        nfa = NFA(UNI, 3, 0, finals={2})
        nfa.add_edge(0, SymbolSet.char_range("a", "z"), 1)
        nfa.add_edge(1, SymbolSet.char_range("0", "9"), 2)
        dfa = LazyDFA(nfa)
        start = dfa.initial()
        info = dfa.info(start)
        self.assertEqual(info.atoms, (Interval(0, 96), Interval(97, 122), Interval(123, 127)))
        self.assertEqual(info.moves[Interval(97, 122)], frozenset({1}))
        self.assertEqual(info.moves[Interval(0, 96)], frozenset())  # dead state
        self.assertFalse(info.is_final)
        self.assertEqual(dfa.computed_states, 1)
        dfa.info(start)  # cached: no recomputation
        self.assertEqual(dfa.computed_states, 1)

    def test_local_invalidation(self):
        nfa = NFA(UNI, 4, 0, finals={3})
        nfa.add_edge(0, SymbolSet.chars("a"), 1)
        nfa.add_edge(0, SymbolSet.chars("b"), 2)
        nfa.add_edge(1, SymbolSet.chars("c"), 3)
        nfa.add_edge(2, SymbolSet.chars("d"), 3)
        dfa = LazyDFA(nfa)
        dfa.info(frozenset({0}))
        dfa.info(frozenset({1}))
        dfa.info(frozenset({2}))
        self.assertEqual(dfa.cache_size(), 3)
        # Edit an edge of state 1: only entries derived from state 1 die.
        removed = dfa.invalidate({1})
        self.assertEqual(removed, 1)
        self.assertEqual(dfa.cache_size(), 2)
        self.assertIsNone(dfa._cache.get(frozenset({1})))
        self.assertIsNotNone(dfa._cache.get(frozenset({2})))

    def test_exponential_blowup(self):
        n = 9
        nfa = make_nth_from_last(n)
        dfa = LazyDFA(nfa)
        # Explore the whole determinized space via BFS over subset moves.
        seen = {dfa.initial()}
        queue = [dfa.initial()]
        while queue:
            subset = queue.pop()
            for target in dfa.info(subset).moves.values():
                if target not in seen:
                    seen.add(target)
                    queue.append(target)
        self.assertEqual(dfa.computed_states, 2 ** (n - 1))
        self.assertEqual(nfa.num_states, n)


if __name__ == "__main__":
    unittest.main()

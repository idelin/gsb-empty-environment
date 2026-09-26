import unittest

from automata.nfa import NFA, accepts
from automata.symbols import SymbolSet

UNI = SymbolSet.range(0, 127)


def make_ab_nfa():
    nfa = NFA(UNI, 3, 0, finals={2})
    nfa.add_edge(0, SymbolSet.chars("a"), 1)
    nfa.add_edge(1, SymbolSet.chars("b"), 2)
    return nfa


class TestNFA(unittest.TestCase):
    def test_explicit_structure_required(self):
        nfa = NFA(UNI, 2, 0, finals={1})
        self.assertFalse(accepts(nfa, (97,)))
        nfa.add_edge(0, SymbolSet.chars("a"), 1)
        self.assertTrue(accepts(nfa, (97,)))

    def test_state_validation(self):
        nfa = NFA(UNI, 2, 0)
        with self.assertRaises(ValueError):
            nfa.add_edge(0, SymbolSet.chars("a"), 5)
        with self.assertRaises(ValueError):
            NFA(UNI, 2, 7)

    def test_empty_edge_rejected(self):
        nfa = NFA(UNI, 2, 0)
        with self.assertRaises(ValueError):
            nfa.add_edge(0, SymbolSet(), 1)

    def test_epsilon_closure_tolerates_cycles(self):
        nfa = NFA(UNI, 3, 0, finals={2})
        nfa.add_epsilon(0, 1)
        nfa.add_epsilon(1, 0)   # cycle
        nfa.add_epsilon(1, 2)
        self.assertEqual(nfa.eps_closure({0}), frozenset({0, 1, 2}))
        self.assertTrue(accepts(nfa, ()))

    def test_interpreter_basic(self):
        nfa = make_ab_nfa()
        self.assertTrue(accepts(nfa, (97, 98)))
        self.assertFalse(accepts(nfa, (97,)))
        self.assertFalse(accepts(nfa, (98, 97)))
        self.assertFalse(accepts(nfa, ()))

    def test_unknown_symbol_goes_dead(self):
        nfa = NFA(SymbolSet.range(0, 10), 2, 0, finals={1})
        nfa.add_edge(0, SymbolSet.range(0, 10), 1)
        self.assertFalse(accepts(nfa, (55,)))

    def test_mutation_tracks_version_and_dirty_states(self):
        nfa = make_ab_nfa()
        version = nfa.version
        nfa.dirty.clear()
        edge = nfa.edges[1][0]
        nfa.replace_edge_symbols(edge.id, SymbolSet.chars("c"))
        self.assertEqual(nfa.version, version + 1)
        self.assertEqual(nfa.dirty, {1})
        self.assertFalse(accepts(nfa, (97, 98)))
        self.assertTrue(accepts(nfa, (97, 99)))

    def test_remove_edge(self):
        nfa = make_ab_nfa()
        edge = nfa.edges[0][0]
        self.assertTrue(nfa.remove_edge(edge.id))
        self.assertFalse(accepts(nfa, (97, 98)))
        self.assertFalse(nfa.remove_edge(999))


if __name__ == "__main__":
    unittest.main()

"""Nullable chains propagate the end marker to deep levels; revoking a
nullable rule must reclaim the old propagation."""
import unittest

from grammar_compiler import Grammar
from grammar_compiler.symbols import END, Sym


class NullableChainTest(unittest.TestCase):
    def build(self, depth=5):
        # S -> A B1 ; A -> 'a' ; B1 -> B2 -> ... -> B{depth} -> epsilon
        # END reaches follow(A) only through the nullable B-chain.
        g = Grammar()
        g.add_terminals("a")
        m = g.module("m")
        m.add("S", ["A", "B1"])
        m.add("A", ["a"])
        for i in range(1, depth):
            m.add(f"B{i}", [f"B{i + 1}"])
        last = m.add(f"B{depth}", [])
        g.set_start("m", "S")
        return g, last, depth

    def test_end_marker_reaches_deep_chain(self):
        g, _, depth = self.build()
        a = g.analysis
        self.assertEqual(a.follow[Sym("N", "A", "m")], {END})
        for i in range(1, depth + 1):
            sym = Sym("N", f"B{i}", "m")
            self.assertIn(sym, a.nullable)
            self.assertEqual(a.follow[sym], {END})

    def test_revoking_nullable_rule_reclaims_propagation(self):
        g, last, depth = self.build()
        a = g.analysis
        g.remove_production(last)
        for i in range(1, depth + 1):
            self.assertNotIn(Sym("N", f"B{i}", "m"), a.nullable)
        # END no longer crosses the (now non-nullable) B-chain to reach A
        self.assertEqual(a.follow.get(Sym("N", "A", "m"), set()), set())
        # B1 is still at the end of S, so END stays on the chain itself
        self.assertEqual(a.follow[Sym("N", "B1", "m")], {END})
        a.check_invariants()

    def test_nullable_retraction_updates_first(self):
        g = Grammar()
        g.add_terminals("a", "b")
        m = g.module("m")
        m.add("S", ["A", "b"])
        keep = m.add("A", ["a"])
        drop = m.add("A", [])
        g.set_start("m", "S")
        a = g.analysis
        s = Sym("N", "S", "m")
        self.assertEqual(a.first[s], {Sym("T", "a"), Sym("T", "b")})
        g.remove_production(drop)
        self.assertEqual(a.first[s], {Sym("T", "a")})
        g.remove_production(keep)
        self.assertEqual(a.first.get(s, set()), set())
        a.check_invariants()


if __name__ == "__main__":
    unittest.main()

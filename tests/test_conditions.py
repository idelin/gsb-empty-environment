"""Conditional productions: toggling updates sets incrementally and retracts
derived evidence when the condition is switched off."""
import unittest

from grammar_compiler import Grammar
from grammar_compiler.reference import compute_sets
from grammar_compiler.symbols import Sym


def ref(g):
    a = g.analysis
    active = [p for p in a.productions.values()
              if p.condition is None or p.condition in a.enabled_conditions]
    return compute_sets(active, g.start)


class ConditionTest(unittest.TestCase):
    def build(self):
        g = Grammar()
        g.add_terminals("a", "b")
        m = g.module("m")
        m.add("S", ["A"])
        m.add("A", ["a"])
        m.add("A", ["b"], condition="ext")
        g.set_start("m", "S")
        return g

    def test_toggle_updates_incrementally(self):
        g = self.build()
        a = g.analysis
        s = Sym("N", "S", "m")
        self.assertEqual(a.first[s], {Sym("T", "a")})
        g.enable_condition("ext")
        self.assertEqual(a.first[s], {Sym("T", "a"), Sym("T", "b")})
        _, first, _ = ref(g)
        self.assertEqual({k: set(v) for k, v in a.first.items()}, first)
        g.disable_condition("ext")
        self.assertEqual(a.first[s], {Sym("T", "a")})
        nullable, first, follow = ref(g)
        self.assertEqual({k: set(v) for k, v in a.first.items()}, first)
        self.assertEqual(set(a.nullable), nullable)
        self.assertEqual({k: set(v) for k, v in a.follow.items()}, follow)
        a.check_invariants()

    def test_disabled_condition_production_is_inert(self):
        g = self.build()
        a = g.analysis
        fact = ("first", Sym("N", "A", "m"), Sym("T", "b"))
        self.assertEqual(a.proofs_of(fact), frozenset())
        g.enable_condition("ext")
        self.assertEqual(len(a.proofs_of(fact)), 1)
        g.disable_condition("ext")
        self.assertEqual(a.proofs_of(fact), frozenset())


if __name__ == "__main__":
    unittest.main()

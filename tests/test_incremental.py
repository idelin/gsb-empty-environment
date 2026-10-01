"""Random add/remove/condition-toggle fuzzing against the full-fixpoint
reference, plus provenance retraction checks."""
import random
import unittest

from grammar_compiler import Grammar
from grammar_compiler.reference import compute_sets
from grammar_compiler.symbols import Sym


def active_productions(g):
    a = g.analysis
    return [p for p in a.productions.values()
            if p.condition is None or p.condition in a.enabled_conditions]


def assert_matches_reference(testcase, g):
    a = g.analysis
    nullable, first, follow = compute_sets(active_productions(g), g.start)
    testcase.assertEqual(set(a.nullable), nullable)
    testcase.assertEqual({k: set(v) for k, v in a.first.items()}, first)
    testcase.assertEqual({k: set(v) for k, v in a.follow.items()}, follow)
    a.check_invariants()


class FuzzTest(unittest.TestCase):
    def test_random_modifications_match_reference(self):
        rng = random.Random(20261001)
        g = Grammar()
        g.add_terminals("a", "b", "c", "d")
        g.module("m1")
        g.module("m2")
        g.set_start("m1", "S")
        live = []
        conds = ["c1", "c2"]
        names = ["S", "A", "B"]

        def rand_rhs():
            out = []
            for _ in range(rng.randint(0, 3)):
                r = rng.random()
                if r < 0.4:
                    out.append(rng.choice(["a", "b", "c", "d"]))
                elif r < 0.8:
                    out.append(f"{rng.choice(['m1', 'm2'])}.{rng.choice(names)}")
                else:
                    out.append(rng.choice(names))
            return out

        for _ in range(400):
            op = rng.random()
            if op < 0.45 or not live:
                mod = g.module(rng.choice(["m1", "m2"]))
                cond = rng.choice([None, None, "c1", "c2"])
                live.append(mod.add(rng.choice(names), rand_rhs(),
                                    condition=cond))
            elif op < 0.7:
                pid = live.pop(rng.randrange(len(live)))
                g.remove_production(pid)
            else:
                cond = rng.choice(conds)
                if rng.random() < 0.5:
                    g.enable_condition(cond)
                else:
                    g.disable_condition(cond)
            assert_matches_reference(self, g)

    def test_retraction_removes_last_evidence_only(self):
        g = Grammar()
        g.add_terminals("a", "b")
        m = g.module("m")
        p1 = m.add("S", ["A"])
        p2 = m.add("S", ["B"])
        m.add("A", ["a"])
        m.add("B", ["a"])
        g.set_start("m", "S")
        a = g.analysis
        s = Sym("N", "S", "m")
        fact = ("first", s, Sym("T", "a"))
        self.assertEqual(len(a.proofs_of(fact)), 2)
        g.remove_production(p1)
        self.assertEqual(len(a.proofs_of(fact)), 1)
        self.assertIn(Sym("T", "a"), a.first[s])
        g.remove_production(p2)
        self.assertEqual(a.proofs_of(fact), frozenset())
        self.assertNotIn(s, a.first)
        assert_matches_reference(self, g)


if __name__ == "__main__":
    unittest.main()

"""Conflict reports: shortest reachable prefix, competing productions, and
parser-confirmed reachability of the witness."""
import unittest

from grammar_compiler import Grammar, Parser
from grammar_compiler.parser import ConflictError
from grammar_compiler.symbols import Sym


def dangling_else():
    g = Grammar()
    g.add_terminals("if", "then", "else", "print", "x")
    lang = g.module("lang")
    lang.add("S", ["if", "E", "then", "S", "S2"])
    lang.add("S", ["print"])
    lang.add("S2", ["else", "S"])
    lang.add("S2", [])
    lang.add("E", ["x"])
    g.set_start("lang", "S")
    return g


class ConflictTest(unittest.TestCase):
    def test_report_contains_prefix_and_competitors(self):
        snap = dangling_else().compile()
        self.assertEqual(len(snap.conflicts), 1)
        report = snap.conflicts[0]
        self.assertEqual(report.nonterminal, Sym("N", "S2", "lang"))
        self.assertEqual(report.terminal, Sym("T", "else"))
        self.assertEqual([t.name for t in report.prefix],
                         ["if", "x", "then", "print"])
        self.assertEqual([t.name for t in report.witness],
                         ["if", "x", "then", "print", "else"])
        self.assertEqual(len(report.productions), 2)
        self.assertEqual(len(report.reasons), 2)
        self.assertTrue(any("FOLLOW" in r for r in report.reasons))
        text = str(report)
        self.assertIn("S2", text)
        self.assertIn("else", text)

    def test_witness_prefix_is_reachable_by_parser(self):
        snap = dangling_else().compile()
        report = snap.conflicts[0]
        parser = Parser(snap.table_dict(), snap.production_map(), snap.start)
        witness = [t.name for t in report.witness]
        with self.assertRaises(ConflictError) as ctx:
            parser.parse(witness)
        err = ctx.exception
        self.assertEqual([t.name for t in err.consumed],
                         [t.name for t in report.prefix])
        self.assertEqual(err.nonterminal, report.nonterminal)
        self.assertEqual(err.terminal, report.terminal)
        self.assertEqual(set(err.candidates),
                         {p.pid for p in report.productions})

    def test_demo_counterexample_replays(self):
        from examples.conflict_demo import replay
        report, err = replay()
        self.assertEqual([t.name for t in err.consumed],
                         [t.name for t in report.prefix])

    def test_conflict_appears_and_disappears_incrementally(self):
        g = Grammar()
        g.add_terminals("a")
        m = g.module("m")
        m.add("S", ["a"])
        dup = m.add("S", ["a"], condition="dup")
        g.set_start("m", "S")
        self.assertEqual(len(g.compile().conflicts), 0)
        g.enable_condition("dup")
        self.assertEqual(len(g.compile().conflicts), 1)
        g.disable_condition("dup")
        self.assertEqual(len(g.compile().conflicts), 0)
        g.enable_condition("dup")
        g.remove_production(dup)
        self.assertEqual(len(g.compile().conflicts), 0)


if __name__ == "__main__":
    unittest.main()

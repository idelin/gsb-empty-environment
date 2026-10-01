"""Left recursion must not send the analyser or the parser into a loop."""
import unittest

from grammar_compiler import Grammar, Parser
from grammar_compiler.parser import ConflictError, LeftRecursionError
from grammar_compiler.symbols import Sym


class LeftRecursionTest(unittest.TestCase):
    def build(self):
        g = Grammar()
        g.add_terminals("a", "b")
        m = g.module("m")
        m.add("A", ["A", "a"])
        m.add("A", ["b"])
        g.set_start("m", "A")
        return g

    def test_analysis_terminates(self):
        g = self.build()
        a = g.analysis
        self.assertEqual(a.first[Sym("N", "A", "m")], {Sym("T", "b")})
        self.assertNotIn(Sym("N", "A", "m"), a.nullable)

    def test_parser_surfaces_conflict_not_hang(self):
        g = self.build()
        snap = g.compile()
        parser = Parser(snap.table_dict(), snap.production_map(), snap.start)
        with self.assertRaises(ConflictError):
            parser.parse(["b", "a"])

    def test_forced_left_recursive_choice_raises(self):
        g = self.build()
        snap = g.compile()
        parser = Parser(snap.table_dict(), snap.production_map(), snap.start)

        def choose(nt, look, cell):
            prods = snap.production_map()
            for pid in cell:
                prod = prods[pid]
                if prod.rhs and prod.rhs[0] == prod.lhs:
                    return pid
            return cell[0]

        with self.assertRaises(LeftRecursionError):
            parser.parse(["b", "a"], chooser=choose)


if __name__ == "__main__":
    unittest.main()

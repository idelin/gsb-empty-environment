"""Same-named symbols in different modules must never be merged."""
import unittest

from grammar_compiler import Grammar
from grammar_compiler.symbols import Sym


class NamespaceTest(unittest.TestCase):
    def test_same_named_symbols_stay_separate(self):
        g = Grammar()
        g.add_terminals("x", "y")
        front = g.module("front")
        back = g.module("back")
        front.add("Expr", ["x"])
        back.add("Expr", ["y"])
        front.add("S", ["Expr", "back.Expr"])
        g.set_start("front", "S")
        a = g.analysis
        fe = Sym("N", "Expr", "front")
        be = Sym("N", "Expr", "back")
        self.assertEqual(a.first[fe], {Sym("T", "x")})
        self.assertEqual(a.first[be], {Sym("T", "y")})
        self.assertEqual(a.first[Sym("N", "S", "front")], {Sym("T", "x")})
        self.assertEqual(a.follow[fe], {Sym("T", "y")})

    def test_import_alias_resolution(self):
        g = Grammar()
        g.add_terminals("x")
        lib = g.module("lib")
        lib.add("Expr", ["x"])
        app = g.module("app")
        app.import_module("lib", alias="L")
        app.add("Main", ["L.Expr"])
        g.set_start("app", "Main")
        a = g.analysis
        self.assertEqual(a.first[Sym("N", "Main", "app")], {Sym("T", "x")})
        self.assertIn(Sym("N", "Expr", "lib"), a.nullable | set(a.first))

    def test_unknown_module_reference_rejected(self):
        g = Grammar()
        m = g.module("m")
        with self.assertRaises(KeyError):
            m.add("S", ["nope.Expr"])


if __name__ == "__main__":
    unittest.main()

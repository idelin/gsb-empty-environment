"""Old snapshots keep verifying after later mutations; failed transactions
roll back completely."""
import unittest

from grammar_compiler import Grammar
from grammar_compiler.symbols import Sym


class SnapshotTest(unittest.TestCase):
    def build(self):
        g = Grammar()
        g.add_terminals("a", "b")
        m = g.module("m")
        m.add("S", ["A"])
        m.add("A", ["a"])
        g.set_start("m", "S")
        return g

    def test_old_snapshot_still_verifies(self):
        g = self.build()
        snap1 = g.compile()
        snap1.verify()
        m = g.module("m")
        m.add("A", ["b"])
        m.add("S", ["b"])
        snap2 = g.compile()
        self.assertNotEqual(snap1.revision, snap2.revision)
        self.assertNotEqual(snap1.first, snap2.first)
        snap1.verify()
        snap2.verify()

    def test_failed_transaction_rolls_back(self):
        g = self.build()
        before = g.compile()
        m = g.module("m")
        with self.assertRaises(RuntimeError):
            with g.transaction():
                m.add("A", ["b"])
                m.add("B", ["a", "B"])
                g.enable_condition("ext")
                raise RuntimeError("boom")
        after = g.compile()
        self.assertEqual(before, after)
        after.verify()
        self.assertEqual(g.analysis.first[Sym("N", "A", "m")], {Sym("T", "a")})

    def test_successful_transaction_commits(self):
        g = self.build()
        with g.transaction():
            g.module("m").add("A", ["b"])
        self.assertEqual(g.analysis.first[Sym("N", "A", "m")],
                         {Sym("T", "a"), Sym("T", "b")})
        g.compile().verify()


if __name__ == "__main__":
    unittest.main()

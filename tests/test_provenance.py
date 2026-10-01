"""Every set element carries inspectable source proofs."""
import unittest

from grammar_compiler import Grammar
from grammar_compiler.symbols import END, Sym


class ProvenanceTest(unittest.TestCase):
    def test_first_element_proof_names_source_production(self):
        g = Grammar()
        g.add_terminals("a")
        m = g.module("m")
        pid = m.add("S", ["A", "a"])
        m.add("A", [])
        g.set_start("m", "S")
        a = g.analysis
        s = Sym("N", "S", "m")
        fact = ("first", s, Sym("T", "a"))
        proofs = a.proofs_of(fact)
        self.assertTrue(any(p.source[0] == "prod" and p.source[1] == pid
                            for p in proofs))
        text = "\n".join(a.explain(fact))
        self.assertIn(f"'prod', {pid}", text)

    def test_follow_end_marker_seed_proof(self):
        g = Grammar()
        g.add_terminals("a")
        m = g.module("m")
        m.add("S", ["a"])
        g.set_start("m", "S")
        a = g.analysis
        fact = ("follow", Sym("N", "S", "m"), END)
        proofs = a.proofs_of(fact)
        self.assertTrue(any(p.source[0] == "seed" for p in proofs))

    def test_chain_proof_records_antecedents(self):
        g = Grammar()
        g.add_terminals("a")
        m = g.module("m")
        m.add("S", ["A"])
        m.add("A", ["B"])
        m.add("B", ["a"])
        g.set_start("m", "S")
        a = g.analysis
        fact = ("first", Sym("N", "S", "m"), Sym("T", "a"))
        (proof,) = a.proofs_of(fact)
        self.assertEqual(proof.antecedents,
                         (("first", Sym("N", "A", "m"), Sym("T", "a")),))
        # removing the middle rule breaks the chain and withdraws the fact
        g.remove_production(2)
        self.assertEqual(a.proofs_of(fact), frozenset())
        self.assertNotIn(Sym("N", "S", "m"), a.first)


if __name__ == "__main__":
    unittest.main()

import json
import os
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO

from automata.inclusion import InclusionChecker, Status
from automata.nfa import NFA, accepts
from automata.serialize import cex_from_dict, cex_to_dict, nfa_from_dict, nfa_to_dict
from automata.symbols import SymbolSet
from automata.verify import main as verify_main
from automata.verify import verify_counterexample

UNI = SymbolSet.range(0, 127)


def build_pair():
    a = NFA(UNI, 3, 0, finals={2}, name="A")
    a.add_edge(0, SymbolSet.chars("a"), 1)
    a.add_edge(1, SymbolSet.char_range("0", "9"), 2)
    b = NFA(UNI, 2, 0, finals={1}, name="B")
    b.add_edge(0, SymbolSet.chars("a"), 1)
    return a, b


class TestSerialize(unittest.TestCase):
    def test_nfa_roundtrip(self):
        a, _ = build_pair()
        a.add_epsilon(2, 0)
        clone = nfa_from_dict(json.loads(json.dumps(nfa_to_dict(a))))
        self.assertEqual(clone.universe, a.universe)
        self.assertEqual(clone.finals, a.finals)
        for word in [(), (97,), (97, 50), (97, 50, 97, 51)]:
            self.assertEqual(accepts(clone, word), accepts(a, word))
        # New edges after restore must not collide with existing ids.
        e = clone.add_edge(0, SymbolSet.chars("x"), 1)
        self.assertEqual(len({e.id} | {ed.id for lst in clone.edges.values() for ed in lst}),
                         1 + sum(len(lst) for lst in clone.edges.values()) - 1)

    def test_cex_roundtrip(self):
        a, b = build_pair()
        cex = InclusionChecker(a, b).check().counterexample
        clone = cex_from_dict(a, b, json.loads(json.dumps(cex_to_dict(cex))))
        self.assertEqual(clone.word, cex.word)
        self.assertEqual(verify_counterexample(a, b, clone), [])


class TestVerifyCLI(unittest.TestCase):
    def test_cli_accepts_valid_and_rejects_tampered(self):
        a, b = build_pair()
        result = InclusionChecker(a, b).check()
        self.assertIs(result.status, Status.NOT_INCLUDED)
        with tempfile.TemporaryDirectory() as tmp:
            pa, pb, pc = (os.path.join(tmp, n) for n in ("a.json", "b.json", "cex.json"))
            with open(pa, "w") as f:
                json.dump(nfa_to_dict(a), f)
            with open(pb, "w") as f:
                json.dump(nfa_to_dict(b), f)
            with open(pc, "w") as f:
                json.dump(cex_to_dict(result.counterexample), f)
            with redirect_stdout(StringIO()):
                self.assertEqual(verify_main([pa, pb, pc]), 0)
            # Tamper: replace the word with one B accepts.
            with open(pc) as f:
                bad = json.load(f)
            bad["word"] = [97]
            with open(pc, "w") as f:
                json.dump(bad, f)
            with redirect_stdout(StringIO()):
                self.assertEqual(verify_main([pa, pb, pc]), 1)


if __name__ == "__main__":
    unittest.main()

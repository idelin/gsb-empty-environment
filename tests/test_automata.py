import ast
import pathlib
import unittest

from parserlib import demolang
from parserlib.automata import (
    AutomatonError,
    Lexer,
    LexerSpec,
    concat,
    literal,
    plus,
    star,
    symbol,
    union,
    NFA,
)

AB = frozenset({"a", "b"})


class NFATest(unittest.TestCase):
    def test_literal(self):
        nfa = literal("ab", AB)
        self.assertTrue(nfa.accepts_string("ab"))
        self.assertFalse(nfa.accepts_string("a"))
        self.assertFalse(nfa.accepts_string("abc"))
        self.assertFalse(nfa.accepts_string("ba"))

    def test_union_star_concat(self):
        # (a|b)*ab over an explicit alphabet
        nfa = concat(star(union(literal("a", AB), literal("b", AB))), literal("ab", AB))
        self.assertTrue(nfa.accepts_string("ab"))
        self.assertTrue(nfa.accepts_string("aab"))
        self.assertTrue(nfa.accepts_string("bbbab"))
        self.assertFalse(nfa.accepts_string(""))
        self.assertFalse(nfa.accepts_string("ba"))
        self.assertFalse(nfa.accepts_string("aba"))

    def test_symbol_outside_alphabet_rejected(self):
        with self.assertRaises(AutomatonError):
            literal("abc", AB)
        with self.assertRaises(AutomatonError):
            NFA.from_transitions(AB, {("q0", "z"): {"q1"}}, "q0", {"q1"})

    def test_dfa_equivalent_to_nfa(self):
        nfa = concat(star(union(literal("a", AB), literal("b", AB))), literal("ab", AB))
        dfa = nfa.to_dfa()
        for length in range(6):
            for bits in range(2**length):
                text = "".join("ab"[(bits >> i) & 1] for i in range(length))
                self.assertEqual(
                    nfa.accepts_string(text),
                    dfa.accepts_string(text),
                    f"mismatch on {text!r}",
                )

    def test_longest_prefix(self):
        digit = symbol(set("0123456789"), demolang.ALPHABET)
        dfa = plus(digit).to_dfa()
        self.assertEqual(dfa.longest_prefix("123abc"), 3)
        self.assertEqual(dfa.longest_prefix("abc"), 0)


class LexerTest(unittest.TestCase):
    def test_demo_lexer(self):
        tokens = demolang.lex("print abc = 42 ;")
        self.assertEqual(
            [t.kind for t in tokens], ["print", "id", "=", "num", ";"]
        )
        self.assertEqual(tokens[1].text, "abc")
        self.assertEqual(tokens[1].span, (6, 9))

    def test_keyword_beats_identifier_on_tie(self):
        tokens = demolang.lex("print printx")
        self.assertEqual([t.kind for t in tokens], ["print", "id"])
        self.assertEqual(tokens[1].text, "printx")


class NoAutomataLibraryTest(unittest.TestCase):
    def test_no_regex_or_automata_library_imported(self):
        root = pathlib.Path(__file__).resolve().parent.parent / "parserlib"
        banned = {"re", "regex", "automata", "fst", "pyautomata"}
        for path in root.glob("*.py"):
            tree = ast.parse(path.read_text())
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    for alias in node.names:
                        self.assertNotIn(
                            alias.name.split(".")[0], banned, f"{path}: {alias.name}"
                        )
                elif isinstance(node, ast.ImportFrom):
                    if node.level:  # relative import of our own modules
                        continue
                    self.assertNotIn(
                        (node.module or "").split(".")[0], banned, f"{path}"
                    )


if __name__ == "__main__":
    unittest.main()

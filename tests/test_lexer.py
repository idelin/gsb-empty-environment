"""Explicit-DFA lexer tests: alphabet and transitions are plain data."""
import unittest

from grammar_compiler.lexer import DFA, LexError, Lexer, literal_dfa


def number_dfa():
    digits = "0123456789"
    transitions = {}
    for d in digits:
        transitions[("start", d)] = "num"
        transitions[("num", d)] = "num"
    return DFA(set(digits), transitions, "start", {"num"})


class LexerTest(unittest.TestCase):
    def test_tokenize_with_explicit_dfas(self):
        lexer = Lexer()
        lexer.add_token("NUM", number_dfa())
        lexer.add_token("PLUS", literal_dfa("+"))
        self.assertEqual(lexer.tokenize("12+7 + 300"),
                         ["NUM", "PLUS", "NUM", "PLUS", "NUM"])

    def test_longest_match_wins(self):
        lexer = Lexer()
        lexer.add_token("IF", literal_dfa("if"))
        transitions = {}
        letters = "abcdefghijklmnopqrstuvwxyz"
        for ch in letters:
            transitions[("start", ch)] = "id"
            transitions[("id", ch)] = "id"
        lexer.add_token("ID", DFA(set(letters), transitions, "start", {"id"}))
        self.assertEqual(lexer.tokenize("if ifx"), ["IF", "ID"])

    def test_lex_error_on_unknown_char(self):
        lexer = Lexer()
        lexer.add_token("NUM", number_dfa())
        with self.assertRaises(LexError):
            lexer.tokenize("12#3")


if __name__ == "__main__":
    unittest.main()

"""Core semantics: explicit DFAs, longest match, priority, waiting."""
import unittest

from streamlex import DFA, DFAError, Lexer, Rule, Status, char_class_plus, literal

LETTERS = set("abcdefghijklmnopqrstuvwxyz")


def keyword_lexer(**kw):
    rules = [
        Rule("KEY", literal("key"), priority=1, states={"CODE"}),
        Rule("ID", char_class_plus(LETTERS), priority=2, states={"CODE"}),
        Rule("WS", char_class_plus({" "}), priority=3, states={"CODE"}, emit=False),
    ]
    return Lexer(rules, alphabet=LETTERS | {" "}, initial_state="CODE", **kw)


class TestDFAValidation(unittest.TestCase):
    def test_valid_minimal(self):
        dfa = DFA(states={0, 1}, alphabet={"a"}, start=0,
                  transitions={(0, "a"): 1}, accepts={1})
        self.assertTrue(dfa.is_accept(1))
        self.assertEqual(dfa.step(0, "a"), 1)
        self.assertIsNone(dfa.step(1, "a"))

    def test_unknown_start(self):
        with self.assertRaises(DFAError):
            DFA(states={0}, alphabet={"a"}, start=9, transitions={}, accepts=set())

    def test_accept_not_subset(self):
        with self.assertRaises(DFAError):
            DFA(states={0, 1}, alphabet={"a"}, start=0,
                transitions={(0, "a"): 1}, accepts={2})

    def test_transition_unknown_state(self):
        with self.assertRaises(DFAError):
            DFA(states={0, 1}, alphabet={"a"}, start=0,
                transitions={(0, "a"): 7}, accepts={1})

    def test_transition_symbol_outside_alphabet(self):
        with self.assertRaises(DFAError):
            DFA(states={0, 1}, alphabet={"a"}, start=0,
                transitions={(0, "b"): 1}, accepts={1})

    def test_empty_lexeme_forbidden(self):
        with self.assertRaises(DFAError):
            DFA(states={0}, alphabet={"a"}, start=0, transitions={}, accepts={0})

    def test_rule_alphabet_must_be_subset_of_lexer_alphabet(self):
        rule = Rule("A", literal("a"), priority=1, states={"S"})
        with self.assertRaises(ValueError):
            Lexer([rule], alphabet={"b"}, initial_state="S")


class TestPriorityAndLongestMatch(unittest.TestCase):
    def test_keyword_beats_identifier_at_same_length(self):
        lx = keyword_lexer()
        res = lx.feed("key ")
        self.assertEqual([t.type for t in res.tokens], ["KEY"])
        self.assertEqual(lx.finish().tokens, ())

    def test_longer_identifier_beats_keyword(self):
        lx = keyword_lexer()
        res = lx.feed("keyboard ")
        self.assertEqual([t.type for t in res.tokens], ["ID"])
        self.assertEqual(res.tokens[0].lexeme, "keyboard")

    def test_offsets_are_absolute(self):
        lx = keyword_lexer()
        tokens = list(lx.feed("key abc ").tokens)
        tokens.extend(lx.finish().tokens)
        self.assertEqual([(t.type, t.lexeme, t.start, t.end) for t in tokens],
                         [("KEY", "key", 0, 3), ("ID", "abc", 4, 7)])

    def test_longest_match_waits_even_after_accept(self):
        # EQ accepts "==" and "===", so after "==" a token is accepted
        # but NOT decided: the match could still extend.  The lexer must
        # wait for proof that no automaton can consume another char.
        eq_dfa = DFA(
            states={0, 1, 2, 3},
            alphabet={"="},
            start=0,
            transitions={(0, "="): 1, (1, "="): 2, (2, "="): 3},
            accepts={2, 3},
        )
        rules = [
            Rule("EQ", eq_dfa, priority=1, states={"S"}),
            Rule("SEMI", literal(";"), priority=2, states={"S"}),
        ]
        lx = Lexer(rules, alphabet={"=", ";"}, initial_state="S")
        self.assertEqual(lx.feed("==").tokens, ())  # accepted but undecided
        res = lx.feed(";")
        self.assertEqual([(t.type, t.lexeme) for t in res.tokens],
                         [("EQ", "=="), ("SEMI", ";")])

        lx2 = Lexer(rules, alphabet={"=", ";"}, initial_state="S")
        lx2.feed("==")
        res = lx2.feed("=")  # the match does extend: longest wins
        self.assertEqual([(t.type, t.lexeme) for t in res.tokens], [("EQ", "===")])

    def test_single_equals_decided_at_finish(self):
        rules = [
            Rule("EQ", literal("=="), priority=1, states={"S"}),
            Rule("ASSIGN", literal("="), priority=2, states={"S"}),
        ]
        lx = Lexer(rules, alphabet={"="}, initial_state="S")
        self.assertEqual(lx.feed("=").tokens, ())
        res = lx.finish()
        self.assertEqual([t.type for t in res.tokens], ["ASSIGN"])

    def test_input_tail_may_extend_until_finish(self):
        lx = keyword_lexer()
        self.assertEqual(lx.feed("ke").tokens, ())
        self.assertEqual(lx.feed("y").tokens, ())  # "key" could still extend
        res = lx.finish()
        self.assertEqual([t.type for t in res.tokens], ["KEY"])


class TestLexerConstruction(unittest.TestCase):
    def test_empty_alphabet_rejected(self):
        with self.assertRaises(ValueError):
            Lexer([Rule("A", literal("a"), priority=1, states={"S"})],
                  alphabet=set(), initial_state="S")

    def test_duplicate_rule_names_rejected(self):
        rules = [Rule("A", literal("a"), priority=1, states={"S"}),
                 Rule("A", literal("b"), priority=2, states={"S"})]
        with self.assertRaises(ValueError):
            Lexer(rules, alphabet={"a", "b"}, initial_state="S")

    def test_feed_after_finish_rejected(self):
        lx = keyword_lexer()
        lx.finish()
        with self.assertRaises(RuntimeError):
            lx.feed("x")


if __name__ == "__main__":
    unittest.main()

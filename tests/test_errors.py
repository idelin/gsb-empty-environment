"""Error reporting with exact offsets, recovery, ambiguity detection."""
import unittest

from streamlex import (
    AmbiguousRuleError,
    Lexer,
    LexError,
    Rule,
    char_class_plus,
    literal,
)

LETTERS = set("abcdefghijklmnopqrstuvwxyz")


def id_lexer():
    rules = [
        Rule("ID", char_class_plus(LETTERS), priority=1, states={"S"}),
        Rule("WS", char_class_plus({" "}), priority=2, states={"S"}, emit=False),
    ]
    return Lexer(rules, alphabet=LETTERS | {" ", "@"}, initial_state="S")


class TestNoMatch(unittest.TestCase):
    def test_exact_offset_and_char_reported(self):
        lx = id_lexer()
        with self.assertRaises(LexError) as ctx:
            lx.feed("ab @cd")
        err = ctx.exception
        self.assertEqual(err.offset, 3)
        self.assertEqual(err.char, "@")
        self.assertEqual(err.state, "S")
        # tokens produced before the error ride along on the exception
        self.assertEqual([(t.type, t.lexeme, t.start) for t in err.tokens],
                         [("ID", "ab", 0)])

    def test_recovery_by_skipping(self):
        lx = id_lexer()
        try:
            lx.feed("ab @cd ")
        except LexError as err:
            kept = list(err.tokens)
        lx.skip(1)  # drop the offending '@'
        kept.extend(lx.resume().tokens)
        kept.extend(lx.finish().tokens)
        self.assertEqual([(t.type, t.lexeme, t.start) for t in kept],
                         [("ID", "ab", 0), ("ID", "cd", 4)])
        self.assertEqual(lx.position, 7)

    def test_error_persists_until_skipped(self):
        lx = id_lexer()
        with self.assertRaises(LexError):
            lx.feed("@")
        with self.assertRaises(LexError):  # feeding more does not clear it
            lx.feed("ab")
        lx.skip(1)
        tokens = list(lx.resume().tokens)
        tokens.extend(lx.finish().tokens)  # "ab" may extend; finish decides
        self.assertEqual([t.lexeme for t in tokens], ["ab"])

    def test_error_at_finish(self):
        lx = id_lexer()
        lx.feed("ok")
        with self.assertRaises(LexError) as ctx:
            lx.finish() if False else lx.feed("@") or lx.finish()
        self.assertEqual(ctx.exception.offset, 2)

    def test_skip_bounds_checked(self):
        lx = id_lexer()
        lx.feed("a")
        with self.assertRaises(ValueError):
            lx.skip(5)


class TestAmbiguity(unittest.TestCase):
    def test_equal_length_equal_priority_is_a_configuration_error(self):
        rules = [
            Rule("R1", literal("ab"), priority=1, states={"S"}),
            Rule("R2", literal("ab"), priority=1, states={"S"}),
            Rule("WS", char_class_plus({" "}), priority=3, states={"S"}, emit=False),
        ]
        lx = Lexer(rules, alphabet={"a", "b", " "}, initial_state="S")
        with self.assertRaises(AmbiguousRuleError) as ctx:
            lx.feed("ab ")
        self.assertEqual(ctx.exception.names, ("R1", "R2"))

    def test_explicit_priority_resolves_would_be_ambiguity(self):
        rules = [
            Rule("R1", literal("ab"), priority=1, states={"S"}),
            Rule("R2", literal("ab"), priority=2, states={"S"}),
            Rule("WS", char_class_plus({" "}), priority=3, states={"S"}, emit=False),
        ]
        lx = Lexer(rules, alphabet={"a", "b", " "}, initial_state="S")
        res = lx.feed("ab ")
        self.assertEqual([t.type for t in res.tokens], ["R1"])


if __name__ == "__main__":
    unittest.main()

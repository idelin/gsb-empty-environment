"""Property test: arbitrary chunking must equal the slow whole-input reference.

The streaming lexer is fed random texts in random chunkings; the token
stream (type, lexeme, offsets, lexical state) must exactly match
``streamlex.reference.lex_all``, an independent O(n^2) implementation
that re-simulates every rule from scratch at every position.
"""
import random
import unittest

from streamlex import Lexer, Rule, char_class_plus, lex_all, literal

LETTERS = set("abcdefinorxz")
DIGITS = set("012")
ALPHABET = LETTERS | DIGITS | {" ", "+", ";"}

RULES = [
    Rule("KW_IF", literal("if"), priority=0, states={"S"}),
    Rule("KW_IN", literal("in"), priority=0, states={"S"}),
    Rule("KW_FOR", literal("for"), priority=0, states={"S"}),
    Rule("ID", char_class_plus(LETTERS), priority=1, states={"S"}),
    Rule("NUM", char_class_plus(DIGITS), priority=1, states={"S"}),
    Rule("PLUS", literal("+"), priority=1, states={"S"}),
    Rule("SEMI", literal(";"), priority=1, states={"S"}),
    Rule("WS", char_class_plus({" "}), priority=9, states={"S"}, emit=False),
]

PIECES = ["if", "in", "for", "a", "abc", "ifx", "forn", "xz", "nin",
          "0", "12", "121", "+", ";"]


def random_text(rng):
    parts = []
    for _ in range(rng.randrange(1, 30)):
        parts.append(rng.choice(PIECES))
        parts.append(rng.choice(["", " ", "  "]))
    return "".join(parts)


def random_chunks(rng, text):
    chunks, i = [], 0
    while i < len(text):
        n = rng.randrange(1, 6)
        chunks.append(text[i:i + n])
        i += n
    return chunks


def run_streaming(chunks):
    lx = Lexer(RULES, alphabet=ALPHABET, initial_state="S")
    tokens = []
    for chunk in chunks:
        tokens.extend(lx.feed(chunk).tokens)
    tokens.extend(lx.finish().tokens)
    return tokens


class TestArbitraryChunking(unittest.TestCase):
    def test_matches_slow_reference(self):
        rng = random.Random(20260926)
        for case in range(300):
            text = random_text(rng)
            expected = lex_all(RULES, initial_state="S", text=text)
            with self.subTest(case=case, text=text):
                self.assertEqual(run_streaming(random_chunks(rng, text)), expected)
                self.assertEqual(run_streaming([text]), expected)  # one shot
                self.assertEqual(run_streaming(list(text)), expected)  # 1-char

    def test_reference_agrees_on_offsets(self):
        text = "if ifx 12+;"
        tokens = lex_all(RULES, initial_state="S", text=text)
        self.assertEqual(
            [(t.type, t.lexeme, t.start, t.end) for t in tokens],
            [("KW_IF", "if", 0, 2), ("ID", "ifx", 3, 6), ("NUM", "12", 7, 9),
             ("PLUS", "+", 9, 10), ("SEMI", ";", 10, 11)],
        )


if __name__ == "__main__":
    unittest.main()

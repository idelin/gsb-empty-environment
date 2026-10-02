import unittest

from errparse.demo_grammar import lex
from errparse.lexer import LexError
from errparse.tokens import EOF_SYMBOL, SOURCE


class LexerTest(unittest.TestCase):
    def test_tokenize_explicit_dfa(self):
        toks = lex("x = 12 ;")
        self.assertEqual([t.type for t in toks],
                         ["ID", "ASSIGN", "NUM", "SEMI", EOF_SYMBOL])
        self.assertEqual([t.value for t in toks[:4]], ["x", "=", "12", ";"])
        self.assertEqual([t.pos for t in toks[:4]], [0, 2, 4, 7])

    def test_keywords(self):
        toks = lex("if print iffy")
        self.assertEqual([t.type for t in toks[:3]], ["IF", "PRINT", "ID"])

    def test_token_identity(self):
        toks = lex("x = 1 ;")
        self.assertEqual(len({t.uid for t in toks}), len(toks))
        self.assertTrue(all(t.origin == SOURCE for t in toks[:-1]))

    def test_lex_error_position(self):
        with self.assertRaises(LexError) as ctx:
            lex("x = 1 ~ ;")
        self.assertEqual(ctx.exception.pos, 6)


if __name__ == "__main__":
    unittest.main()

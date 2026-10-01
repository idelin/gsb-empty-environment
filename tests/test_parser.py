"""End-to-end parse of an arithmetic token stream via the predictive table."""
import unittest

from grammar_compiler import Grammar, Parser
from grammar_compiler.parser import ParseError


def arithmetic():
    g = Grammar()
    g.add_terminals("NUM", "PLUS", "LP", "RP")
    expr = g.module("expr")
    expr.add("E", ["T", "E2"])
    expr.add("E2", ["PLUS", "T", "E2"])
    expr.add("E2", [])
    expr.add("T", ["NUM"])
    expr.add("T", ["LP", "E", "RP"])
    g.set_start("expr", "E")
    return g


class ParserTest(unittest.TestCase):
    def test_accepts_valid_input(self):
        snap = arithmetic().compile()
        self.assertEqual(snap.conflicts, ())
        parser = Parser(snap.table_dict(), snap.production_map(), snap.start)
        steps = parser.parse(["NUM", "PLUS", "LP", "NUM", "PLUS", "NUM", "RP"])
        self.assertGreater(steps, 0)

    def test_rejects_invalid_input(self):
        snap = arithmetic().compile()
        parser = Parser(snap.table_dict(), snap.production_map(), snap.start)
        with self.assertRaises(ParseError):
            parser.parse(["NUM", "PLUS", "PLUS"])


if __name__ == "__main__":
    unittest.main()

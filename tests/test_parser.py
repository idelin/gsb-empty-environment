import unittest

from parserlib import EPSILON, PredictiveParser
from parserlib import demolang
from parserlib.tree import ERROR_SYMBOL


def parse(kinds):
    parser = PredictiveParser(demolang.program_grammar())
    return parser.parse(demolang.tokens_from_kinds(kinds))


class ValidParseTest(unittest.TestCase):
    def test_valid_program(self):
        result = parse("id = num ; print id ;")
        self.assertTrue(result.ok)
        self.assertEqual(len(result.root.find_all("statement")), 2)

    def test_nested_expression(self):
        result = parse("id = ( id + num ) + id ;")
        self.assertTrue(result.ok)

    def test_epsilon_production_builds_tree(self):
        result = parse("")
        self.assertTrue(result.ok)
        eps = result.root.find_all(EPSILON)
        self.assertEqual(len(eps), 1)
        self.assertEqual(result.root.children[0].symbol, "statement_list")
        self.assertIs(result.root.children[0].children[0], eps[0])

    def test_epsilon_tail_nodes_present(self):
        result = parse("id = num ;")
        self.assertTrue(result.ok)
        tails = result.root.find_all("expr_tail")
        self.assertEqual(len(tails), 1)
        self.assertEqual(tails[0].children[0].symbol, EPSILON)


class RecoveryTest(unittest.TestCase):
    def test_follow_sync_keeps_following_structure(self):
        # Bad token inside statement 2 of 3: statement 3 must survive.
        result = parse("id = num ; print + id ; id = num ;")
        self.assertEqual(len(result.root.find_all("statement")), 3)
        self.assertEqual(len(result.diagnostics), 1)
        diag = result.diagnostics[0]
        self.assertEqual(diag.kind, "unexpected-token")
        self.assertEqual(diag.expected, ("(", "id", "num"))
        self.assertEqual(diag.found, "+")

    def test_error_node_retains_skipped_tokens(self):
        result = parse("id = num ; ) id = num ;")
        self.assertEqual(len(result.root.find_all("statement")), 2)
        errors = [n for n in result.root._walk() if n.symbol == ERROR_SYMBOL]
        self.assertEqual(len(errors), 1)
        self.assertEqual([t.kind for t in errors[0].skipped], [")"])
        self.assertEqual([t.text for t in errors[0].skipped], [")"])

    def test_missing_terminal_locates_expected_set(self):
        result = parse("id = num print id ;")
        self.assertEqual(len(result.diagnostics), 1)
        diag = result.diagnostics[0]
        self.assertEqual(diag.kind, "missing-terminal")
        self.assertEqual(diag.expected, (";",))
        self.assertEqual(diag.position, 3)
        self.assertEqual(len(result.root.find_all("statement")), 2)

    def test_unexpected_token_locates_expected_set(self):
        result = parse("id = ;")
        self.assertEqual(len(result.diagnostics), 1)
        diag = result.diagnostics[0]
        self.assertEqual(diag.kind, "unexpected-token")
        self.assertEqual(diag.expected, ("(", "id", "num"))

    def test_no_duplicate_positions(self):
        # Crafted so naive recovery reports the same position twice.
        result = parse("print num )")
        positions = [d.position for d in result.diagnostics]
        self.assertEqual(len(positions), len(set(positions)))

    def test_multiple_errors_all_reported_once(self):
        result = parse("id num ; print + id ; )")
        positions = [d.position for d in result.diagnostics]
        self.assertEqual(len(positions), len(set(positions)))
        self.assertGreaterEqual(len(result.diagnostics), 2)

    def test_recovery_terminates_on_adversarial_inputs(self):
        for kinds in [
            "( ( ( ( ( (",
            ") ) ) )",
            "id id id id",
            "= = ; ;",
            "print print print",
            "( ) ; ( ; )",
            "",
        ]:
            result = parse(kinds)  # must simply return
            positions = [d.position for d in result.diagnostics]
            self.assertEqual(
                len(positions), len(set(positions)), f"duplicate report: {kinds}"
            )

    def test_strict_mode_flags_invalid(self):
        parser = PredictiveParser(demolang.program_grammar(), recover=False)
        self.assertTrue(parser.parse(demolang.tokens_from_kinds("id = num ;")).ok)
        self.assertFalse(parser.parse(demolang.tokens_from_kinds("id = num")).ok)


class SourceMapTest(unittest.TestCase):
    def test_source_map_spans(self):
        result = parse("id = num ;")
        mapping = result.root.source_map()
        kinds = [entry[0] for entry in mapping]
        self.assertEqual(kinds, ["id", "=", "num", ";"])
        spans = [entry[1] for entry in mapping]
        self.assertEqual(spans, [(0, 1), (1, 2), (2, 3), (3, 4)])
        self.assertTrue(all(entry[2] == "original" for entry in mapping))


if __name__ == "__main__":
    unittest.main()

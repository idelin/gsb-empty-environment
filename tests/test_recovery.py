import unittest

from minilang import Document, run_source
from minilang.lexer import lex
from minilang.parser import (ParseFailure, describe_edits, parse_decl_at,
                             parse_program, recover_decl)
from minilang.tokens import (EOF, EQ, IDENT, LET, LPAREN, NUMBER, RPAREN,
                             SEMI, virtual_token)


def parser_diags(result):
    return [d for d in result.diagnostics if d.source == "parser"]


# ---------------------------------------------------------------------------
# Independent exhaustive check of minimal edit cost: unrestricted edit space
# (insert/delete/replace any token at any position), BFS by cost.
# ---------------------------------------------------------------------------

BRUTE_KINDS = (LET, IDENT, NUMBER, EQ, SEMI, RPAREN, LPAREN)


def brute_force_min_cost(tokens, start, max_cost=2):
    def parses(stream):
        try:
            parse_decl_at(stream, start)
        except ParseFailure:
            return False
        return True

    frontier = [list(tokens)]
    for cost in range(max_cost + 1):
        children = []
        for stream in frontier:
            if parses(stream):
                return cost
            if cost == max_cost:
                continue
            for i in range(start, len(stream)):
                tok = stream[i]
                for kind in BRUTE_KINDS:
                    children.append(stream[:i] + [virtual_token(kind, tok.start)]
                                    + stream[i:])
                if tok.kind != EOF:
                    children.append(stream[:i] + stream[i + 1:])
                    for kind in BRUTE_KINDS:
                        children.append(stream[:i]
                                        + [virtual_token(kind, tok.start, tok.end)]
                                        + stream[i + 1:])
        seen = set()
        frontier = []
        for stream in children:
            key = tuple(t.kind for t in stream)
            if key not in seen:
                seen.add(key)
                frontier.append(stream)
    return None


class MinimalCostTests(unittest.TestCase):
    def test_minimal_cost_matches_exhaustive_search(self):
        cases = [
            "let a = 1",          # missing semicolon before EOF
            "let a = ;",          # missing expression
            "let a 1;",           # missing '='
            "let = 1;",           # missing name
            "let a = (1 + 2;",    # missing ')'
            "let a = (1 + ;",     # missing operand and ')'
            "let a = ((1;",       # missing two ')'
            "a = 1;",             # missing 'let'
        ]
        for source in cases:
            with self.subTest(source=source):
                tokens, _ = lex(source)
                solutions, cost, _ = recover_decl(tokens, 0, max_cost=3)
                self.assertIsNotNone(cost)
                self.assertTrue(solutions)
                self.assertEqual(cost, brute_force_min_cost(tokens, 0))

    def test_all_same_cost_candidates_returned(self):
        tokens, _ = lex("let a = 1\nlet b = 2;")
        solutions, cost, truncated = recover_decl(tokens, 0)
        self.assertEqual(cost, 1)
        self.assertFalse(truncated)
        descriptions = [describe_edits(s[0]) for s in solutions]
        self.assertTrue(any("insert SEMI" in d for d in descriptions))
        self.assertTrue(any("replace" in d and "SEMI" in d
                            for d in descriptions))

    def test_truncation_is_explicit(self):
        tokens, _ = lex("let a = 1\nlet b = 2;")
        solutions, cost, truncated = recover_decl(tokens, 0, max_candidates=1)
        self.assertEqual(cost, 1)
        self.assertTrue(truncated)
        self.assertEqual(len(solutions), 1)

    def test_truncation_reported_in_diagnostic(self):
        tokens, lex_diags = lex("let a = 1\nlet b = 2;")
        _, diags = parse_program(tokens, lex_diags, max_candidates=1)
        self.assertEqual(len(diags), 1)
        self.assertTrue(diags[0].truncated)
        self.assertIn("truncated", diags[0].message)
        self.assertNotIn("all ", diags[0].message)


class RecoveryTests(unittest.TestCase):
    def test_two_missing_semicolons_each_reported_once(self):
        result = run_source("let a = 1\nlet b = 2\nlet c = 3;\n")
        diags = parser_diags(result)
        self.assertEqual(len(diags), 2)
        for d in diags:
            self.assertEqual(d.cost, 1)
            self.assertIn("minimal edit cost 1", d.message)
        self.assertEqual(result.env, {"a": 1, "b": 2, "c": 3})

    def test_error_span_does_not_cross_into_next_decl(self):
        source = "let a = 1\nlet b = 2;\n"
        result = run_source(source)
        (diag,) = parser_diags(result)
        self.assertLessEqual(diag.end, source.index("let b"))

    def test_unterminated_string_reports_once_not_per_char(self):
        source = 'let s = "abc\nlet t = 2;\nlet u = "xyz\n'
        result = run_source(source)
        lex_diags = [d for d in result.diagnostics if d.source == "lexer"]
        self.assertEqual(len(lex_diags), 2)
        self.assertEqual(len(parser_diags(result)), 2)
        self.assertEqual(len(result.diagnostics), 4)
        for d in result.diagnostics:
            self.assertLessEqual(d.end - d.start, 20)
        self.assertEqual(result.env["s"], "abc")
        self.assertEqual(result.env["t"], 2)
        self.assertEqual(result.env["u"], "xyz")

    def test_nested_missing_paren_keeps_following_decl(self):
        source = "let x = ((1 + 2) * 3;\nlet y = 5;\n"
        result = run_source(source)
        diags = parser_diags(result)
        self.assertEqual(len(diags), 1)
        self.assertEqual(diags[0].cost, 1)
        self.assertLessEqual(diags[0].end, source.index("let y"))
        self.assertEqual(result.env["x"], 9)
        self.assertEqual(result.env["y"], 5)

    def test_double_missing_paren_costs_two(self):
        tokens, _ = lex("let x = ((1 + 2;")
        solutions, cost, _ = recover_decl(tokens, 0)
        self.assertEqual(cost, 2)
        self.assertTrue(any([e.kind for e in edits] == ["RPAREN", "RPAREN"]
                            for edits, *_ in solutions))
        result = run_source("let x = ((1 + 2;")
        self.assertEqual(result.env["x"], 3)

    def test_virtual_tokens_and_token_identity(self):
        source = "let a = 1\nlet b = 2;"
        tokens, lex_diags = lex(source)
        program, _ = parse_program(tokens, lex_diags)
        decl_a, decl_b = program.decls
        self.assertTrue(decl_a.semi_token.virtual)
        self.assertFalse(decl_a.let_token.virtual)
        self.assertIs(decl_a.let_token, tokens[0])
        self.assertIs(decl_a.name_token, tokens[1])
        self.assertTrue(decl_a.edits)
        self.assertFalse(decl_b.semi_token.virtual)
        self.assertIs(decl_b.let_token, tokens[4])

    def test_recovered_decls_still_execute(self):
        result = run_source("let a = 2\nlet b = a * 3 + 1;")
        self.assertEqual(len(parser_diags(result)), 1)
        self.assertEqual(result.env, {"a": 2, "b": 7})

    def test_unrecoverable_within_budget_is_explicit(self):
        source = "let a ((( ;\nlet b = 2;\n"  # minimal recovery cost is 3
        result = run_source(source, max_cost=2)
        diags = parser_diags(result)
        self.assertEqual(len(diags), 1)
        self.assertIn("budget", diags[0].message)
        self.assertNotIn("minimal", diags[0].message)
        self.assertNotIn("a", result.env)
        self.assertEqual(result.env["b"], 2)


class LanguageTests(unittest.TestCase):
    def test_utf8_byte_spans(self):
        source = "let 变量 = 1;\nlet 乙 = 变量;\n"
        tokens, _ = lex(source)
        name = tokens[1]
        self.assertEqual(name.text, "变量")
        self.assertEqual(name.start, 4)
        self.assertEqual(name.end, 10)
        raw = source.encode("utf-8")
        self.assertEqual(raw[name.start:name.end].decode("utf-8"), "变量")

    def test_utf8_diagnostic_span_after_multibyte(self):
        source = "let 甲 = 1\nlet 乙 = 2;\n"
        result = run_source(source)
        (diag,) = parser_diags(result)
        self.assertEqual(diag.start, len("let 甲 = 1".encode("utf-8")))
        self.assertEqual(diag.end, len("let 甲 = 1\n".encode("utf-8")))

    def test_interpreter_arithmetic_and_strings(self):
        result = run_source(
            'let s = "hi" + "!";\nlet n = 2 * 3 + 1;\nlet p = (n - 1) / 2;\n')
        self.assertEqual(result.diagnostics, [])
        self.assertEqual(result.env, {"s": "hi!", "n": 7, "p": 3.0})

    def test_runtime_errors_are_diagnostics(self):
        result = run_source("let a = missing;\nlet b = 1 / 0;\n")
        runtime = [d for d in result.diagnostics if d.source == "runtime"]
        self.assertEqual(len(runtime), 2)

    def test_document_edit_updates_diagnostics(self):
        doc = Document("let a = 1\nlet b = 2;\n")
        self.assertEqual(len([d for d in doc.diagnostics
                              if d.source == "parser"]), 1)
        at = len("let a = 1".encode("utf-8"))
        doc.edit(at, at, ";")
        self.assertEqual(doc.diagnostics, [])
        self.assertEqual(doc.env, {"a": 1, "b": 2})

    def test_document_edit_can_introduce_error(self):
        doc = Document("let a = 1;\n")
        self.assertEqual(doc.diagnostics, [])
        doc.edit(len("let a = 1".encode("utf-8")),
                 len("let a = 1;".encode("utf-8")), "")
        self.assertEqual(len([d for d in doc.diagnostics
                              if d.source == "parser"]), 1)


if __name__ == "__main__":
    unittest.main()

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from declang import (
    ALL_KINDS,
    Document,
    find_minimal_repairs,
    lex,
)
from declang.parser import ParseError, parse_decl


# ---------------------------------------------------------------------------
# Independent exhaustive edit-sequence enumerator (kind-level).
# Enumerates *every* sequence of <= max_cost edits, applies each step to the
# current stream, and records distinct results that fully parse as one decl.
# ---------------------------------------------------------------------------

def _parses(kinds):
    try:
        _, consumed = parse_decl(
            [type("T", (), {"kind": k})() for k in kinds], 0
        )
    except ParseError:
        return False
    return consumed == len(kinds)


def brute_force_repairs(kinds, max_cost=2):
    """Return (min_cost, set_of_parsing_kind_tuples_at_that_cost)."""
    start = tuple(kinds)
    if _parses(start):
        return 0, {start}

    # Per-state op generation, so validity follows the evolving stream.
    def gen_ops(seq):
        length = len(seq)
        for pos in range(length + 1):
            if pos < length:
                yield ("del", pos)
                for kind in ALL_KINDS:
                    if kind != seq[pos]:
                        yield ("rep", pos, kind)
            for kind in ALL_KINDS:
                yield ("ins", pos, kind)

    def apply_op(seq, op):
        tag = op[0]
        if tag == "del":
            pos = op[1]
            return seq[:pos] + seq[pos + 1:]
        if tag == "rep":
            _, pos, kind = op
            return seq[:pos] + (kind,) + seq[pos + 1:]
        _, pos, kind = op
        return seq[:pos] + (kind,) + seq[pos:]

    frontier = {start}
    for cost in range(1, max_cost + 1):
        found = set()
        next_frontier = set()
        for seq in frontier:
            for op in gen_ops(seq):
                result = apply_op(seq, op)
                if _parses(result):
                    found.add(result)
                else:
                    next_frontier.add(result)
        if found:
            return cost, found
        frontier = next_frontier
    return None, set()



def repair_result(source, **kw):
    tokens, _ = lex(source)
    return tokens, find_minimal_repairs(tokens, **kw)


# ---------------------------------------------------------------------------

class RecoveryTests(unittest.TestCase):

    def test_two_missing_semicolons_two_errors(self):
        src = "let a = 1 let b = 2 let c = 3;"
        doc = Document(src)
        syntax = [d for d in doc.diagnostics if d.severity == "error"]
        self.assertEqual(len(syntax), 2, doc.diagnostics)
        self.assertTrue(all(d.minimal for d in syntax))
        # each repair is a single zero-cost-gap insert of ';'
        for diag in syntax:
            self.assertEqual(len(diag.candidates), 1)
            self.assertIn("insert ';'", diag.candidates[0])
        self.assertEqual(len(doc.decls), 3)
        self.assertEqual(doc.env, {"a": 1, "b": 2, "c": 3})

    def test_unterminated_string_is_one_error_not_per_character(self):
        src = 'let s = "oops; let t = 2;\nlet u = 3;\n'
        doc = Document(src)
        messages = [d.message for d in doc.diagnostics]
        self.assertEqual(sum("unterminated string" in m for m in messages), 1)
        # no diagnostic spans the whole file character by character
        data = src.encode("utf-8")
        for diag in doc.diagnostics:
            self.assertLess(diag.end - diag.start, len(data) - 1)
        # recovered following declaration still parses and executes
        self.assertEqual(doc.env.get("u"), 3)
        self.assertEqual(len(doc.decls), 2)

    def test_nested_missing_paren_does_not_swallow_next_decl(self):
        src = "let a = (1 + (2 + 3); let b = 4;"
        doc = Document(src)
        syntax = [d for d in doc.diagnostics if d.severity == "error"]
        self.assertEqual(len(syntax), 1, doc.diagnostics)
        diag = syntax[0]
        self.assertTrue(diag.minimal)
        # 5 equal-cost repairs: insert ')' at 3 valid points or delete '('
        self.assertEqual(len(diag.candidates), 5)
        # every candidate evaluates to 6
        self.assertEqual(doc.env["a"], 6)
        self.assertEqual(doc.env["b"], 4)
        self.assertEqual(len(doc.decls), 2)
        # range never crosses into the following valid declaration
        next_let = src.encode("utf-8").index(b"let b")
        self.assertLessEqual(diag.end, next_let)

    def test_minimal_cost_verified_by_exhaustive_enumeration(self):
        cases = [
            "let a 1;",            # insert '='
            "let a = 1 2;",        # delete either int
            "let = 1;",            # insert name
            "let a = (1;",         # insert ')' or delete '('
            "let a = ;",           # insert int / string / name
            "let a = 1",           # insert ';' at EOF
            "let a = 1 2 3;",      # cost 2: delete two ints
        ]
        for src in cases:
            with self.subTest(src=src):
                tokens, _ = lex(src)
                result = find_minimal_repairs(
                    tokens, max_cost=2, max_candidates=10_000, max_states=10**9
                )
                self.assertTrue(result.found, f"no repair for {src}")
                min_cost, expected = brute_force_repairs(
                    tuple(t.kind for t in tokens), max_cost=2
                )
                self.assertIsNotNone(min_cost, f"brute force failed for {src}")
                self.assertEqual(result.cost, min_cost)
                got = {tuple(t.kind for t in c.tokens) for c in result.candidates}
                self.assertFalse(result.truncated)
                self.assertEqual(got, expected)

    def test_token_identity_and_virtual_markers_preserved(self):
        src = "let a = 1 let b = 2;"
        doc = Document(src)
        tokens = doc.tokens
        first = doc.decls[0]
        # original tokens are the very same objects as the lexer produced
        self.assertIs(first.let_tok, tokens[0])
        self.assertIs(first.name_tok, tokens[1])
        self.assertIs(first.eq_tok, tokens[2])
        self.assertIs(first.expr.tok, tokens[3])
        self.assertTrue(first.semi_tok.virtual)
        self.assertEqual(first.semi_tok.kind, ";")
        # second declaration is untouched original tokens
        second = doc.decls[1]
        self.assertIs(second.let_tok, tokens[4])
        self.assertFalse(second.semi_tok.virtual)

    def test_replace_recovery_keeps_original_identity(self):
        src = "let ( = 1;"
        doc = Document(src)
        self.assertEqual(len(doc.decls), 1)
        name_tok = doc.decls[0].name_tok
        self.assertTrue(name_tok.virtual)
        self.assertEqual(name_tok.kind, "name")
        self.assertIsNotNone(name_tok.replaces)
        self.assertEqual(name_tok.replaces.kind, "(")
        self.assertEqual(name_tok.start, name_tok.replaces.start)
        self.assertEqual(name_tok.end, name_tok.replaces.end)

    def test_local_edit_updates_diagnostics(self):
        src = "let a = 1 let b = 2;"
        doc = Document(src)
        self.assertEqual(
            len([d for d in doc.diagnostics if d.severity == "error"]), 1
        )
        pos = src.encode("utf-8").index(b"let b")
        doc.edit(pos, pos, "; ")
        self.assertEqual(doc.diagnostics, [])
        self.assertEqual(doc.env, {"a": 1, "b": 2})

    def test_utf8_byte_ranges(self):
        src = "let 变量 = 1 let b = 2;"
        doc = Document(src)
        data = src.encode("utf-8")
        syntax = [d for d in doc.diagnostics if d.severity == "error"]
        self.assertEqual(len(syntax), 1)
        diag = syntax[0]
        # zero-width insertion point immediately after the '1'
        one = data.index(b"1")
        expected = one + 1
        self.assertEqual(diag.start, expected)
        self.assertEqual(diag.end, expected)
        self.assertEqual(data[diag.start:diag.end], b"")
        self.assertEqual(data[one:expected], b"1")

    def test_candidate_truncation_is_explicit(self):
        src = "let a = ;"
        tokens, _ = lex(src)
        result = find_minimal_repairs(
            tokens, max_cost=1, max_candidates=2
        )
        self.assertTrue(result.found)
        self.assertEqual(result.cost, 1)
        self.assertEqual(len(result.candidates), 2)
        self.assertTrue(result.truncated)
        # and with room for all, there are exactly 3 (int, string, name)
        full = find_minimal_repairs(tokens, max_cost=1, max_candidates=100)
        self.assertEqual(len(full.candidates), 3)
        self.assertFalse(full.truncated)

    def test_budget_exhaustion_uses_greedy_sync_without_claiming_minimal(self):
        # needs 4 deletions, beyond the cost-2 budget
        src = "let a = 1 2 3 4 5 6 7 8 9; let b = 2;"
        doc = Document(src, max_cost=2)
        syntax = [d for d in doc.diagnostics if d.severity == "error"]
        self.assertEqual(len(syntax), 1)
        diag = syntax[0]
        self.assertFalse(diag.minimal)
        self.assertIn("not a minimal repair", diag.message)
        # following valid declaration still synchronized and executed
        self.assertEqual(doc.env, {"b": 2})
        self.assertEqual(len(doc.decls), 1)

    def test_stray_semicolon_skipped(self):
        src = "let a = 1;; let b = 2;"
        doc = Document(src, max_cost=1)
        syntax = [d for d in doc.diagnostics if d.severity == "error"]
        self.assertEqual(len(syntax), 1)
        self.assertFalse(syntax[0].minimal)
        self.assertEqual(doc.env, {"a": 1, "b": 2})

    def test_interpreter_runs_recovered_and_valid_decls(self):
        src = "let a = 1 let b = a + 2;"
        doc = Document(src)
        self.assertEqual(doc.env, {"a": 1, "b": 3})
        valid = Document('let x = 1 + 2; let y = x + 4; let s = "a" + "b";'
                         ' let z = q + 1; let w = 9;')
        self.assertEqual(valid.env["x"], 3)
        self.assertEqual(valid.env["y"], 7)
        self.assertEqual(valid.env["s"], "ab")
        self.assertEqual(valid.env["w"], 9)
        self.assertNotIn("z", valid.env)
        runtime = [d for d in valid.diagnostics if d.severity == "runtime"]
        self.assertEqual(len(runtime), 1)
        self.assertIn("undefined name", runtime[0].message)

    def test_invalid_characters_grouped_in_one_diagnostic(self):
        src = "let a = 1; %$$% let b = 2;"
        doc = Document(src)
        bad = [d for d in doc.diagnostics
               if "unexpected character" in d.message]
        self.assertEqual(len(bad), 1)
        self.assertEqual(bad[0].end - bad[0].start, len("%$$%".encode()))
        self.assertEqual(doc.env, {"a": 1, "b": 2})


if __name__ == "__main__":
    unittest.main()

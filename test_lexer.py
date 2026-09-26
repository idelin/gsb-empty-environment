"""unittest suite for the streaming lexer.

Run with:  python3.11 -m unittest -v
"""

from __future__ import annotations

import random
import string
import unittest

from lexer import (Action, DFA, Lexer, Rule, StateOp, Status,
                   charclass_dfa, literal_dfa)
from reference import reference_lex
from sample_lang import ALPHABET, build_states


def stream(states, text, chunk_sizes, start_state="main", max_pending=1 << 20):
    """Feed ``text`` through a Lexer in chunks and return (tokens, results)."""
    lex = Lexer(states, start_state, max_pending=max_pending)
    results = []
    pos = 0
    for size in chunk_sizes:
        results.append(lex.feed(text[pos:pos + size]))
        pos += size
    results.append(lex.feed(text[pos:]))
    results.append(lex.finish())
    return lex.drain(), results


def random_chunk_sizes(total, rng):
    sizes = []
    left = total
    while left > 0:
        size = rng.randint(1, 9)
        sizes.append(size)
        left -= size
    return sizes


class KeywordVsIdentifierTest(unittest.TestCase):
    def test_keyword_wins_on_equal_length(self):
        tokens, _ = stream(build_states(), "if ", [3])
        self.assertEqual([(t.type, t.lexeme) for t in tokens], [("KW", "if")])

    def test_longer_identifier_beats_keyword(self):
        tokens, _ = stream(build_states(), "ifx ", [4])
        self.assertEqual([(t.type, t.lexeme) for t in tokens], [("IDENT", "ifx")])

    def test_keyword_prefix_inside_identifier(self):
        tokens, _ = stream(build_states(), "while1 ", [7])
        self.assertEqual([(t.type, t.lexeme) for t in tokens], [("IDENT", "while1")])


class LongestMatchAcrossChunksTest(unittest.TestCase):
    def test_shift_assign_never_cut_early(self):
        text = "a<<=b"
        for split in range(len(text) + 1):
            tokens, _ = stream(build_states(), text, [split])
            kinds = [(t.type, t.lexeme) for t in tokens]
            self.assertEqual(
                kinds,
                [("IDENT", "a"), ("OP", "<<="), ("IDENT", "b")],
                f"mis-cut at split {split}")

    def test_no_token_while_match_can_extend(self):
        lex = Lexer(build_states(), "main")
        lex.feed("<")
        self.assertEqual(lex.drain(), [])      # "<" could still become "<<"
        lex.feed("<")
        self.assertEqual(lex.drain(), [])      # "<<" could still become "<<="
        result = lex.feed("=")
        self.assertEqual(result.status, Status.OK)
        # Every DFA is dead past "<<=", so the match is determined and the
        # token is committed without waiting for further input.
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()], [("OP", "<<=")])


class TailWaitsForFinishTest(unittest.TestCase):
    def test_tail_extends_with_next_chunk(self):
        lex = Lexer(build_states(), "main")
        lex.feed("if")
        self.assertEqual(lex.drain(), [])
        lex.feed("x")
        self.assertEqual(lex.drain(), [])
        result = lex.finish()
        self.assertEqual(result.status, Status.FINISHED)
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()], [("IDENT", "ifx")])

    def test_tail_resolved_by_finish(self):
        lex = Lexer(build_states(), "main")
        lex.feed("if")
        self.assertEqual(lex.drain(), [])
        lex.finish()
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()], [("KW", "if")])


class LexicalStateTest(unittest.TestCase):
    def test_nested_comments_use_state_stack(self):
        text = "a /* one /* two */ still comment */ b"
        tokens, _ = stream(build_states(), text, [5, 3, 100])
        self.assertEqual([(t.type, t.lexeme) for t in tokens],
                         [("IDENT", "a"), ("IDENT", "b")])

    def test_comment_close_outside_comment_is_an_error(self):
        lex = Lexer(build_states(), "main")
        result = lex.feed("*/")
        self.assertEqual(result.status, Status.ERROR)
        self.assertEqual(result.error.offset, 0)

    def test_state_stack_restored_by_rollback(self):
        lex = Lexer(build_states(), "main")
        lex.feed("x /* ")
        cp = lex.checkpoint()
        self.assertEqual(lex.state_stack, ["main", "comment"])
        lex.feed("nonsense ")
        lex.rollback(cp)
        self.assertEqual(lex.state_stack, ["main", "comment"])
        result = lex.feed("*/ y ")
        self.assertEqual(result.status, Status.OK)
        lex.finish()
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("IDENT", "x"), ("IDENT", "y")])


class SameLexemePriorityTest(unittest.TestCase):
    def _states(self):
        arrow = literal_dfa("->", ALPHABET)
        main = [
            Rule("arrow_switch", arrow, 1, Action("ARROW", StateOp.SWITCH, "other")),
            Rule("arrow_keep", arrow, 2, Action("RARROW")),
        ]
        other = [
            Rule("ident", charclass_dfa(string.ascii_lowercase, ALPHABET), 5,
                 Action("LOWER")),
            Rule("ws", charclass_dfa(" ", ALPHABET), 20, Action(None)),
        ]
        return {"main": main, "other": other}

    def test_unique_action_chosen_by_priority(self):
        lex = Lexer(self._states(), "main")
        lex.feed("-> ab ")
        lex.finish()
        tokens = lex.drain()
        self.assertEqual([(t.type, t.lexeme) for t in tokens],
                         [("ARROW", "->"), ("LOWER", "ab")])
        self.assertEqual(lex.state_stack, ["other"])


class BoundedBufferingTest(unittest.TestCase):
    def _states(self):
        return {"main": [
            Rule("long", literal_dfa("a" * 20 + "b", ALPHABET), 1, Action("LONG")),
        ]}

    def test_incomplete_instead_of_mis_cut(self):
        lex = Lexer(self._states(), "main", max_pending=8)
        saw_incomplete = False
        for _ in range(4):
            result = lex.feed("aaaaa")
            self.assertEqual(lex.drain(), [])
            if result.status is Status.INCOMPLETE:
                saw_incomplete = True
                self.assertGreater(result.buffered, 8)
                self.assertGreater(result.work, 0)
        self.assertTrue(saw_incomplete)
        result = lex.feed("b")
        self.assertEqual(result.status, Status.OK)
        tokens = lex.drain()
        self.assertEqual([(t.type, t.lexeme) for t in tokens],
                         [("LONG", "a" * 20 + "b")])

    def test_work_bound_per_feed(self):
        lex = Lexer(self._states(), "main", max_pending=1 << 20)
        for _ in range(2):
            result = lex.feed("a" * 7)
            self.assertEqual(result.status, Status.OK)
            # One rule: at most one transition lookup per buffered char.
            self.assertLessEqual(result.work, result.buffered)


class ErrorOffsetAndRecoveryTest(unittest.TestCase):
    def test_precise_offset_and_recovery(self):
        lex = Lexer(build_states(), "main")
        result = lex.feed("ab$cd")
        self.assertEqual(result.status, Status.ERROR)
        self.assertEqual(result.error.offset, 2)
        self.assertEqual(result.error.char, "$")
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("IDENT", "ab")])
        lex.recover()
        result = lex.finish()
        self.assertEqual(result.status, Status.FINISHED)
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("IDENT", "cd")])

    def test_offset_is_absolute_across_chunks(self):
        lex = Lexer(build_states(), "main")
        lex.feed("abc ")
        lex.drain()
        result = lex.feed("de$f")
        self.assertEqual(result.status, Status.ERROR)
        self.assertEqual(result.error.offset, 6)


class CheckpointTest(unittest.TestCase):
    def test_rollback_reemits_tokens(self):
        lex = Lexer(build_states(), "main")
        lex.feed("if x")
        cp = lex.checkpoint()
        lex.feed("<<=1 ")
        lex.rollback(cp)
        first = lex.drain()
        self.assertEqual([(t.type, t.lexeme) for t in first],
                         [("KW", "if"), ("IDENT", "x"), ("OP", "<<="), ("INT", "1")])
        lex.rollback(cp)
        second = lex.drain()
        self.assertEqual(first, second)

    def test_rollback_then_undo_diverges_input(self):
        lex = Lexer(build_states(), "main")
        lex.feed("if x")
        cp = lex.checkpoint()
        lex.feed("<<=1 ")
        lex.rollback(cp)
        lex.undo_input(5)                  # retract "<<=1 "
        lex.feed(" ")
        result = lex.finish()
        self.assertEqual(result.status, Status.FINISHED)
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("KW", "if"), ("IDENT", "x")])


class UndoInputTest(unittest.TestCase):
    def test_undo_recent_input_and_refeed(self):
        lex = Lexer(build_states(), "main")
        lex.feed("abc ")
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("IDENT", "abc")])
        lex.feed("xyz")
        lex.undo_input(3)
        lex.feed("abd ")
        lex.finish()
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("IDENT", "abd")])

    def test_undo_uncommits_straddling_token(self):
        lex = Lexer(build_states(), "main")
        lex.feed("ab ")
        self.assertEqual(len(lex.drain()), 1)
        lex.undo_input(3)
        lex.feed("ac ")
        lex.finish()
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("IDENT", "ac")])

    def test_undo_bounds_checked(self):
        lex = Lexer(build_states(), "main")
        lex.feed("ab")
        with self.assertRaises(ValueError):
            lex.undo_input(3)


class EdgeCasesTest(unittest.TestCase):
    def test_empty_input(self):
        lex = Lexer(build_states(), "main")
        result = lex.finish()
        self.assertEqual(result.status, Status.FINISHED)
        self.assertEqual(lex.drain(), [])

    def test_feed_after_finish_rejected(self):
        lex = Lexer(build_states(), "main")
        lex.finish()
        with self.assertRaises(RuntimeError):
            lex.feed("x")

    def test_pop_underflow_rejected(self):
        states = {"main": [
            Rule("pop", literal_dfa(")", ALPHABET), 1, Action(None, StateOp.POP)),
        ]}
        lex = Lexer(states, "main")
        with self.assertRaises(RuntimeError):
            lex.feed(")")

    def test_zero_length_match_impossible(self):
        states = {"main": [
            Rule("empty", DFA(ALPHABET, 0, {}, frozenset({0})), 1, Action("E")),
            Rule("a", literal_dfa("a", ALPHABET), 2, Action("A")),
        ]}
        lex = Lexer(states, "main")
        lex.feed("aa")
        lex.finish()
        self.assertEqual([(t.type, t.lexeme) for t in lex.drain()],
                         [("A", "a"), ("A", "a")])


class ChunkingFuzzTest(unittest.TestCase):
    PIECES = ["if", "while", "x", "y", "abc", "ifx", "1", "42", "<", "<<",
              "<<=", "=", "==", "!", "!=", " ", "  ", "\n", "/* c */",
              "/* a /* b */ c */"]

    def test_random_chunkings_match_reference(self):
        states = build_states()
        rng = random.Random(20260926)
        for trial in range(300):
            text = "".join(rng.choice(self.PIECES)
                           for _ in range(rng.randint(1, 12)))
            expected = reference_lex(states, "main", text)
            tokens, results = stream(states, text, random_chunk_sizes(len(text), rng))
            self.assertEqual(tokens, expected, f"trial {trial}: {text!r}")
            self.assertEqual(results[-1].status, Status.FINISHED)

    def test_exhaustive_two_splits(self):
        states = build_states()
        text = "if x<<=1 /*c*/ y!=2 "
        expected = reference_lex(states, "main", text)
        for split in range(len(text) + 1):
            tokens, _ = stream(states, text, [split])
            self.assertEqual(tokens, expected, f"split {split}")


if __name__ == "__main__":
    unittest.main()

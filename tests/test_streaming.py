"""Streaming behaviour: chunking, buffering bound, undo, checkpoints."""
import unittest

from streamlex import Lexer, Rule, Status, char_class_plus, literal, star_then_literal

LETTERS = set("abcdefghijklmnopqrstuvwxyz")


def key_lexer(**kw):
    rules = [
        Rule("KEY", literal("key"), priority=1, states={"S"}),
        Rule("ID", char_class_plus(LETTERS), priority=2, states={"S"}),
        Rule("WS", char_class_plus({" "}), priority=3, states={"S"}, emit=False),
    ]
    return Lexer(rules, alphabet=LETTERS | {" "}, initial_state="S", **kw)


def prefix_lexer(**kw):
    # AB: 'a'* 'b'  (arbitrarily long failing prefix), A: 'a'+
    rules = [
        Rule("AB", star_then_literal({"a"}, "b"), priority=1, states={"S"}),
        Rule("A", char_class_plus({"a"}), priority=2, states={"S"}),
        Rule("WS", char_class_plus({" "}), priority=3, states={"S"}, emit=False),
    ]
    return Lexer(rules, alphabet={"a", "b", " "}, initial_state="S", **kw)


def drain(chunks, lexer):
    tokens = []
    for chunk in chunks:
        tokens.extend(lexer.feed(chunk).tokens)
    tokens.extend(lexer.finish().tokens)
    return tokens


class TestNoPrematureTokens(unittest.TestCase):
    def test_short_token_not_delivered_across_chunks(self):
        lx = key_lexer()
        self.assertEqual(lx.feed("ke").tokens, ())
        self.assertEqual(lx.feed("y").tokens, ())
        res = lx.feed(" ")
        self.assertEqual([t.type for t in res.tokens], ["KEY"])

    def test_long_failing_prefix_not_cut(self):
        lx = prefix_lexer()
        self.assertEqual(lx.feed("aaaa").tokens, ())
        res = lx.feed("b ")
        self.assertEqual([(t.type, t.lexeme) for t in res.tokens], [("AB", "aaaab")])

    def test_failing_prefix_falls_back_to_longest_accept(self):
        lx = prefix_lexer()
        lx.feed("aaaa")
        res = lx.feed(" ")  # 'b' never comes: fall back to A "aaaa"
        self.assertEqual([(t.type, t.lexeme) for t in res.tokens], [("A", "aaaa")])


class TestChunkingEquivalence(unittest.TestCase):
    def test_char_by_char_equals_one_shot(self):
        text = "key keyboard k keyk "
        one_shot = drain([text], key_lexer())
        trickle = drain(list(text), key_lexer())
        self.assertEqual(one_shot, trickle)
        self.assertEqual([t.lexeme for t in one_shot],
                         ["key", "keyboard", "k", "keyk"])


class TestBufferBound(unittest.TestCase):
    def test_over_limit_returns_incomplete_without_cutting(self):
        lx = prefix_lexer(max_buffer=4)
        res = lx.feed("aaaaa")  # AB still alive, buffer 5 > 4
        self.assertEqual(res.status, Status.INCOMPLETE)
        self.assertEqual(res.tokens, ())          # nothing mis-cut
        self.assertEqual(res.buffered, 5)         # buffer kept intact
        # enlarge the bound and continue: the token is still correct
        lx.max_buffer = 64
        res = lx.feed("b ")
        self.assertEqual(res.status, Status.OK)
        self.assertEqual([(t.type, t.lexeme) for t in res.tokens], [("AB", "aaaaab")])

    def test_finish_forces_resolution_despite_limit(self):
        lx = prefix_lexer(max_buffer=4)
        res = lx.feed("aaaaa")
        self.assertEqual(res.status, Status.INCOMPLETE)
        res = lx.finish()  # end of input decides: longest accept is A "aaaaa"
        self.assertEqual([(t.type, t.lexeme) for t in res.tokens], [("A", "aaaaa")])


class TestUndoAndCheckpoint(unittest.TestCase):
    def test_retract_revokes_unconsumed_tail(self):
        lx = prefix_lexer()
        lx.feed("aaa")
        self.assertEqual(lx.retract(2), "aa")
        self.assertEqual(lx.buffered, 1)
        res = lx.feed("b ")
        self.assertEqual([(t.type, t.lexeme) for t in res.tokens], [("AB", "ab")])

    def test_retract_cannot_take_consumed_input(self):
        lx = key_lexer()
        lx.feed("key ")  # KEY consumed; trailing " " is buffered (WS may extend)
        self.assertEqual(lx.retract(10), " ")
        self.assertEqual(lx.buffered, 0)
        self.assertEqual(lx.retract(1), "")  # "key" is already a token, gone

    def test_pushback_requeues_text(self):
        lx = key_lexer()
        lx.feed("key ")
        lx.pushback("key ")
        res = lx.feed("")
        self.assertEqual([t.type for t in res.tokens], ["KEY"])

    def test_checkpoint_restore_rewrites_the_stream(self):
        lx = key_lexer()
        lx.feed("key ")
        cp = lx.checkpoint()
        first = lx.feed("abc ").tokens
        self.assertEqual([t.lexeme for t in first], ["abc"])
        lx.restore(cp)
        second = lx.feed("key ").tokens
        self.assertEqual([t.type for t in second], ["KEY"])
        self.assertEqual(second[0].start, 4)  # offsets continue from checkpoint

    def test_checkpoint_preserves_state_stack(self):
        from streamlex import Push, Pop
        rules = [
            Rule("OPEN", literal("{"), priority=1, states={"A", "B"}, action=Push("B")),
            Rule("CLOSE", literal("}"), priority=1, states={"A", "B"}, action=Pop()),
            Rule("ID", char_class_plus(LETTERS), priority=2, states={"A", "B"}),
            Rule("WS", char_class_plus({" "}), priority=3, states={"A", "B"}, emit=False),
        ]
        lx = Lexer(rules, alphabet=LETTERS | {" ", "{", "}"}, initial_state="A")
        lx.feed("{ ")
        cp = lx.checkpoint()
        lx.restore(cp)
        self.assertEqual(lx.state_stack, ("A", "B"))
        res = lx.feed("x } ")
        self.assertEqual([t.type for t in res.tokens], ["ID", "CLOSE"])
        self.assertEqual(lx.state_stack, ("A",))


if __name__ == "__main__":
    unittest.main()

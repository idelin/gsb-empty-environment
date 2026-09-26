"""Lexical states: stack, push/pop/switch, per-state rules."""
import unittest

from streamlex import (
    Lexer,
    Pop,
    Push,
    Rule,
    StateStackError,
    Switch,
    char_class_plus,
    literal,
)

LETTERS = set("abcdefghijklmnopqrstuvwxyz")


def block_lexer():
    rules = [
        Rule("LBRACE", literal("{"), priority=1, states={"CODE", "BLOCK"}, action=Push("BLOCK")),
        Rule("RBRACE", literal("}"), priority=1, states={"CODE", "BLOCK"}, action=Pop()),
        Rule("ID", char_class_plus(LETTERS), priority=2, states={"CODE", "BLOCK"}),
        Rule("WS", char_class_plus({" "}), priority=3, states={"CODE", "BLOCK"}, emit=False),
    ]
    return Lexer(rules, alphabet=LETTERS | {" ", "{", "}"}, initial_state="CODE")


def string_lexer():
    rules = [
        Rule("QUOTE", literal('"'), priority=1, states={"CODE"}, action=Push("STR")),
        Rule("TEXT", char_class_plus(LETTERS | {" "}), priority=1, states={"STR"}),
        Rule("ENDQ", literal('"'), priority=1, states={"STR"}, action=Pop()),
        Rule("ID", char_class_plus(LETTERS), priority=2, states={"CODE"}),
        Rule("WS", char_class_plus({" "}), priority=3, states={"CODE"}, emit=False),
    ]
    return Lexer(rules, alphabet=LETTERS | {" ", '"'}, initial_state="CODE")


def drain(lexer, chunks):
    tokens = []
    for chunk in chunks:
        tokens.extend(lexer.feed(chunk).tokens)
    tokens.extend(lexer.finish().tokens)
    return tokens


class TestStateStack(unittest.TestCase):
    def test_nested_blocks_push_and_pop(self):
        lx = block_lexer()
        tokens = drain(lx, ["{ a { b } c }"])
        self.assertEqual([t.type for t in tokens],
                         ["LBRACE", "ID", "LBRACE", "ID", "RBRACE", "ID", "RBRACE"])
        self.assertEqual(tokens[3].state, "BLOCK")  # 'b' matched inside BLOCK
        self.assertEqual(tokens[5].state, "BLOCK")  # 'c' still inside outer block
        self.assertEqual(lx.state_stack, ("CODE",))

    def test_pop_on_initial_state_raises(self):
        lx = block_lexer()
        with self.assertRaises(StateStackError):
            lx.feed("} ")

    def test_string_state_enter_and_leave(self):
        lx = string_lexer()
        tokens = drain(lx, ['say "hi', ' yo" end'])
        self.assertEqual(
            [(t.type, t.lexeme) for t in tokens],
            [("ID", "say"), ("QUOTE", '"'), ("TEXT", "hi yo"),
             ("ENDQ", '"'), ("ID", "end")],
        )
        self.assertEqual(lx.state, "CODE")

    def test_switch_replaces_top_state(self):
        rules = [
            Rule("GO", literal("go"), priority=1, states={"A"}, action=Switch("B")),
            Rule("XA", literal("x"), priority=1, states={"A"}),
            Rule("XB", literal("x"), priority=1, states={"B"}),
            Rule("WS", char_class_plus({" "}), priority=3, states={"A", "B"}, emit=False),
        ]
        lx = Lexer(rules, alphabet=LETTERS | {" "}, initial_state="A")
        tokens = drain(lx, ["x go x "])
        self.assertEqual([t.type for t in tokens], ["XA", "GO", "XB"])
        self.assertEqual(lx.state, "B")


class TestSameLexemeDifferentStates(unittest.TestCase):
    def test_same_lexeme_maps_to_different_rules_per_state(self):
        rules = [
            Rule("AX", literal("x"), priority=1, states={"A"}),
            Rule("BX", literal("x"), priority=1, states={"B"}),
            Rule("GO", literal("go"), priority=1, states={"A"}, action=Switch("B")),
            Rule("WS", char_class_plus({" "}), priority=3, states={"A", "B"}, emit=False),
        ]
        lx = Lexer(rules, alphabet=LETTERS | {" "}, initial_state="A")
        tokens = drain(lx, ["x go x "])
        self.assertEqual([t.type for t in tokens], ["AX", "GO", "BX"])

    def test_same_lexeme_same_state_priority_picks_unique_action(self):
        # Both rules accept "x" in state A; only the higher-priority
        # action may fire, so the resulting state is unambiguous.
        rules = [
            Rule("X_PUSH", literal("x"), priority=1, states={"A"}, action=Push("S1")),
            Rule("X_SWITCH", literal("x"), priority=2, states={"A"}, action=Switch("S2")),
            Rule("WS", char_class_plus({" "}), priority=3,
                 states={"A", "S1", "S2"}, emit=False),
        ]
        lx = Lexer(rules, alphabet=LETTERS | {" "}, initial_state="A")
        tokens = drain(lx, ["x "])
        self.assertEqual([t.type for t in tokens], ["X_PUSH"])
        self.assertEqual(lx.state_stack, ("A", "S1"))  # push won, not switch


if __name__ == "__main__":
    unittest.main()

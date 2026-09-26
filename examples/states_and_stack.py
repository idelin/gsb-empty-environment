"""Demo (standalone): lexical state stack for string literals.

Run:  python3.11 examples/states_and_stack.py
"""
import _common  # noqa: F401

from streamlex import Lexer, Pop, Push, Rule, char_class_plus, literal

LETTERS = set("abcdefghijklmnopqrstuvwxyz")
ALPHABET = LETTERS | {" ", '"'}

RULES = [
    Rule("QUOTE", literal('"'), priority=1, states={"CODE"}, action=Push("STR")),
    Rule("TEXT", char_class_plus(LETTERS | {" "}), priority=1, states={"STR"}),
    Rule("ENDQ", literal('"'), priority=1, states={"STR"}, action=Pop()),
    Rule("ID", char_class_plus(LETTERS), priority=2, states={"CODE"}),
    Rule("WS", char_class_plus({" "}), priority=3, states={"CODE"}, emit=False),
]


def main():
    lx = Lexer(RULES, alphabet=ALPHABET, initial_state="CODE")
    for chunk in ['print "hel', 'lo world" done']:
        for tok in lx.feed(chunk).tokens:
            print(f"  {tok.type:<5} {tok.lexeme!r:<12} matched_in_state={tok.state}")
    lx.finish()
    print(f"final state stack: {lx.state_stack}")
    assert lx.state_stack == ("CODE",)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

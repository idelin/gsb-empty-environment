"""Demo (standalone): streaming an expression lexer chunk by chunk.

Run:  python3.11 examples/demo_streaming.py
"""
import _common  # noqa: F401

from streamlex import Lexer, Rule, char_class_plus, literal

LETTERS = set("abcdefghijklmnopqrstuvwxyz")
DIGITS = set("0123456789")
SYMBOLS = set("+-*/=(); ")
ALPHABET = LETTERS | DIGITS | SYMBOLS

RULES = [
    Rule("LET", literal("let"), priority=0, states={"S"}),
    Rule("ID", char_class_plus(LETTERS), priority=1, states={"S"}),
    Rule("NUM", char_class_plus(DIGITS), priority=1, states={"S"}),
    Rule("EQEQ", literal("=="), priority=0, states={"S"}),
    Rule("OP", char_class_plus(set("+-*/=();")), priority=2, states={"S"}),
    Rule("WS", char_class_plus({" "}), priority=9, states={"S"}, emit=False),
]


def main():
    lx = Lexer(RULES, alphabet=ALPHABET, initial_state="S")
    text = "let total == total1 + 42;"
    print(f"input: {text!r}\n")
    for i in range(0, len(text), 7):
        chunk = text[i:i + 7]
        res = lx.feed(chunk)
        for tok in res.tokens:
            print(f"  chunk {chunk!r:>9} -> {tok.type:<4} {tok.lexeme!r} "
                  f"[{tok.start}:{tok.end}]")
        if not res.tokens:
            print(f"  chunk {chunk!r:>9} -> (waiting: longest match undecided)")
    for tok in lx.finish().tokens:
        print(f"  {'finish()':>11} -> {tok.type:<4} {tok.lexeme!r} "
              f"[{tok.start}:{tok.end}]")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

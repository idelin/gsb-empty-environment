"""Counterexample (standalone): naive per-chunk lexing mis-cuts tokens.

A naive streaming lexer finalizes each chunk on its own.  Feed it
"ke" + "y " and it emits ID("ke") then ID("y") -- the keyword "key"
is destroyed at the chunk boundary.  streamlex instead waits until the
longest match is *determined*, so the same chunks yield KEY("key").

Run:  python3.11 examples/counterexample_premature_cut.py
Exit status is non-zero if the demonstration fails.
"""
import _common  # noqa: F401  (sys.path shim)

from streamlex import Lexer, Rule, Status, char_class_plus, lex_all, literal

LETTERS = set("abcdefghijklmnopqrstuvwxyz")
RULES = [
    Rule("KEY", literal("key"), priority=1, states={"S"}),
    Rule("ID", char_class_plus(LETTERS), priority=2, states={"S"}),
    Rule("WS", char_class_plus({" "}), priority=3, states={"S"}, emit=False),
]
CHUNKS = ["ke", "y "]


def naive_per_chunk(chunks):
    """The bug: treat every chunk as if it were the whole input."""
    tokens = []
    for chunk in chunks:
        tokens.extend(lex_all(RULES, initial_state="S", text=chunk))
    return tokens


def streamed(chunks):
    lx = Lexer(RULES, alphabet=LETTERS | {" "}, initial_state="S")
    tokens = []
    for chunk in chunks:
        tokens.extend(lx.feed(chunk).tokens)
    tokens.extend(lx.finish().tokens)
    return tokens


def main():
    naive = [(t.type, t.lexeme) for t in naive_per_chunk(CHUNKS)]
    ours = [(t.type, t.lexeme) for t in streamed(CHUNKS)]
    print(f"chunks: {CHUNKS!r}")
    print(f"naive per-chunk lexer : {naive}   <-- WRONG, keyword split")
    print(f"streamlex             : {ours}")
    assert naive == [("ID", "ke"), ("ID", "y")], "counterexample changed?"
    assert ours == [("KEY", "key")], "streamlex must wait for the longest match"
    print("OK: streamlex held the token until the longest match was decided")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

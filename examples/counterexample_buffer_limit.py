"""Counterexample (standalone): long failing prefixes and the buffer bound.

The rule AB matches 'a'* 'b': the input can grow arbitrarily long
without accepting.  A lexer that "gives up" and cuts the 'a'-run into
an A token would mis-tokenize the moment a 'b' finally arrives.
streamlex keeps the unconsumed prefix, reports Status.INCOMPLETE once
the configured buffer bound is exceeded, and still delivers the right
token afterwards.

Run:  python3.11 examples/counterexample_buffer_limit.py
"""
import _common  # noqa: F401

from streamlex import Lexer, Rule, Status, char_class_plus, star_then_literal

RULES = [
    Rule("AB", star_then_literal({"a"}, "b"), priority=1, states={"S"}),
    Rule("A", char_class_plus({"a"}), priority=2, states={"S"}),
    Rule("WS", char_class_plus({" "}), priority=3, states={"S"}, emit=False),
]


def main():
    lx = Lexer(RULES, alphabet={"a", "b", " "}, initial_state="S", max_buffer=8)
    res = lx.feed("a" * 100)
    print(f"after 100 x 'a': status={res.status.value}, tokens={list(res.tokens)}, "
          f"buffered={res.buffered}")
    assert res.status is Status.INCOMPLETE and res.tokens == ()
    # The bound is explainable: buffer holds exactly the undecided prefix.
    # Raise it (or call finish()) and the token is still cut correctly.
    lx.max_buffer = 1 << 20
    res = lx.feed("b ")
    print(f"after 'b':      status={res.status.value}, "
          f"tokens={[(t.type, len(t.lexeme)) for t in res.tokens]}")
    assert [(t.type, t.lexeme) for t in res.tokens] == [("AB", "a" * 100 + "b")]
    print("OK: INCOMPLETE was reported instead of mis-cutting the token")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

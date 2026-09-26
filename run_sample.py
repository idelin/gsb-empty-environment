"""End-to-end demo of the streaming lexer.  Run: python3.11 run_sample.py"""

from __future__ import annotations

from lexer import Lexer
from sample_lang import build_states

PROGRAM = "if x<<=1 /* nested /* comment */ still comment */ while y!=2"


def demo_chunked_streaming():
    print("== chunked streaming (7-char chunks) ==")
    lex = Lexer(build_states(), "main", max_pending=64)
    for i in range(0, len(PROGRAM), 7):
        chunk = PROGRAM[i:i + 7]
        result = lex.feed(chunk)
        print(f"feed {chunk!r:10} -> {result.status.name:10}"
              f" buffered={result.buffered:2} work={result.work:3}")
        for tok in lex.drain():
            print(f"    {tok.type:5} {tok.lexeme!r:8} @[{tok.start},{tok.end})")
    result = lex.finish()
    print(f"finish     -> {result.status.name}")
    for tok in lex.drain():
        print(f"    {tok.type:5} {tok.lexeme!r:8} @[{tok.start},{tok.end})")


def demo_checkpoint_and_undo():
    print("\n== checkpoint / rollback / undo_input ==")
    lex = Lexer(build_states(), "main")
    lex.feed("if x")
    cp = lex.checkpoint()
    lex.feed("<<=1 ")
    lex.rollback(cp)
    print("after rollback, re-lexed:",
          [(t.type, t.lexeme) for t in lex.drain()])
    lex.rollback(cp)
    lex.undo_input(5)          # retract "<<=1 "
    lex.feed("==2 ")
    lex.finish()
    print("re-fed '==2 ' instead  :",
          [(t.type, t.lexeme) for t in lex.drain()])


def demo_error_recovery():
    print("\n== precise error offset + recovery ==")
    lex = Lexer(build_states(), "main")
    result = lex.feed("ab$cd")
    print(f"feed 'ab$cd' -> {result.status.name}"
          f" at offset {result.error.offset} ({result.error.char!r})")
    print("tokens so far:", [(t.type, t.lexeme) for t in lex.drain()])
    lex.recover()              # skip the offending '$'
    lex.finish()
    print("after recover + finish:",
          [(t.type, t.lexeme) for t in lex.drain()])


if __name__ == "__main__":
    demo_chunked_streaming()
    demo_checkpoint_and_undo()
    demo_error_recovery()

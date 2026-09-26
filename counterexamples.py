"""Independently executable counterexamples.

Each section shows a naive approach failing on a case the streaming lexer
handles correctly.  Run with:  python3.11 counterexamples.py
"""

from __future__ import annotations

from lexer import Lexer, Status
from reference import reference_lex
from sample_lang import build_states


def naive_chunkwise_tokens(chunks):
    """Naive streaming: lex every chunk on its own, final at chunk end."""
    tokens = []
    for chunk in chunks:
        tokens.extend(reference_lex(build_states(), "main", chunk))
    return [(t.type, t.lexeme) for t in tokens]


def ce1_chunk_boundary_mis_cut():
    print("CE1: '<<=' split across chunks")
    chunks = ["a<", "<", "=b "]
    naive = naive_chunkwise_tokens(chunks)
    print("  naive per-chunk lexing:", naive)
    assert naive == [("IDENT", "a"), ("OP", "<"), ("OP", "<"), ("OP", "="),
                     ("IDENT", "b")], "naive should mis-cut here"

    lex = Lexer(build_states(), "main")
    for chunk in chunks:
        lex.feed(chunk)
    lex.finish()
    streamed = [(t.type, t.lexeme) for t in lex.drain()]
    print("  streaming lexer       :", streamed)
    assert streamed == [("IDENT", "a"), ("OP", "<<="), ("IDENT", "b")]
    print("  OK: no premature short token across the chunk boundary")


def ce2_unbounded_pending_prefix():
    print("CE2: long failing prefix (rule needs 'a'*20 then 'b')")
    from lexer import Action, Rule, literal_dfa
    from sample_lang import ALPHABET
    states = {"main": [Rule("long", literal_dfa("a" * 20 + "b", ALPHABET),
                            1, Action("LONG"))]}

    lex = Lexer(states, "main", max_pending=8)
    statuses = []
    for n in (7, 7, 6):                    # 20 x 'a' in three chunks
        result = lex.feed("a" * n)
        statuses.append((n, result.status.name, result.buffered, result.work))
        assert lex.drain() == [], "no token may be cut while undetermined"
    for n, name, buffered, work in statuses:
        print(f"  feed {n} x 'a' -> {name:10} buffered={buffered:2} work={work}")
    assert any(name == "INCOMPLETE" for _, name, _, _ in statuses)
    print("  OK: buffer/work bounds are reported as INCOMPLETE, no mis-cut")

    result = lex.feed("b")
    tokens = [(t.type, t.lexeme) for t in lex.drain()]
    print(f"  feed 'b'     -> {result.status.name:10} tokens={tokens}")
    assert tokens == [("LONG", "a" * 20 + "b")]
    print("  OK: the over-long token still completes correctly")


def ce3_tail_may_extend():
    print("CE3: keyword-looking tail that a later chunk extends")
    chunks = ["if", "x "]
    naive = naive_chunkwise_tokens(chunks)
    print("  naive per-chunk lexing:", naive)
    assert naive == [("KW", "if"), ("IDENT", "x")], "naive commits 'if' too early"

    lex = Lexer(build_states(), "main")
    lex.feed("if")
    assert lex.drain() == [], "must wait: the tail may extend"
    lex.feed("x ")
    lex.finish()
    streamed = [(t.type, t.lexeme) for t in lex.drain()]
    print("  streaming lexer       :", streamed)
    assert streamed == [("IDENT", "ifx")]
    print("  OK: waited for disambiguation instead of emitting KW 'if'")


if __name__ == "__main__":
    ce1_chunk_boundary_mis_cut()
    ce2_unbounded_pending_prefix()
    ce3_tail_may_extend()
    print("all counterexamples behaved as expected")

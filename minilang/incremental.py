"""Incremental re-parsing: reuse unchanged subtrees across edits.

``Session`` holds the current source (as UTF-8 bytes) and its parse tree.
``Session.edit(start, end, replacement)`` splices ``replacement`` over the
byte range ``[start, end)`` and re-parses, reusing every old subtree that

  * lies strictly before or strictly after the edited range (nodes touching
    the range are excluded, since tokens could merge across the boundary),
  * still has identical source text at its (possibly shifted) position, and
  * is followed by the same token as before the edit.

Reused nodes are the *same objects* as in the previous tree; nodes after the
edit have their spans shifted in place (so the old tree must be discarded,
like ``ts_tree_edit`` in tree-sitter). Nodes that cannot be reused are
rebuilt by the normal recursive-descent parser, which guarantees the
incremental tree is structurally identical to a full re-parse.
"""
from __future__ import annotations

from bisect import bisect_left

from . import ast_nodes as ast
from .lexer import (
    lex, LPAREN, PLUS, MINUS, STAR, SLASH, EQEQ, NEQ, LT, LE, GT, GE,
)
from .parser import Candidate, Parser

_BINARY_CONTEXTS = {
    "+": "additive", "-": "additive",
    "*": "multiplicative", "/": "multiplicative",
}


# Tokens that would continue the parse loop of a context. A node whose
# follower is one of these was only an *intermediate* node of that loop
# (e.g. the inner call of f(1)(2), or a+b in a+b+c); reusing it would
# short-circuit the loop, so it is never a valid candidate.
_CONTINUATION_TOKENS = {
    "comparison": {EQEQ, NEQ, LT, LE, GT, GE},
    "additive": {PLUS, MINUS},
    "multiplicative": {STAR, SLASH},
    "postfix": {LPAREN},
}


def context_of(node) -> str | None:
    """Which parse function produced this node (its reuse context)."""
    if isinstance(node, (ast.FnDecl, ast.LetDecl)):
        return "decl"
    if isinstance(node, ast.Binary):
        return _BINARY_CONTEXTS.get(node.op, "comparison")
    if isinstance(node, ast.Unary):
        return "unary"
    if isinstance(node, ast.Call):
        return "postfix"
    if isinstance(node, (ast.Num, ast.Name, ast.Group, ast.If)):
        return "primary"
    return None  # ErrorNode and Program are never reused


def to_bytes(source) -> bytes:
    if isinstance(source, str):
        return source.encode("utf-8")
    return bytes(source)


def is_char_boundary(src: bytes, pos: int) -> bool:
    """True if ``pos`` lies on a UTF-8 code point boundary of ``src``."""
    return pos <= 0 or pos >= len(src) or (src[pos] & 0xC0) != 0x80


def parse(source) -> ast.Program:
    """Full parse of ``source`` (``str`` or UTF-8 ``bytes``)."""
    return Parser(to_bytes(source)).parse_program()


_WHITESPACE = b" \t\r\n"


def _build_reuse_map(old_tree, old_tokens, old_src, new_src,
                     start, end, delta, rep) -> dict:
    old_starts = [t.start for t in old_tokens]
    buckets: dict[int, list[Candidate]] = {}

    # A node *touching* the edit range is still safe to reuse when the byte
    # on the other side of the boundary is whitespace, because tokens can
    # never merge across whitespace. (Comments are handled too: a comment
    # that would swallow the node removes its token boundary, so the reuse
    # lookup in the parser simply never matches.)
    if rep:
        before_edge_safe = rep[:1] in _WHITESPACE
        after_edge_safe = rep[-1:] in _WHITESPACE
    else:
        before_edge_safe = end >= len(old_src) or old_src[end:end + 1] in _WHITESPACE
        after_edge_safe = start == 0 or old_src[start - 1:start] in _WHITESPACE

    def consider(node, context):
        before_ok = node.end < start or (node.end == start and before_edge_safe)
        after_ok = node.start > end or (node.start == end and after_edge_safe)
        if node.end > node.start and (before_ok or after_ok):
            shift = 0 if before_ok else delta
            new_start = node.start + shift
            new_end = node.end + shift
            if new_src[new_start:new_end] == old_src[node.start:node.end]:
                j = bisect_left(old_starts, node.end)
                follower = (old_tokens[j].kind, old_tokens[j].text)
                if follower[0] in _CONTINUATION_TOKENS.get(context, ()):
                    pass  # intermediate node of a parse loop: not reusable
                else:
                    buckets.setdefault(new_start, []).append(
                        Candidate(node, context, new_end, follower, shift))
        # Children of an unusable node may still be reusable themselves.
        for child in node.child_nodes():
            child_context = context_of(child)
            if child_context is not None:
                consider(child, child_context)

    for decl in old_tree.decls:
        context = context_of(decl)
        if context is not None:  # skip top-level ErrorNodes
            consider(decl, context)
    for candidates in buckets.values():
        # Longest first: prefer the outermost call chain / binary chain.
        candidates.sort(key=lambda c: c.node.start - c.node.end)
    return buckets


class Session:
    """Stateful incremental parsing session."""

    def __init__(self, source=b""):
        self.src = to_bytes(source)
        self._parser = Parser(self.src)
        self.tree = self._parser.parse_program()
        self.reused: tuple = ()

    def edit(self, start: int, end: int, replacement=b"") -> ast.Program:
        """Replace bytes ``[start, end)`` with ``replacement`` and re-parse.

        Offsets are UTF-8 byte offsets and must fall on code point
        boundaries. Returns the new tree; ``self.reused`` lists the old
        nodes that were reused (same objects, spans already updated).
        """
        rep = to_bytes(replacement)
        old = self.src
        if not (0 <= start <= end <= len(old)):
            raise ValueError(f"invalid edit range [{start}, {end}) "
                             f"for source of {len(old)} bytes")
        if not is_char_boundary(old, start) or not is_char_boundary(old, end):
            raise ValueError("edit range cuts a UTF-8 code point")
        rep.decode("utf-8")  # replacement must be valid UTF-8
        new = old[:start] + rep + old[end:]
        delta = len(rep) - (end - start)
        reuse_map = _build_reuse_map(self.tree, self._parser.tokens,
                                     old, new, start, end, delta, rep)
        parser = Parser(new, reuse_map)
        self.tree = parser.parse_program()
        self.src = new
        self._parser = parser
        self.reused = tuple(parser.reused)
        return self.tree

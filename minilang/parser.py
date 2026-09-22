"""Recursive-descent parser with error recovery and subtree reuse.

Grammar (all positions are UTF-8 byte offsets):

    program      := decl*
    decl         := 'fn' IDENT '(' params? ')' '=' expr
                  | 'let' IDENT '=' expr
    expr         := comparison
    comparison   := additive (('=='|'!='|'<'|'<='|'>'|'>=') additive)*
    additive     := multiplicative (('+'|'-') multiplicative)*
    multiplicative := unary (('*'|'/') unary)*
    unary        := '-' unary | postfix
    postfix      := primary ('(' args? ')')*
    primary      := NUMBER | IDENT | '(' expr ')'
                  | 'if' expr 'then' expr 'else' expr

Error recovery never consumes the synchronization tokens ``fn``/``let``
(plus `)`, `,`, `then`, `else`, EOF), so an unclosed parenthesis or call
never swallows a following declaration; instead an explicit ``ErrorNode``
is produced.

Subtree reuse: each parse function first asks ``try_reuse`` for a candidate
of its own *context* starting at the current token. A candidate is reused
only when (1) it starts exactly at the current token boundary, (2) its
source text is unchanged, and (3) the token following it is the same as in
the old parse. Because the parser is deterministic with one-token lookahead,
those conditions guarantee the reused node is exactly what a full reparse
would build at this point.
"""
from __future__ import annotations

from bisect import bisect_left

from . import ast_nodes as ast
from .lexer import (
    lex, FN, LET, IF, THEN, ELSE, IDENT, NUMBER,
    LPAREN, RPAREN, COMMA, EQUALS, PLUS, MINUS, STAR, SLASH,
    EQEQ, NEQ, LT, LE, GT, GE, EOF, ERROR,
)

_ADDITIVE = {PLUS: "+", MINUS: "-"}
_MULTIPLICATIVE = {STAR: "*", SLASH: "/"}
_COMPARISON = {EQEQ: "==", NEQ: "!=", LT: "<", LE: "<=", GT: ">", GE: ">="}
_SYNC = {RPAREN, COMMA, FN, LET, THEN, ELSE, EOF}


class Candidate:
    """A reusable node from the previous tree (see incremental.py)."""

    __slots__ = ("node", "context", "end", "follower", "shift")

    def __init__(self, node, context, end, follower, shift):
        self.node = node
        self.context = context
        self.end = end            # new (post-edit) end offset
        self.follower = follower  # (kind, text) of the token after the node
        self.shift = shift        # bytes to add to the node's span on reuse


class Parser:
    def __init__(self, src: bytes, reuse_map: dict | None = None):
        self.src = src
        self.tokens = lex(src)
        self.starts = [t.start for t in self.tokens]
        self.i = 0
        self.reuse_map = reuse_map or {}
        self.reused: list[ast.Node] = []

    # -- token helpers -----------------------------------------------------

    def at(self) -> "object":
        return self.tokens[self.i]

    def advance(self):
        tok = self.tokens[self.i]
        if tok.kind != EOF:
            self.i += 1
        return tok

    def error_pos(self) -> int:
        """Where a missing token was expected: end of the last consumed
        token. Zero-width errors are placed here (never at the lookahead
        token, which may sit far ahead across trivia) so that every node's
        span stays within the tokens its parse actually consumed -- the
        invariant subtree reuse relies on."""
        if self.i > 0:
            return self.tokens[self.i - 1].end
        return self.at().start

    # -- subtree reuse -----------------------------------------------------

    def try_reuse(self, context: str):
        tok = self.at()
        for cand in self.reuse_map.get(tok.start, ()):
            if cand.context != context:
                continue
            j = bisect_left(self.starts, cand.end)
            if j <= self.i:
                continue
            # The candidate must end exactly on a token boundary ...
            if self.tokens[j - 1].end != cand.end:
                continue
            # ... and be followed by the same token as in the old parse.
            follower = self.tokens[j]
            if (follower.kind, follower.text) != cand.follower:
                continue
            if cand.shift:
                cand.node.shift(cand.shift)
            self.i = j
            self.reused.append(cand.node)
            return cand.node
        return None

    # -- program / declarations ---------------------------------------------

    def parse_program(self) -> ast.Program:
        decls = []
        while self.at().kind != EOF:
            node = self.try_reuse("decl")
            if node is not None:
                decls.append(node)
                continue
            kind = self.at().kind
            if kind == FN:
                decls.append(self.parse_fn_decl())
            elif kind == LET:
                decls.append(self.parse_let_decl())
            else:
                decls.append(self.parse_junk())
        return ast.Program(decls, 0, len(self.src))

    def parse_junk(self) -> ast.ErrorNode:
        start = self.at().start
        end = start
        while self.at().kind not in (FN, LET, EOF):
            end = self.advance().end
        return ast.ErrorNode("unexpected token(s) at top level", [], start, end)

    def _decl_error(self, message: str, start: int, min_end: int) -> ast.ErrorNode:
        end = min_end
        while self.at().kind not in (FN, LET, EOF):
            end = self.advance().end
        return ast.ErrorNode(message, [], start, end)

    def parse_fn_decl(self):
        fn_tok = self.advance()  # 'fn'
        if self.at().kind != IDENT:
            return self._decl_error("expected function name",
                                    fn_tok.start, fn_tok.end)
        name = self.advance().text
        if self.at().kind != LPAREN:
            return self._decl_error("expected '(' after function name",
                                    fn_tok.start, fn_tok.end)
        self.advance()
        params = []
        if self.at().kind == IDENT:
            params.append(self.advance().text)
            while self.at().kind == COMMA:
                self.advance()
                if self.at().kind != IDENT:
                    break
                params.append(self.advance().text)
        error = None
        if self.at().kind == RPAREN:
            self.advance()
        else:
            pos = self.error_pos()
            error = ast.ErrorNode("expected ')' in parameter list",
                                  [], pos, pos)
        if self.at().kind != EQUALS:
            return self._decl_error("expected '=' before function body",
                                    fn_tok.start, fn_tok.end)
        self.advance()
        body = self.parse_expr()
        return ast.FnDecl(name, params, body, error, fn_tok.start, body.end)

    def parse_let_decl(self):
        let_tok = self.advance()  # 'let'
        if self.at().kind != IDENT:
            return self._decl_error("expected variable name",
                                    let_tok.start, let_tok.end)
        name = self.advance().text
        if self.at().kind != EQUALS:
            return self._decl_error("expected '=' in let declaration",
                                    let_tok.start, let_tok.end)
        self.advance()
        value = self.parse_expr()
        return ast.LetDecl(name, value, let_tok.start, value.end)

    # -- expressions ---------------------------------------------------------

    def parse_expr(self):
        return self.parse_comparison()

    def _parse_binary(self, context, ops, sub):
        node = self.try_reuse(context)
        if node is not None:
            return node
        left = sub()
        while self.at().kind in ops:
            op = ops[self.advance().kind]
            right = sub()
            left = ast.Binary(op, left, right, left.start, right.end)
        return left

    def parse_comparison(self):
        return self._parse_binary("comparison", _COMPARISON, self.parse_additive)

    def parse_additive(self):
        return self._parse_binary("additive", _ADDITIVE, self.parse_multiplicative)

    def parse_multiplicative(self):
        return self._parse_binary("multiplicative", _MULTIPLICATIVE, self.parse_unary)

    def parse_unary(self):
        node = self.try_reuse("unary")
        if node is not None:
            return node
        if self.at().kind == MINUS:
            tok = self.advance()
            operand = self.parse_unary()
            return ast.Unary("-", operand, tok.start, operand.end)
        return self.parse_postfix()

    def parse_postfix(self):
        node = self.try_reuse("postfix")
        if node is not None:
            return node
        expr = self.parse_primary()
        while self.at().kind == LPAREN:
            self.advance()
            args = []
            error = None
            if self.at().kind != RPAREN:
                args.append(self.parse_expr())
                while self.at().kind == COMMA:
                    self.advance()
                    args.append(self.parse_expr())
            if self.at().kind == RPAREN:
                end = self.advance().end
            else:
                pos = self.error_pos()
                error = ast.ErrorNode("expected ')' after arguments", [], pos, pos)
                end = args[-1].end if args else expr.end
            expr = ast.Call(expr, args, error, expr.start, end)
        return expr

    def parse_primary(self):
        node = self.try_reuse("primary")
        if node is not None:
            return node
        tok = self.at()
        if tok.kind == NUMBER:
            self.advance()
            value = float(tok.text) if "." in tok.text else int(tok.text)
            return ast.Num(value, tok.start, tok.end)
        if tok.kind == IDENT:
            self.advance()
            return ast.Name(tok.text, tok.start, tok.end)
        if tok.kind == LPAREN:
            self.advance()
            inner = self.parse_expr()
            if self.at().kind == RPAREN:
                end = self.advance().end
                return ast.Group(inner, None, tok.start, end)
            pos = self.error_pos()
            error = ast.ErrorNode("expected ')'", [], pos, pos)
            return ast.Group(inner, error, tok.start, inner.end)
        if tok.kind == IF:
            return self.parse_if()
        if tok.kind == ERROR:
            self.advance()
            return ast.ErrorNode("unexpected character", [], tok.start, tok.end)
        # Invariant for subtree reuse: a node's span covers exactly the
        # tokens consumed while parsing it. So the error node gets the
        # token's span only when the token is actually consumed; for
        # synchronization tokens (left for the caller) it is zero-width.
        found = tok.text or tok.kind.lower()
        if tok.kind not in _SYNC:
            self.advance()
            return ast.ErrorNode(f"expected expression, found {found!r}",
                                 [], tok.start, tok.end)
        pos = self.error_pos()
        return ast.ErrorNode(f"expected expression, found {found!r}",
                             [], pos, pos)

    def parse_if(self):
        if_tok = self.advance()  # 'if'
        cond = self.parse_expr()
        error = None
        if self.at().kind == THEN:
            self.advance()
        else:
            pos = self.error_pos()
            error = ast.ErrorNode("expected 'then'", [], pos, pos)
        then = self.parse_expr()
        if self.at().kind == ELSE:
            self.advance()
        elif error is None:
            pos = self.error_pos()
            error = ast.ErrorNode("expected 'else'", [], pos, pos)
        otherwise = self.parse_expr()
        return ast.If(cond, then, otherwise, error, if_tok.start, otherwise.end)

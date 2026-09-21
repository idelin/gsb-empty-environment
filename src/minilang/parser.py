from __future__ import annotations

from .common import SyntaxErr
from .nodes import App, BinOp, BoolLit, If, IntLit, Lam, Let, Token, Var
from .lexer import Lexer

# Lowest to highest precedence.
_BINARY = (
    (("==", "!="),),
    (("<", ">", "<=", ">="),),
    (("+", "-"),),
    (("*", "/"),),
)
_PRECEDENCE: dict[str, int] = {}
for _level, _groups in enumerate(_BINARY):
    for _group in _groups:
        for _op in _group:
            _PRECEDENCE[_op] = _level


def parse(source_bytes: bytes):
    return Parser(Lexer(source_bytes).tokenize()).parse_program()


class Parser:
    def __init__(self, tokens: list[Token]):
        self.toks = tokens
        self.pos = 0

    @property
    def cur(self) -> Token:
        return self.toks[self.pos]

    def advance(self) -> Token:
        tok = self.toks[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def expect(self, kind: str) -> Token:
        if self.cur.kind != kind:
            raise SyntaxErr(f"expected {kind!r} but found {self.cur.text!r}", self.cur.start)
        return self.advance()

    def parse_program(self):
        expr = self.parse_expr()
        self.expect("EOF")
        return expr

    def parse_expr(self) -> object:
        return self.parse_binary(0)

    def parse_binary(self, min_prec: int):
        left = self.parse_app()
        while self.cur.kind in _PRECEDENCE and _PRECEDENCE[self.cur.kind] >= min_prec:
            op_tok = self.advance()
            prec = _PRECEDENCE[op_tok.kind]
            right = self.parse_binary(prec + 1)  # left associative
            left = BinOp(
                start=left.start,
                end=right.end,
                op=op_tok.kind,
                op_start=op_tok.start,
                op_end=op_tok.end,
                left=left,
                right=right,
            )
        return left

    def parse_app(self):
        func = self.parse_atom()
        while self.cur.kind in ("INT", "IDENT", "TRUE", "FALSE", "FUN", "LET", "IF", "("):
            arg = self.parse_atom()
            func = App(start=func.start, end=arg.end, fn=func, arg=arg)
        return func

    def parse_atom(self):
        tok = self.cur
        if tok.kind == "INT":
            self.advance()
            return IntLit(start=tok.start, end=tok.end, value=int(tok.text))
        if tok.kind in ("TRUE", "FALSE"):
            self.advance()
            return BoolLit(start=tok.start, end=tok.end, value=(tok.kind == "TRUE"))
        if tok.kind == "IDENT":
            self.advance()
            return Var(start=tok.start, end=tok.end, name=tok.text)
        if tok.kind == "(":
            self.advance()
            expr = self.parse_expr()
            self.expect(")")
            return expr
        if tok.kind == "FUN":
            return self.parse_lam()
        if tok.kind == "LET":
            return self.parse_let()
        if tok.kind == "IF":
            return self.parse_if()
        raise SyntaxErr(f"expected an expression but found {tok.text!r}", tok.start)

    def parse_lam(self):
        start = self.expect("FUN").start
        param = self.expect("IDENT")
        self.expect(".")
        body = self.parse_expr()
        return Lam(
            start=start,
            end=body.end,
            param=param.text,
            param_start=param.start,
            param_end=param.end,
            body=body,
        )

    def parse_let(self):
        start = self.expect("LET").start
        name = self.expect("IDENT")
        self.expect("=")
        rhs = self.parse_expr()
        self.expect("IN")
        body = self.parse_expr()
        return Let(
            start=start,
            end=body.end,
            name=name.text,
            name_start=name.start,
            name_end=name.end,
            rhs=rhs,
            body=body,
        )

    def parse_if(self):
        start = self.expect("IF").start
        cond = self.parse_expr()
        self.expect("THEN")
        then = self.parse_expr()
        self.expect("ELSE")
        otherwise = self.parse_expr()
        return If(
            start=start,
            end=otherwise.end,
            cond=cond,
            then=then,
            otherwise=otherwise,
        )

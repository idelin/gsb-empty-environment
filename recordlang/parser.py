"""Recursive-descent parser."""

from __future__ import annotations

from .ast_nodes import (
    App, BinOp, BoolLit, Extend, FieldAccess, Fn, If, IntLit, Let, LetStmt,
    ExprStmt, Param, RecordLit, TBool, TFn, TInt, TRecord, UnaryOp, Update, Var,
    record_type,
)
from .errors import LangError

_COMPARISON_OPS = ("==", "!=", "<=", ">=", "<", ">")
_ADDITIVE_OPS = ("+", "-")
_MULTIPLICATIVE_OPS = ("*", "/")


class Parser:
    def __init__(self, tokens):
        self.tokens = tokens
        self.pos = 0

    def peek(self):
        return self.tokens[self.pos]

    def advance(self):
        tok = self.tokens[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def expect(self, kind):
        tok = self.peek()
        if tok.kind != kind:
            raise LangError(
                f"expected {kind!r}, found {tok.text or tok.kind!r}", tok.span
            )
        return self.advance()

    # ----- program & statements -----

    def parse_program(self):
        stmts = []
        while self.peek().kind != "EOF":
            stmts.append(self.parse_stmt())
        return stmts

    def parse_stmt(self):
        tok = self.peek()
        if tok.kind == "let":
            self.advance()
            name = self.expect("IDENT")
            self.expect("=")
            expr = self.parse_expr()
            end = self.expect(";")
            return LetStmt(name.text, name.span, expr, (tok.span[0], end.span[1]))
        expr = self.parse_expr()
        end = self.expect(";")
        return ExprStmt(expr, (expr.span[0], end.span[1]))

    # ----- expressions -----

    def parse_expr(self):
        tok = self.peek()
        if tok.kind == "let":
            self.advance()
            name = self.expect("IDENT")
            self.expect("=")
            value = self.parse_expr()
            self.expect(";")
            body = self.parse_expr()
            return Let(name.text, value, body, (tok.span[0], body.span[1]))
        if tok.kind == "if":
            self.advance()
            cond = self.parse_expr()
            self.expect("then")
            then = self.parse_expr()
            self.expect("else")
            other = self.parse_expr()
            return If(cond, then, other, (tok.span[0], other.span[1]))
        if tok.kind == "extend":
            self.advance()
            # The target is parsed without consuming a trailing `with`, so
            # that `extend r with x = ...` is not misread as an update of r.
            record = self.parse_postfix(allow_with=False)
            self.expect("with")
            name = self.expect("IDENT")
            self.expect("=")
            value = self.parse_expr()
            return Extend(
                record, name.text, name.span, value, (tok.span[0], value.span[1])
            )
        return self.parse_comparison()

    def parse_comparison(self):
        node = self.parse_additive()
        while self.peek().kind in _COMPARISON_OPS:
            op = self.advance()
            rhs = self.parse_additive()
            node = BinOp(op.kind, node, rhs, (node.span[0], rhs.span[1]))
        return node

    def parse_additive(self):
        node = self.parse_multiplicative()
        while self.peek().kind in _ADDITIVE_OPS:
            op = self.advance()
            rhs = self.parse_multiplicative()
            node = BinOp(op.kind, node, rhs, (node.span[0], rhs.span[1]))
        return node

    def parse_multiplicative(self):
        node = self.parse_unary()
        while self.peek().kind in _MULTIPLICATIVE_OPS:
            op = self.advance()
            rhs = self.parse_unary()
            node = BinOp(op.kind, node, rhs, (node.span[0], rhs.span[1]))
        return node

    def parse_unary(self):
        tok = self.peek()
        if tok.kind == "-":
            self.advance()
            operand = self.parse_unary()
            return UnaryOp("-", operand, (tok.span[0], operand.span[1]))
        return self.parse_postfix()

    def parse_postfix(self, allow_with=True):
        node = self.parse_primary()
        while True:
            tok = self.peek()
            if tok.kind == ".":
                self.advance()
                name = self.expect("IDENT")
                node = FieldAccess(
                    node, name.text, name.span, (node.span[0], name.span[1])
                )
            elif tok.kind == "with" and allow_with:
                self.advance()
                name = self.expect("IDENT")
                self.expect("=")
                value = self.parse_expr()
                node = Update(
                    node, name.text, name.span, value, (node.span[0], value.span[1])
                )
            elif tok.kind == "(":
                self.advance()
                args = []
                if self.peek().kind != ")":
                    args.append(self.parse_expr())
                    while self.peek().kind == ",":
                        self.advance()
                        args.append(self.parse_expr())
                end = self.expect(")")
                node = App(node, args, (node.span[0], end.span[1]))
            else:
                return node

    def parse_primary(self):
        tok = self.peek()
        if tok.kind == "INT":
            self.advance()
            return IntLit(int(tok.text), tok.span)
        if tok.kind in ("true", "false"):
            self.advance()
            return BoolLit(tok.kind == "true", tok.span)
        if tok.kind == "IDENT":
            self.advance()
            return Var(tok.text, tok.span)
        if tok.kind == "(":
            self.advance()
            node = self.parse_expr()
            self.expect(")")
            return node
        if tok.kind == "{":
            return self.parse_record_literal()
        if tok.kind == "fn":
            return self.parse_fn()
        raise LangError(
            f"expected an expression, found {tok.text or tok.kind!r}", tok.span
        )

    def parse_record_literal(self):
        start = self.expect("{")
        fields = []
        seen = set()
        if self.peek().kind != "}":
            while True:
                name = self.expect("IDENT")
                if name.text in seen:
                    raise LangError(
                        f"duplicate field '{name.text}' in record literal", name.span
                    )
                seen.add(name.text)
                self.expect("=")
                value = self.parse_expr()
                fields.append((name.text, value, name.span))
                if self.peek().kind == ",":
                    self.advance()
                    continue
                break
        end = self.expect("}")
        return RecordLit(fields, (start.span[0], end.span[1]))

    def parse_fn(self):
        start = self.expect("fn")
        self.expect("(")
        params = []
        if self.peek().kind != ")":
            while True:
                pname = self.expect("IDENT")
                self.expect(":")
                ptype = self.parse_type()
                mode, mend = self.parse_mode()
                params.append(Param(pname.text, ptype, mode, (pname.span[0], mend[1])))
                if self.peek().kind == ",":
                    self.advance()
                    continue
                break
        self.expect(")")
        self.expect("->")
        ret = self.parse_type()
        self.expect("{")
        body = self.parse_expr()
        end = self.expect("}")
        return Fn(params, ret, body, (start.span[0], end.span[1]))

    def parse_mode(self):
        at = self.expect("@")
        tok = self.peek()
        if tok.kind in ("ro", "rw"):
            self.advance()
            return tok.kind, (at.span[0], tok.span[1])
        raise LangError("expected 'ro' or 'rw' after '@'", tok.span)

    # ----- types -----

    def parse_type(self):
        tok = self.peek()
        if tok.kind == "Int":
            self.advance()
            return TInt()
        if tok.kind == "Bool":
            self.advance()
            return TBool()
        if tok.kind == "{":
            self.advance()
            fields = {}
            if self.peek().kind != "}":
                while True:
                    name = self.expect("IDENT")
                    if name.text in fields:
                        raise LangError(
                            f"duplicate field '{name.text}' in record type", name.span
                        )
                    self.expect(":")
                    fields[name.text] = self.parse_type()
                    if self.peek().kind == ",":
                        self.advance()
                        continue
                    break
            self.expect("}")
            return record_type(fields)
        if tok.kind == "fn":
            self.advance()
            self.expect("(")
            params = []
            if self.peek().kind != ")":
                while True:
                    ptype = self.parse_type()
                    mode, _ = self.parse_mode()
                    params.append((ptype, mode))
                    if self.peek().kind == ",":
                        self.advance()
                        continue
                    break
            self.expect(")")
            self.expect("->")
            return TFn(tuple(params), self.parse_type())
        if tok.kind == "(":
            self.advance()
            inner = self.parse_type()
            self.expect(")")
            return inner
        raise LangError(f"expected a type, found {tok.text or tok.kind!r}", tok.span)

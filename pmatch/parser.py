"""Recursive-descent parser.

Grammar (line oriented)::

    program  := union_decl* "match" expr ":" NEWLINE case+ EOF
    union_decl := "union" NAME "=" ctor ("|" ctor)* NEWLINE
    ctor     := NAME ("(" NAME ("," NAME)* ")")?
    case     := "|" pattern ("if" expr)? "->" expr NEWLINE

    pattern  := "_" | INT | STRING | "true" | "false" | "none"
              | NAME                          (variable, lowercase)
              | NAME ("(" pattern ("," pattern)* ")")?   (constructor, uppercase)
              | "(" pattern ("," pattern)* ","? ")"      (tuple / grouping)

    expr     := or_expr
    or_expr  := and_expr ("or" and_expr)*
    and_expr := not_expr ("and" not_expr)*
    not_expr := "not" not_expr | cmp_expr
    cmp_expr := add_expr (("=="|"!="|"<"|"<="|">"|">=") add_expr)?
    add_expr := mul_expr (("+"|"-") mul_expr)*
    mul_expr := unary (("*"|"/"|"%") unary)*
    unary    := "-" unary | atom
    atom     := INT | STRING | "true" | "false" | "none" | NAME
              | NAME "(" expr ("," expr)* ")"
              | "(" expr ("," expr)* ","? ")"
"""
from __future__ import annotations

from .lexer import PError, RESERVED, lex
from .nodes import (Case, EBin, ECall, ECtor, ELit, ETuple, EUn, EVar,
                    PCtor, PLit, PTuple, PVar, PWild, Program)


def parse(src):
    return _Parser(lex(src)).program()


class _Parser:
    def __init__(self, toks):
        self.toks = toks
        self.pos = 0

    # -- token helpers ------------------------------------------------------
    def peek(self, k=0):
        return self.toks[min(self.pos + k, len(self.toks) - 1)]

    def next(self):
        tok = self.toks[self.pos]
        self.pos += 1
        return tok

    def at_op(self, text):
        t = self.peek()
        return t.kind == "OP" and t.text == text

    def at_name(self, text):
        t = self.peek()
        return t.kind == "NAME" and t.text == text

    def expect_kind(self, kind):
        t = self.peek()
        if t.kind != kind:
            raise PError(f"expected {kind}, got {t.text!r}", t.span)
        return self.next()

    def expect_op(self, text):
        t = self.peek()
        if not (t.kind == "OP" and t.text == text):
            raise PError(f"expected {text!r}, got {t.text!r}", t.span)
        return self.next()

    def expect_name(self, text):
        t = self.peek()
        if not (t.kind == "NAME" and t.text == text):
            raise PError(f"expected {text!r}, got {t.text!r}", t.span)
        return self.next()

    def skip_newlines(self):
        while self.peek().kind == "NEWLINE":
            self.next()

    def expect_newline(self):
        t = self.peek()
        if t.kind == "EOF":
            return t
        nl = self.expect_kind("NEWLINE")
        self.skip_newlines()
        return nl

    # -- program ------------------------------------------------------------
    def program(self):
        self.skip_newlines()
        unions = {}
        seen_tags = {}
        while self.at_name("union"):
            self.union_decl(unions, seen_tags)
        self.expect_name("match")
        subject = self.expr()
        self.expect_op(":")
        self.expect_newline()
        cases = []
        while self.at_op("|"):
            cases.append(self.case())
        eof = self.expect_kind("EOF")
        if not cases:
            raise PError("match needs at least one case", eof.span)
        return Program(unions, subject, cases, subject.span)

    def union_decl(self, unions, seen_tags):
        start = self.expect_name("union")
        name_tok = self.expect_kind("NAME")
        uname = name_tok.text
        if not uname[0].isupper():
            raise PError("union name must start with an uppercase letter",
                         name_tok.span)
        if uname in unions:
            raise PError(f"duplicate union {uname!r}", name_tok.span)
        self.expect_op("=")
        ctors = []
        while True:
            tag_tok = self.expect_kind("NAME")
            tag = tag_tok.text
            if not tag[0].isupper():
                raise PError("constructor must start with an uppercase letter",
                             tag_tok.span)
            if tag in seen_tags:
                raise PError(f"duplicate constructor {tag!r}", tag_tok.span)
            arity = 0
            if self.at_op("("):
                self.next()
                self.expect_kind("NAME")     # field names are documentary
                arity = 1
                while self.at_op(","):
                    self.next()
                    self.expect_kind("NAME")
                    arity += 1
                self.expect_op(")")
            ctors.append((tag, arity))
            seen_tags[tag] = uname
            if self.at_op("|"):
                self.next()
                continue
            break
        self.expect_newline()
        unions[uname] = ctors

    def case(self):
        bar = self.expect_op("|")
        pattern = self.pattern()
        guard = None
        if self.at_name("if"):
            self.next()
            guard = self.expr()
        self.expect_op("->")
        body = self.expr()
        end = self.expect_newline()
        return Case(pattern, guard, body, (bar.span[0], end.span[1]))

    # -- patterns -----------------------------------------------------------
    def pattern(self):
        tok = self.peek()
        if tok.kind == "INT":
            self.next()
            return PLit(tok.value, tok.span)
        if tok.kind == "STRING":
            self.next()
            return PLit(tok.value, tok.span)
        if tok.kind == "NAME":
            text = tok.text
            if text == "_":
                self.next()
                return PWild(tok.span)
            if text == "true":
                self.next()
                return PLit(True, tok.span)
            if text == "false":
                self.next()
                return PLit(False, tok.span)
            if text == "none":
                self.next()
                return PLit(None, tok.span)
            if text in RESERVED:
                raise PError(f"keyword {text!r} cannot be used in a pattern",
                             tok.span)
            if text[0].isupper():
                self.next()
                if self.at_op("("):
                    self.next()
                    args = [self.pattern()]
                    while self.at_op(","):
                        self.next()
                        args.append(self.pattern())
                    close = self.expect_op(")")
                    return PCtor(text, args, (tok.span[0], close.span[1]))
                return PCtor(text, [], tok.span)
            self.next()
            return PVar(text, tok.span)
        if self.at_op("("):
            open_tok = self.next()
            first = self.pattern()
            items = [first]
            seen_comma = False
            while self.at_op(","):
                self.next()
                seen_comma = True
                if self.at_op(")"):
                    break
                items.append(self.pattern())
            close = self.expect_op(")")
            if seen_comma:
                return PTuple(items, (open_tok.span[0], close.span[1]))
            return first
        raise PError(f"unexpected {tok.text!r} in pattern", tok.span)

    # -- expressions ----------------------------------------------------------
    def expr(self):
        return self.or_expr()

    def or_expr(self):
        e = self.and_expr()
        while self.at_name("or"):
            op = self.next()
            r = self.and_expr()
            e = EBin("or", e, r, (op.span[0], r.span[1]))
        return e

    def and_expr(self):
        e = self.not_expr()
        while self.at_name("and"):
            op = self.next()
            r = self.not_expr()
            e = EBin("and", e, r, (op.span[0], r.span[1]))
        return e

    def not_expr(self):
        if self.at_name("not"):
            op = self.next()
            operand = self.not_expr()
            return EUn("not", operand, (op.span[0], operand.span[1]))
        return self.cmp_expr()

    def cmp_expr(self):
        e = self.add_expr()
        t = self.peek()
        if t.kind == "OP" and t.text in ("==", "!=", "<", "<=", ">", ">="):
            self.next()
            r = self.add_expr()
            return EBin(t.text, e, r, (e.span[0], r.span[1]))
        return e

    def add_expr(self):
        e = self.mul_expr()
        while True:
            t = self.peek()
            if t.kind == "OP" and t.text in ("+", "-"):
                self.next()
                r = self.mul_expr()
                e = EBin(t.text, e, r, (e.span[0], r.span[1]))
            else:
                return e

    def mul_expr(self):
        e = self.unary()
        while True:
            t = self.peek()
            if t.kind == "OP" and t.text in ("*", "/", "%"):
                self.next()
                r = self.unary()
                e = EBin(t.text, e, r, (e.span[0], r.span[1]))
            else:
                return e

    def unary(self):
        if self.at_op("-"):
            op = self.next()
            operand = self.unary()
            return EUn("-", operand, (op.span[0], operand.span[1]))
        return self.atom()

    def atom(self):
        tok = self.peek()
        if tok.kind == "INT":
            self.next()
            return ELit(tok.value, tok.span)
        if tok.kind == "STRING":
            self.next()
            return ELit(tok.value, tok.span)
        if tok.kind == "NAME":
            text = tok.text
            if text == "true":
                self.next()
                return ELit(True, tok.span)
            if text == "false":
                self.next()
                return ELit(False, tok.span)
            if text == "none":
                self.next()
                return ELit(None, tok.span)
            if text in RESERVED:
                raise PError(f"unexpected keyword {text!r}", tok.span)
            self.next()
            if self.at_op("("):
                self.next()
                args = [self.expr()]
                while self.at_op(","):
                    self.next()
                    args.append(self.expr())
                close = self.expect_op(")")
                if text[0].isupper():
                    return ECtor(text, args, (tok.span[0], close.span[1]))
                return ECall(text, args, (tok.span[0], close.span[1]))
            if text[0].isupper():
                return ECtor(text, [], tok.span)
            return EVar(text, tok.span)
        if self.at_op("("):
            open_tok = self.next()
            first = self.expr()
            items = [first]
            seen_comma = False
            while self.at_op(","):
                self.next()
                seen_comma = True
                if self.at_op(")"):
                    break
                items.append(self.expr())
            close = self.expect_op(")")
            if seen_comma:
                return ETuple(items, (open_tok.span[0], close.span[1]))
            return first
        raise PError(f"unexpected {tok.text!r}", tok.span)

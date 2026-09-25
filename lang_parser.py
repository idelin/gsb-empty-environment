"""Hand-written recursive-descent parser (no eval, no ready-made compiler).

Grammar:
  program  := stmt* EOF
  stmt     := 'let' IDENT '=' expr ';'
            | 'if' expr '{' stmt* '}' ('else' '{' stmt* '}')?
            | 'print' expr ';'
            | expr ';'
  expr     := or_expr
  or_expr  := and_expr ('or' and_expr)*
  and_expr := cmp_expr ('and' cmp_expr)*
  cmp_expr := add_expr (('<'|'<='|'>'|'>='|'=='|'!=') add_expr)?
  add_expr := mul_expr (('+'|'-') mul_expr)*
  mul_expr := unary (('*'|'/'|'%') unary)*
  unary    := ('-'|'not') unary | primary
  primary  := INT | 'true' | 'false' | IDENT | '(' expr ')'
"""
from __future__ import annotations

from lexer import tokenize
from nodes import (Binary, BoolLit, ExprStmt, If, IntLit, Let, Print,
                   Program, Unary, Var)


class ParseError(Exception):
    def __init__(self, message, span):
        super().__init__(message)
        self.message = message
        self.span = span


class Parser:
    def __init__(self, tokens):
        self.toks = tokens
        self.i = 0
        self._last_brace_end = 0

    def peek(self):
        return self.toks[self.i]

    def advance(self):
        tok = self.toks[self.i]
        self.i += 1
        return tok

    def at(self, text):
        tok = self.peek()
        return tok.text == text and tok.kind in ("OP", "KW")

    def expect(self, text):
        if not self.at(text):
            tok = self.peek()
            raise ParseError("expected %r, got %r" % (text, tok.text or "EOF"),
                             tok.span)
        return self.advance()

    def parse_program(self):
        start = self.peek().span[0]
        body = self.parse_statements(stop_at_brace=False)
        end = self.peek().span[1]
        return Program((start, end), body)

    def parse_statements(self, stop_at_brace):
        stmts = []
        while self.peek().kind != "EOF":
            if stop_at_brace and self.at("}"):
                break
            stmts.append(self.parse_statement())
        return stmts

    def parse_statement(self):
        tok = self.peek()
        if tok.text == "let" and tok.kind == "KW":
            return self.parse_let()
        if tok.text == "if" and tok.kind == "KW":
            return self.parse_if()
        if tok.text == "print" and tok.kind == "KW":
            self.advance()
            value = self.parse_expr()
            semi = self.expect(";")
            return Print((tok.span[0], semi.span[1]), value)
        value = self.parse_expr()
        semi = self.expect(";")
        return ExprStmt((value.span[0], semi.span[1]), value)

    def parse_let(self):
        kw = self.advance()  # let
        name = self.advance()
        if name.kind != "IDENT":
            raise ParseError("expected identifier after 'let'", name.span)
        self.expect("=")
        value = self.parse_expr()
        semi = self.expect(";")
        return Let((kw.span[0], semi.span[1]), name.text, name.span, value)

    def parse_if(self):
        kw = self.advance()  # if
        cond = self.parse_expr()
        then_body = self.parse_block()
        else_body = []
        if self.peek().text == "else" and self.peek().kind == "KW":
            self.advance()
            else_body = self.parse_block()
        return If((kw.span[0], self._last_brace_end), cond, then_body,
                  else_body)

    def parse_block(self):
        self.expect("{")
        body = self.parse_statements(stop_at_brace=True)
        brace = self.expect("}")
        self._last_brace_end = brace.span[1]
        return body

    def parse_expr(self):
        return self.parse_or()

    def parse_or(self):
        node = self.parse_and()
        while self.at("or"):
            op = self.advance()
            right = self.parse_and()
            node = Binary((node.span[0], right.span[1]), "or", node, right,
                          op.span)
        return node

    def parse_and(self):
        node = self.parse_cmp()
        while self.at("and"):
            op = self.advance()
            right = self.parse_cmp()
            node = Binary((node.span[0], right.span[1]), "and", node, right,
                          op.span)
        return node

    def parse_cmp(self):
        node = self.parse_add()
        if self.peek().text in ("<", "<=", ">", ">=", "==", "!="):
            op = self.advance()
            right = self.parse_add()
            node = Binary((node.span[0], right.span[1]), op.text, node, right,
                          op.span)
        return node

    def parse_add(self):
        node = self.parse_mul()
        while self.peek().text in ("+", "-"):
            op = self.advance()
            right = self.parse_mul()
            node = Binary((node.span[0], right.span[1]), op.text, node, right,
                          op.span)
        return node

    def parse_mul(self):
        node = self.parse_unary()
        while self.peek().text in ("*", "/", "%"):
            op = self.advance()
            right = self.parse_unary()
            node = Binary((node.span[0], right.span[1]), op.text, node, right,
                          op.span)
        return node

    def parse_unary(self):
        tok = self.peek()
        if tok.text == "-":
            op = self.advance()
            operand = self.parse_unary()
            return Unary((op.span[0], operand.span[1]), "neg", operand,
                         op.span)
        if tok.text == "not" and tok.kind == "KW":
            op = self.advance()
            operand = self.parse_unary()
            return Unary((op.span[0], operand.span[1]), "not", operand,
                         op.span)
        return self.parse_primary()

    def parse_primary(self):
        tok = self.advance()
        if tok.kind == "INT":
            return IntLit(tok.span, int(tok.text))
        if tok.kind == "KW" and tok.text == "true":
            return BoolLit(tok.span, True)
        if tok.kind == "KW" and tok.text == "false":
            return BoolLit(tok.span, False)
        if tok.kind == "IDENT":
            return Var(tok.span, tok.text)
        if tok.text == "(":
            node = self.parse_expr()
            close = self.expect(")")
            node.span = (tok.span[0], close.span[1])
            return node
        raise ParseError("unexpected token %r" % (tok.text or "EOF",),
                         tok.span)


def parse(text):
    """source string -> Program AST; spans are UTF-8 byte ranges."""
    return Parser(tokenize(text)).parse_program()

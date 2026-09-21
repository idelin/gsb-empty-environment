"""手写递归下降语法分析。

文法：
    program   := stmt*
    stmt      := ID '=' expr ';'
               | 'print' expr ';'
               | 'if' '(' expr ')' block ('else' block)?
               | 'while' '(' expr ')' block
    block     := '{' stmt* '}'
    expr      := additive (('=='|'!='|'<'|'<='|'>'|'>=') additive)?
    additive  := multi (('+'|'-') multi)*
    multi     := unary (('*'|'/'|'%') unary)*
    unary     := '-' unary | atom
    atom      := NUM | ID | '(' expr ')'
"""
from __future__ import annotations

from .lexer import Token
from .nodes import (
    Assign, Bin, Expr, If, Neg, Num, Print, Program, Stmt, Var, While,
)

CMP_OPS = {"==", "!=", "<", "<=", ">", ">="}


class ParseError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.start = start
        self.end = end


class Parser:
    def __init__(self, tokens: list[Token]):
        self.toks = tokens
        self.pos = 0

    # ---- 基础工具 ----

    def peek(self, ahead: int = 0) -> Token:
        return self.toks[min(self.pos + ahead, len(self.toks) - 1)]

    def advance(self) -> Token:
        tok = self.toks[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def expect(self, text: str) -> Token:
        tok = self.peek()
        if tok.text != text:
            raise ParseError(f"期望 {text!r}，得到 {tok.text!r}", tok.start, tok.end)
        return self.advance()

    # ---- 程序与语句 ----

    def parse_program(self) -> Program:
        body: list[Stmt] = []
        while self.peek().kind != "EOF":
            body.append(self.parse_stmt())
        return Program(body, (0, self.peek().end))

    def parse_stmt(self) -> Stmt:
        tok = self.peek()
        if tok.kind == "ID" and self.peek(1).text == "=":
            return self.parse_assign()
        if tok.text == "print":
            return self.parse_print()
        if tok.text == "if":
            return self.parse_if()
        if tok.text == "while":
            return self.parse_while()
        raise ParseError(f"无法识别的语句，起始符号 {tok.text!r}", tok.start, tok.end)

    def parse_assign(self) -> Assign:
        name_tok = self.advance()            # ID
        self.expect("=")
        value = self.parse_expr()
        semi = self.expect(";")
        return Assign(name_tok.text, (name_tok.start, name_tok.end),
                      value, (name_tok.start, semi.end))

    def parse_print(self) -> Print:
        kw = self.advance()                  # print
        value = self.parse_expr()
        semi = self.expect(";")
        return Print(value, (kw.start, semi.end))

    def parse_block(self) -> list[Stmt]:
        self.expect("{")
        body: list[Stmt] = []
        while self.peek().text != "}":
            if self.peek().kind == "EOF":
                tok = self.peek()
                raise ParseError("缺少闭合的 '}'", tok.start, tok.end)
            body.append(self.parse_stmt())
        self.expect("}")
        return body

    def parse_if(self) -> If:
        kw = self.advance()                  # if
        self.expect("(")
        cond = self.parse_expr()
        self.expect(")")
        then_body = self.parse_block()
        else_body: list[Stmt] = []
        if self.peek().text == "else":
            self.advance()
            else_body = self.parse_block()
        rbrace = self.toks[self.pos - 1]     # 刚消费掉的 '}'
        return If(cond, then_body, else_body, (kw.start, rbrace.end))

    def parse_while(self) -> While:
        kw = self.advance()                  # while
        self.expect("(")
        cond = self.parse_expr()
        self.expect(")")
        body = self.parse_block()
        rbrace = self.toks[self.pos - 1]
        return While(cond, body, (kw.start, rbrace.end))

    # ---- 表达式 ----

    def parse_expr(self) -> Expr:
        left = self.parse_additive()
        tok = self.peek()
        if tok.text in CMP_OPS:
            self.advance()
            right = self.parse_additive()
            return Bin(tok.text, left, right, (left.span[0], right.span[1]))
        return left

    def parse_additive(self) -> Expr:
        left = self.parse_multi()
        while self.peek().text in ("+", "-"):
            op = self.advance()
            right = self.parse_multi()
            left = Bin(op.text, left, right, (left.span[0], right.span[1]))
        return left

    def parse_multi(self) -> Expr:
        left = self.parse_unary()
        while self.peek().text in ("*", "/", "%"):
            op = self.advance()
            right = self.parse_unary()
            left = Bin(op.text, left, right, (left.span[0], right.span[1]))
        return left

    def parse_unary(self) -> Expr:
        tok = self.peek()
        if tok.text == "-":
            self.advance()
            operand = self.parse_unary()
            return Neg(operand, (tok.start, operand.span[1]))
        return self.parse_atom()

    def parse_atom(self) -> Expr:
        tok = self.peek()
        if tok.kind == "NUM":
            self.advance()
            return Num(int(tok.text), (tok.start, tok.end))
        if tok.kind == "ID":
            self.advance()
            return Var(tok.text, (tok.start, tok.end))
        if tok.text == "(":
            self.advance()
            inner = self.parse_expr()
            self.expect(")")
            return inner
        raise ParseError(f"期望表达式，得到 {tok.text!r}", tok.start, tok.end)

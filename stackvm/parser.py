"""递归下降语法分析。

优先级 (从低到高): or < and < not < 比较 < 加减 < 乘除 < 一元负号 < 基本项。
所有 AST 节点都携带 UTF-8 字节区间 (start, end)。
"""
from __future__ import annotations

from dataclasses import dataclass

from .lexer import Token, tokenize


class ParseError(Exception):
    pass


@dataclass
class Num:
    value: int
    start: int
    end: int


@dataclass
class Bool:
    value: bool
    start: int
    end: int


@dataclass
class Var:
    name: str
    start: int
    end: int


@dataclass
class Bin:
    op: str
    left: "Expr"
    right: "Expr"
    start: int
    end: int


@dataclass
class Logic:
    op: str  # "and" | "or"
    left: "Expr"
    right: "Expr"
    start: int
    end: int


@dataclass
class Unary:
    op: str  # "-" | "not"
    operand: "Expr"
    start: int
    end: int


Expr = Num | Bool | Var | Bin | Logic | Unary


@dataclass
class Let:
    name: str
    expr: Expr
    start: int
    end: int


@dataclass
class Assign:
    name: str
    expr: Expr
    start: int
    end: int


@dataclass
class If:
    cond: Expr
    then: list["Stmt"]
    otherwise: list["Stmt"]
    start: int
    end: int


@dataclass
class While:
    cond: Expr
    body: list["Stmt"]
    start: int
    end: int


@dataclass
class Break:
    start: int
    end: int


@dataclass
class Print:
    expr: Expr
    start: int
    end: int


@dataclass
class ExprStmt:
    expr: Expr
    start: int
    end: int


Stmt = Let | Assign | If | While | Break | Print | ExprStmt


class Parser:
    def __init__(self, tokens: list[Token]):
        self.tokens = tokens
        self.pos = 0

    @property
    def cur(self) -> Token:
        return self.tokens[self.pos]

    def advance(self) -> Token:
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def check(self, text: str) -> bool:
        return self.cur.text == text

    def expect(self, text: str) -> Token:
        tok = self.cur
        if tok.text != text:
            raise ParseError(
                f"字节 {tok.start}: 期望 {text!r}, 实际是 {tok.text!r}"
            )
        return self.advance()

    def parse_program(self) -> list[Stmt]:
        stmts: list[Stmt] = []
        while self.cur.kind != "eof":
            stmts.append(self.parse_stmt())
        return stmts

    def parse_block(self) -> list[Stmt]:
        self.expect("{")
        body: list[Stmt] = []
        while not self.check("}"):
            if self.cur.kind == "eof":
                raise ParseError(f"字节 {self.cur.start}: 缺少 '}}'")
            body.append(self.parse_stmt())
        close = self.advance()
        return body, close.end

    def parse_stmt(self) -> Stmt:
        tok = self.cur

        if tok.text == "let":
            self.advance()
            name = self.advance()
            if name.kind != "ident":
                raise ParseError(f"字节 {name.start}: 'let' 后应为标识符")
            self.expect("=")
            expr = self.parse_expr()
            end = self.expect(";")
            return Let(name.text, expr, tok.start, end.end)

        if tok.text == "if":
            self.advance()
            cond = self.parse_expr()
            then, then_end = self.parse_block()
            otherwise: list[Stmt] = []
            last_end = then_end
            if self.check("else"):
                self.advance()
                otherwise, last_end = self.parse_block()
            return If(cond, then, otherwise, tok.start, last_end)

        if tok.text == "while":
            self.advance()
            cond = self.parse_expr()
            body, last_end = self.parse_block()
            return While(cond, body, tok.start, last_end)

        if tok.text == "break":
            self.advance()
            end = self.expect(";")
            return Break(tok.start, end.end)

        if tok.text == "print":
            self.advance()
            expr = self.parse_expr()
            end = self.expect(";")
            return Print(expr, tok.start, end.end)

        # 赋值语句或表达式语句
        expr = self.parse_expr()
        if self.check("="):
            if not isinstance(expr, Var):
                raise ParseError(f"字节 {expr.start}: 赋值目标必须是变量")
            self.advance()
            value = self.parse_expr()
            end = self.expect(";")
            return Assign(expr.name, value, expr.start, end.end)
        end = self.expect(";")
        return ExprStmt(expr, expr.start, end.end)

    def parse_expr(self) -> Expr:
        return self.parse_or()

    def parse_or(self) -> Expr:
        left = self.parse_and()
        while self.check("or"):
            self.advance()
            right = self.parse_and()
            left = Logic("or", left, right, left.start, right.end)
        return left

    def parse_and(self) -> Expr:
        left = self.parse_not()
        while self.check("and"):
            self.advance()
            right = self.parse_not()
            left = Logic("and", left, right, left.start, right.end)
        return left

    def parse_not(self) -> Expr:
        if self.check("not"):
            tok = self.advance()
            operand = self.parse_not()
            return Unary("not", operand, tok.start, operand.end)
        return self.parse_cmp()

    def parse_cmp(self) -> Expr:
        left = self.parse_add()
        while self.cur.text in ("==", "!=", "<", "<=", ">", ">="):
            op = self.advance()
            right = self.parse_add()
            left = Bin(op.text, left, right, left.start, right.end)
        return left

    def parse_add(self) -> Expr:
        left = self.parse_mul()
        while self.cur.text in ("+", "-"):
            op = self.advance()
            right = self.parse_mul()
            left = Bin(op.text, left, right, left.start, right.end)
        return left

    def parse_mul(self) -> Expr:
        left = self.parse_unary()
        while self.cur.text in ("*", "/"):
            op = self.advance()
            right = self.parse_unary()
            left = Bin(op.text, left, right, left.start, right.end)
        return left

    def parse_unary(self) -> Expr:
        if self.check("-"):
            tok = self.advance()
            operand = self.parse_unary()
            return Unary("-", operand, tok.start, operand.end)
        return self.parse_primary()

    def parse_primary(self) -> Expr:
        tok = self.cur
        if tok.kind == "num":
            self.advance()
            return Num(int(tok.text), tok.start, tok.end)
        if tok.text in ("true", "false"):
            self.advance()
            return Bool(tok.text == "true", tok.start, tok.end)
        if tok.kind == "ident":
            self.advance()
            return Var(tok.text, tok.start, tok.end)
        if self.check("("):
            self.advance()
            expr = self.parse_expr()
            self.expect(")")
            return expr
        raise ParseError(f"字节 {tok.start}: 意外的记号 {tok.text!r}")


def parse(src: str) -> list[Stmt]:
    return Parser(tokenize(src)).parse_program()

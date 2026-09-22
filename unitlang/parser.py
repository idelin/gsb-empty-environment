"""语法分析：递归下降，自实现，不依赖 eval 或现成编译器。

文法（优先级从低到高）：
    program := stmt (NEWLINE stmt)*
    stmt    := "let" IDENT "=" expr | expr
    expr    := conv (("<"|"<="|">"|">="|"=="|"!=") conv)*
    conv    := add ("->" unit_expr)?
    add     := mul (("+"|"-") mul)*
    mul     := unary (("*"|"/") unary)*
    unary   := ("-"|"+") unary | postfix
    postfix := primary unit_expr?        ; 仅数字字面量后可跟单位
    primary := NUMBER | IDENT | "(" expr ")"
    unit_expr := unit_term (("*"|"/") unit_term)*
    unit_term := IDENT ("^" ("-"|"+")? 整数)?
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .errors import LangError
from .lexer import Token
from .units import UNITS


# ---- AST 节点（start/end 均为 UTF-8 字节偏移） ----

@dataclass
class Program:
    stmts: list


@dataclass
class Let:
    name: str
    name_start: int
    name_end: int
    expr: object
    start: int
    end: int


@dataclass
class ExprStmt:
    expr: object
    start: int
    end: int


@dataclass
class Literal:
    mag: Fraction
    unit: "UnitExpr | None"
    start: int
    end: int


@dataclass
class Var:
    name: str
    start: int
    end: int


@dataclass
class BinOp:
    op: str
    left: object
    right: object
    op_start: int
    op_end: int
    start: int
    end: int


@dataclass
class Unary:
    op: str
    operand: object
    start: int
    end: int


@dataclass
class Convert:
    expr: object
    unit: "UnitExpr"
    op_start: int
    op_end: int
    start: int
    end: int


@dataclass
class UnitTerm:
    name: str
    exp: int
    start: int
    end: int


@dataclass
class UnitExpr:
    terms: list = field(default_factory=list)
    start: int = 0
    end: int = 0


class Parser:
    def __init__(self, tokens: list[Token]):
        self.toks = tokens
        self.i = 0

    # ---- 基础工具 ----

    def peek(self, k: int = 0) -> Token:
        return self.toks[min(self.i + k, len(self.toks) - 1)]

    def advance(self) -> Token:
        tok = self.toks[self.i]
        self.i += 1
        return tok

    def at_op(self, *ops: str) -> bool:
        tok = self.peek()
        return tok.kind == "OP" and tok.text in ops

    def skip_nl(self) -> None:
        while self.peek().kind == "NL":
            self.i += 1

    def expect_op(self, op: str) -> Token:
        if not self.at_op(op):
            tok = self.peek()
            raise LangError(f"此处应为 “{op}”，而不是 {tok.text!r}", tok.start, tok.end)
        return self.advance()

    # ---- 程序与语句 ----

    def parse_program(self) -> Program:
        stmts = []
        self.skip_nl()
        while self.peek().kind != "EOF":
            stmts.append(self.parse_stmt())
            tok = self.peek()
            if tok.kind == "NL":
                self.skip_nl()
            elif tok.kind != "EOF":
                raise LangError(f"语句结束处出现意外的内容：{tok.text!r}", tok.start, tok.end)
        return Program(stmts)

    def parse_stmt(self):
        tok = self.peek()
        if tok.kind == "KW" and tok.text == "let":
            self.advance()
            name = self.peek()
            if name.kind != "IDENT":
                raise LangError("let 之后应为变量名", name.start, name.end)
            self.advance()
            self.expect_op("=")
            self.skip_nl()
            expr = self.parse_expr()
            return Let(name.text, name.start, name.end, expr, tok.start, expr.end)
        expr = self.parse_expr()
        return ExprStmt(expr, expr.start, expr.end)

    # ---- 表达式 ----

    def parse_expr(self):
        return self.parse_cmp()

    def parse_cmp(self):
        left = self.parse_conv()
        while self.at_op("<", "<=", ">", ">=", "==", "!="):
            op = self.advance()
            self.skip_nl()
            right = self.parse_conv()
            left = BinOp(op.text, left, right, op.start, op.end, left.start, right.end)
        return left

    def parse_conv(self):
        expr = self.parse_add()
        if self.at_op("->"):
            op = self.advance()
            self.skip_nl()
            unit = self.parse_unit_expr()
            expr = Convert(expr, unit, op.start, op.end, expr.start, unit.end)
        return expr

    def parse_add(self):
        left = self.parse_mul()
        while self.at_op("+", "-"):
            op = self.advance()
            self.skip_nl()
            right = self.parse_mul()
            left = BinOp(op.text, left, right, op.start, op.end, left.start, right.end)
        return left

    def parse_mul(self):
        left = self.parse_unary()
        while self.at_op("*", "/"):
            op = self.advance()
            self.skip_nl()
            right = self.parse_unary()
            left = BinOp(op.text, left, right, op.start, op.end, left.start, right.end)
        return left

    def parse_unary(self):
        if self.at_op("-", "+"):
            op = self.advance()
            operand = self.parse_unary()
            if isinstance(operand, Literal):
                # 负号直接折进数字字面量，使 -5 °C 这类写法合法
                mag = -operand.mag if op.text == "-" else operand.mag
                return Literal(mag, operand.unit, op.start, operand.end)
            return Unary(op.text, operand, op.start, operand.end)
        return self.parse_postfix()

    def parse_postfix(self):
        prim = self.parse_primary()
        if isinstance(prim, Literal) and self.peek().kind == "IDENT":
            unit = self.parse_unit_expr()
            return Literal(prim.mag, unit, prim.start, unit.end)
        return prim

    def parse_primary(self):
        tok = self.peek()
        if tok.kind == "NUM":
            self.advance()
            return Literal(Fraction(tok.text), None, tok.start, tok.end)
        if tok.kind == "IDENT":
            self.advance()
            return Var(tok.text, tok.start, tok.end)
        if self.at_op("("):
            self.advance()
            self.skip_nl()
            expr = self.parse_expr()
            self.skip_nl()
            self.expect_op(")")
            return expr
        raise LangError(f"此处应为表达式，而不是 {tok.text!r}", tok.start, tok.end)

    # ---- 单位表达式 ----

    def parse_unit_expr(self) -> UnitExpr:
        first = self.parse_unit_term()
        terms = [first]
        while True:
            tok = self.peek()
            nxt = self.peek(1)
            if (
                tok.kind == "OP"
                and tok.text in ("*", "/")
                and nxt.kind == "IDENT"
                and nxt.text in UNITS
            ):
                self.advance()
                term = self.parse_unit_term()
                if tok.text == "/":
                    term.exp = -term.exp
                terms.append(term)
            else:
                break
        return UnitExpr(terms, terms[0].start, terms[-1].end)

    def parse_unit_term(self) -> UnitTerm:
        tok = self.peek()
        if tok.kind != "IDENT":
            raise LangError("此处应为单位名称", tok.start, tok.end)
        self.advance()
        if tok.text not in UNITS:
            raise LangError(f"未知单位：{tok.text}", tok.start, tok.end)
        exp = 1
        end = tok.end
        if self.at_op("^"):
            self.advance()
            sign = 1
            if self.at_op("-"):
                self.advance()
                sign = -1
            elif self.at_op("+"):
                self.advance()
            num = self.peek()
            if num.kind != "NUM":
                raise LangError("单位指数应为整数", num.start, num.end)
            self.advance()
            value = Fraction(num.text)
            if value.denominator != 1:
                raise LangError("单位指数应为整数", num.start, num.end)
            exp = sign * value.numerator
            end = num.end
        return UnitTerm(tok.text, exp, tok.start, end)

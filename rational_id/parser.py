"""递归下降解析器：把表达式文本解析为语法树。

支持域（超出即抛 UnsupportedError，由上层判定为“未知”）：
- 运算符：+ - * / ^（^ 的指数必须是非负整数字面量，且有上限）
- 运算数：整数/小数常量（精确转为有理数）、变量标识符、括号
- 不支持：函数调用、负指数/非整数指数/非常量指数、隐式乘法等
"""
from __future__ import annotations

import re
from fractions import Fraction

from .ast_nodes import Add, Const, Div, Mul, Neg, Pow, Sub, Var

MAX_EXPONENT = 100000


class ParseError(Exception):
    """语法错误。"""


class UnsupportedError(Exception):
    """构造超出支持域。"""


_TOKEN_RE = re.compile(r"""
    (?P<num>\d+(?:\.\d+)?)
  | (?P<ident>[A-Za-z_][A-Za-z0-9_]*)
  | (?P<op>[+\-*/^()])
  | (?P<ws>\s+)
""", re.VERBOSE)


def tokenize(src):
    tokens = []
    pos = 0
    while pos < len(src):
        m = _TOKEN_RE.match(src, pos)
        if not m:
            raise ParseError(f"无法识别的字符 {src[pos]!r}（位置 {pos}）")
        pos = m.end()
        if m.lastgroup != "ws":
            tokens.append((m.lastgroup, m.group()))
    return tokens


class _Parser:
    def __init__(self, tokens):
        self.tokens = tokens
        self.i = 0

    def peek(self):
        if self.i < len(self.tokens):
            return self.tokens[self.i]
        return (None, None)

    def advance(self):
        tok = self.peek()
        self.i += 1
        return tok

    def parse_expr(self):
        node = self.parse_term()
        while self.peek()[1] in ("+", "-"):
            op = self.advance()[1]
            rhs = self.parse_term()
            node = Add(node, rhs) if op == "+" else Sub(node, rhs)
        return node

    def parse_term(self):
        node = self.parse_factor()
        while self.peek()[1] in ("*", "/"):
            op = self.advance()[1]
            rhs = self.parse_factor()
            node = Mul(node, rhs) if op == "*" else Div(node, rhs)
        return node

    def parse_factor(self):
        if self.peek()[1] == "-":
            self.advance()
            return Neg(self.parse_factor())
        if self.peek()[1] == "+":
            self.advance()
            return self.parse_factor()
        return self.parse_power()

    def parse_power(self):
        base = self.parse_atom()
        if self.peek()[1] == "^":
            self.advance()
            kind, val = self.peek()
            if kind == "num" and "." not in val:
                self.advance()
                exp = int(val)
                if exp > MAX_EXPONENT:
                    raise UnsupportedError(
                        f"指数 {exp} 超过上限 {MAX_EXPONENT}，超出支持域")
                return Pow(base, exp)
            if val == "-":
                raise UnsupportedError("负指数不在支持域内，请改写为分式")
            raise UnsupportedError("指数必须是非负整数字面量")
        return base

    def parse_atom(self):
        kind, val = self.peek()
        if kind == "num":
            self.advance()
            return Const(Fraction(val))
        if kind == "ident":
            self.advance()
            if self.peek()[1] == "(":
                raise UnsupportedError(f"函数调用 {val}(...) 不在支持域内")
            return Var(val)
        if val == "(":
            self.advance()
            node = self.parse_expr()
            if self.peek()[1] != ")":
                raise ParseError("缺少右括号")
            self.advance()
            return node
        raise ParseError(f"意外的记号 {val!r}")


def parse(src):
    parser = _Parser(tokenize(src))
    node = parser.parse_expr()
    if parser.i != len(parser.tokens):
        raise ParseError("表达式末尾有多余内容（注意：不支持隐式乘法，请写 *）")
    return node

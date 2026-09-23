"""极简多项式解析器：+ - * ^ 与括号、整数及 a/b 分数系数、变量并置隐式乘法。

仅支持非负整数指数（多项式环 Q[x1..xn]），拒绝负指数：
本工具不处理 Laurent 多项式，避免引入 x != 0 之类的定义域限制。
"""
from __future__ import annotations

import re
from fractions import Fraction

from .poly import Polynomial

_TOKEN_RE = re.compile(r"\s*(?:(\d+/\d+)|(\d+)|([A-Za-z_]\w*)|(.))")


class ParseError(ValueError):
    pass


def _tokenize(text: str):
    tokens = []
    pos, n = 0, len(text)
    while pos < n:
        if text[pos].isspace():
            pos += 1
            continue
        m = _TOKEN_RE.match(text, pos)
        if not m:
            raise ParseError(f"无法解析位置 {pos}: {text[pos:]!r}")
        pos = m.end()
        frac, num, ident, other = m.groups()
        if frac:
            tokens.append(("num", Fraction(frac)))
        elif num:
            tokens.append(("num", Fraction(int(num))))
        elif ident:
            tokens.append(("var", ident))
        elif other in "+-*/^()":
            tokens.append((other, other))
        else:
            raise ParseError(f"无法识别的字符: {other!r}")
    tokens.append(("end", None))
    return tokens


class _Parser:
    def __init__(self, tokens):
        self.toks = tokens
        self.i = 0

    def peek(self):
        return self.toks[self.i]

    def next(self):
        tok = self.toks[self.i]
        self.i += 1
        return tok

    def parse_expr(self):
        node = self.parse_term()
        while self.peek()[0] in ("+", "-"):
            op = self.next()[0]
            rhs = self.parse_term()
            node = ("add", node, rhs) if op == "+" else ("sub", node, rhs)
        return node

    def parse_term(self):
        node = self.parse_factor()
        while True:
            tag = self.peek()[0]
            if tag == "*":
                self.next()
                node = ("mul", node, self.parse_factor())
            elif tag in ("num", "var", "("):
                node = ("mul", node, self.parse_factor())
            else:
                return node

    def parse_factor(self):
        if self.peek()[0] == "-":
            self.next()
            return ("neg", self.parse_factor())
        if self.peek()[0] == "+":
            self.next()
            return self.parse_factor()
        node = self.parse_atom()
        if self.peek()[0] == "^":
            self.next()
            tag, val = self.next()
            if tag != "num" or val.denominator != 1 or val < 0:
                raise ParseError("指数必须是非负整数（仅多项式环，不支持负指数）")
            node = ("pow", node, int(val))
        return node

    def parse_atom(self):
        tag, val = self.next()
        if tag == "num":
            return ("num", val)
        if tag == "var":
            return ("var", val)
        if tag == "(":
            node = self.parse_expr()
            if self.next()[0] != ")":
                raise ParseError("缺少右括号")
            return node
        raise ParseError(f"意外的记号: {tag}")


def _collect_vars(node, out):
    tag = node[0]
    if tag == "var":
        if node[1] not in out:
            out.append(node[1])
    elif tag in ("add", "sub", "mul"):
        _collect_vars(node[1], out)
        _collect_vars(node[2], out)
    elif tag in ("neg", "pow"):
        _collect_vars(node[1], out)


def _eval(node, vars):
    tag = node[0]
    if tag == "num":
        return Polynomial.build(vars, {tuple(0 for _ in vars): node[1]})
    if tag == "var":
        mono = tuple(1 if v == node[1] else 0 for v in vars)
        return Polynomial.build(vars, {mono: Fraction(1)})
    if tag == "add":
        return _eval(node[1], vars).add(_eval(node[2], vars))
    if tag == "sub":
        return _eval(node[1], vars).sub(_eval(node[2], vars))
    if tag == "mul":
        return _eval(node[1], vars).mul(_eval(node[2], vars))
    if tag == "neg":
        return _eval(node[1], vars).neg()
    if tag == "pow":
        base = _eval(node[1], vars)
        acc = Polynomial.one(vars)
        for _ in range(node[2]):
            acc = acc.mul(base)
        return acc
    raise AssertionError(tag)


def parse(text: str) -> Polynomial:
    """解析为规范多项式；变量按首次出现顺序排列。"""
    parser = _Parser(_tokenize(text))
    node = parser.parse_expr()
    if parser.peek()[0] != "end":
        raise ParseError("表达式末尾存在多余内容")
    vars: list[str] = []
    _collect_vars(node, vars)
    return _eval(node, tuple(vars))

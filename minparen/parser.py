"""独立递归下降解析器（优先级攀爬）。不依赖打印器。

文法（适用域）
------------
::

    expr(0)   := 二元层 [ '?' expr(0) ':' expr(0) ]   # 条件表达式仅出现在 min_prec == 0
    expr(min) := 前缀项 { 二元op expr(右侧min) }        # 优先级攀爬
    前缀项     := 一元op expr(p_unary) | 原子           # 仅当 p_unary >= min_prec
    原子       := 数 | 变量 | 调用 | '(' expr(0) ')'
    调用       := 标识符 '(' [expr(0) {',' expr(0)}] ')'

关键设计（与打印器严格对应）：

- 右结合运算符（如 ``^``）右侧子表达式以相同优先级递归，左侧不行，
  故 ``2^3^2 == 2^(3^2)``。
- 一元前缀运算符只在其优先级 >= 当前最小优先级时被接受。
  因此 ``-x^2 == -(x^2)``（顶层接受负号，操作数吞下 ``^``），
  而 ``2^-1`` 是解析错误，必须写成 ``2^(-1)``。
- ``none`` 结合性运算符连续出现（如 ``a = b = c``）是解析错误。
- 数字字面量为整数或有限小数，经 ``Fraction(str)`` 精确转换为有理数，
  无浮点损失（``0.1`` 即 1/10）。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .ast_nodes import Binary, Call, Cond, Const, Node, Unary, Var
from .operators import DEFAULT_TABLE, LEFT, NONE, RIGHT, OperatorTable


class ParseError(ValueError):
    """输入串不符合文法。"""


@dataclass(frozen=True)
class Token:
    kind: str  # num | ident | op | lparen | rparen | question | colon | comma | eof
    text: str
    pos: int


_SINGLE = {"(": "lparen", ")": "rparen", "?": "question",
           ":": "colon", ",": "comma"}


def tokenize(text: str, table: OperatorTable = DEFAULT_TABLE) -> list[Token]:
    """把输入串切成记号序列。运算符按最长匹配。"""
    tokens: list[Token] = []
    i, n = 0, len(text)
    while i < n:
        ch = text[i]
        if ch.isspace():
            i += 1
            continue
        if ch.isdigit():
            j = i
            while j < n and text[j].isdigit():
                j += 1
            if j + 1 < n and text[j] == "." and text[j + 1].isdigit():
                j += 1
                while j < n and text[j].isdigit():
                    j += 1
            tokens.append(Token("num", text[i:j], i))
            i = j
            continue
        if ch.isalpha() or ch == "_":
            j = i
            while j < n and (text[j].isalnum() or text[j] == "_"):
                j += 1
            tokens.append(Token("ident", text[i:j], i))
            i = j
            continue
        if ch in _SINGLE:
            tokens.append(Token(_SINGLE[ch], ch, i))
            i += 1
            continue
        for sym in table.symbols:  # 最长符号优先
            if text.startswith(sym, i):
                tokens.append(Token("op", sym, i))
                i += len(sym)
                break
        else:
            raise ParseError(f"位置 {i}: 无法识别的字符 {ch!r}")
    tokens.append(Token("eof", "", n))
    return tokens


class Parser:
    """优先级攀爬解析器。对同一张运算符表，解析结果唯一。"""

    def __init__(self, table: OperatorTable = DEFAULT_TABLE) -> None:
        self.table = table

    def parse(self, text: str) -> Node:
        self._tokens = tokenize(text, self.table)
        self._i = 0
        node = self._expr(0)
        tok = self._peek()
        if tok.kind != "eof":
            raise ParseError(f"位置 {tok.pos}: 意外的记号 {tok.text!r}")
        return node

    # -- 内部 ---------------------------------------------------------

    def _peek(self) -> Token:
        return self._tokens[self._i]

    def _next(self) -> Token:
        tok = self._tokens[self._i]
        self._i += 1
        return tok

    def _expr(self, min_prec: int) -> Node:
        lhs = self._prefix(min_prec)
        while True:
            tok = self._peek()
            if tok.kind != "op" or tok.text not in self.table.binary:
                break
            op = self.table.binary[tok.text]
            if op.precedence < min_prec:
                break
            self._next()
            # 左结合/none：右侧以 p+1 递归；右结合：右侧以 p 递归。
            rhs_min = op.precedence if op.assoc == RIGHT else op.precedence + 1
            rhs = self._expr(rhs_min)
            lhs = Binary(op.symbol, lhs, rhs)
            if op.assoc == NONE:
                nxt = self._peek()
                if (nxt.kind == "op" and nxt.text in self.table.binary
                        and self.table.binary[nxt.text].precedence == op.precedence):
                    raise ParseError(
                        f"位置 {nxt.pos}: 运算符 {op.symbol!r} 不可结合(none)，"
                        "不能连续出现，请加括号"
                    )
        if min_prec == 0 and self._peek().kind == "question":
            self._next()
            then = self._expr(0)
            tok = self._next()
            if tok.kind != "colon":
                raise ParseError(f"位置 {tok.pos}: 条件表达式缺少 ':'")
            otherwise = self._expr(0)  # 右结合
            lhs = Cond(lhs, then, otherwise)
        return lhs

    def _prefix(self, min_prec: int) -> Node:
        tok = self._peek()
        if tok.kind == "op" and tok.text in self.table.unary:
            uop = self.table.unary[tok.text]
            if uop.precedence < min_prec:
                raise ParseError(
                    f"位置 {tok.pos}: 一元运算符 {tok.text!r} 在此处优先级不足，"
                    "需要括号"
                )
            self._next()
            operand = self._expr(uop.precedence)
            return Unary(uop.symbol, operand)
        return self._atom()

    def _atom(self) -> Node:
        tok = self._next()
        if tok.kind == "num":
            # Fraction(str) 对十进制字符串是精确转换，无浮点损失。
            return Const(Fraction(tok.text))
        if tok.kind == "ident":
            if self._peek().kind == "lparen":
                self._next()
                args: list[Node] = []
                if self._peek().kind != "rparen":
                    args.append(self._expr(0))
                    while self._peek().kind == "comma":
                        self._next()
                        args.append(self._expr(0))
                if self._next().kind != "rparen":
                    raise ParseError(f"位置 {tok.pos}: 函数调用缺少 ')'")
                return Call(tok.text, tuple(args))
            return Var(tok.text)
        if tok.kind == "lparen":
            node = self._expr(0)
            if self._next().kind != "rparen":
                raise ParseError(f"位置 {tok.pos}: 缺少 ')'")
            return node
        raise ParseError(f"位置 {tok.pos}: 意外的记号 {tok.text!r}")


def parse(text: str, table: OperatorTable = DEFAULT_TABLE) -> Node:
    """便捷函数：解析完整表达式并返回语法树。"""
    return Parser(table).parse(text)

"""递归下降解析器（与打印器独立，只依赖词法 token 与语法表）。

文法骨架（优先级数字越大越紧密）：

    cond    := binary ('?' cond ':' cond)?
    binary  := unary (OP unary)*        # 由优先级表驱动的 climbing
    unary   := '-' unary | postfix
    postfix := atom ('(' args? ')')*
    atom    := INT | RAT | ID | '(' cond ')'
    args    := cond (',' cond)*

条件表达式按 C 语言约定：优先级低于所有二元算符，右结合。
幂等右结合由语法表 ``^: (4, 'R')`` 给出。
"""

from __future__ import annotations

from . import ast as tree
from .errors import ParseError
from .grammar import Grammar
from .lexer import tokenize


class Parser:
    def __init__(self, grammar: Grammar):
        self.grammar = grammar

    def parse(self, text: str):
        self.tokens = tokenize(text, self.grammar)
        self.pos = 0
        node = self._cond()
        if self._peek()[0] != "eof":
            kind, value = self._peek()
            raise ParseError(f"位置 {self.pos}：表达式结束后仍有多余内容 {value!r}")
        return node

    # --- 基本游标 ---

    def _peek(self):
        return self.tokens[self.pos]

    def _advance(self):
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def _expect(self, kind):
        tok = self._peek()
        if tok[0] != kind:
            raise ParseError(
                f"位置 {self.pos}：期望 {kind!r}，实际得到 {tok[0]}={tok[1]!r}"
            )
        return self._advance()

    # --- 条件表达式（最低优先级，右结合） ---

    def _cond(self):
        cond = self._binary(0)
        if self._peek()[0] == "?":
            self._advance()
            then = self._cond()
            self._expect(":")
            otherwise = self._cond()
            return tree.Cond(cond, then, otherwise)
        return cond

    # --- 二元层：优先级 climbing ---

    def _binary(self, min_prec: int):
        # 一元负号内部归约所有比它更紧密的二元层（如幂），
        # 使得 -x^2 == -(x^2) 而不是 (-x)^2。
        return self._climb(self._unary(), min_prec)

    def _climb(self, left, min_prec: int):
        while True:
            kind, value = self._peek()
            if kind != "op":
                break
            prec = self.grammar.prec_of.get(value)
            if prec is None or prec < min_prec:
                break
            assoc = self.grammar.assoc_of[value]
            self._advance()
            # 左结合：内侧不得吞并同级算符（prec+1）；
            # 右结合：允许吞并同级算符（prec），如 a^b^c == a^(b^c)。
            next_min = prec + 1 if assoc == "L" else prec
            right = self._climb(self._unary(), next_min)
            left = tree.BinOp(value, left, right)
        return left

    # --- 一元负号 ---

    def _unary(self):
        kind, value = self._peek()
        if kind == "op" and value == self.grammar.unary_symbol:
            # '-' 同时是二元算符时，仅在前缀位置（无左操作数）按一元处理。
            self._advance()
            return tree.Neg(self._unary())
        operand = self._postfix()
        return self._climb(operand, self.grammar.unary_prec + 1)

    # --- 函数调用（后缀，最紧密） ---

    def _postfix(self):
        node = self._atom()
        while self._peek()[0] == "(":
            self._advance()
            args = []
            if self._peek()[0] != ")":
                args.append(self._cond())
                while self._peek()[0] == ",":
                    self._advance()
                    args.append(self._cond())
            self._expect(")")
            if not isinstance(node, tree.Var):
                raise ParseError("只能对函数名发起调用")
            node = tree.Call(node.name, tuple(args))
        return node

    # --- 原子 ---

    def _atom(self):
        kind, value = self._peek()
        if kind == "int":
            self._advance()
            return tree.Rat(value)
        if kind == "rat":
            self._advance()
            num, den = value
            return tree.Rat(num, den)
        if kind == "id":
            self._advance()
            return tree.Var(value)
        if kind == "(":
            self._advance()
            node = self._cond()
            self._expect(")")
            return node
        raise ParseError(f"位置 {self.pos}：意外的 token {kind}={value!r}")


def parse(text: str, grammar: Grammar = None):
    grammar = grammar or Grammar.build()
    return Parser(grammar).parse(text)

"""递归下降解析器：正则源码 -> AST（保留源码位置）。

语法：
    alt     := concat ('|' concat)*
    concat  := repeat*
    repeat  := atom ('*' | '+' | '?')*
    atom    := '(' alt ')' | '()' | '[' class ']' | '.' | '\\' escaped | literal

约定：
    ()      空串（Epsilon）
    []      空语言（EmptyLang），与空串严格区分
    [^]     字母表全集
    .       字母表全集（[^] 的别名）
    + / ?   语法糖：x+ == x x*，x? == (x|)
"""
from __future__ import annotations

from .ast_nodes import (Alt, CharClass, Concat, EmptyLang, Epsilon, Group,
                        Literal, Span, Star)
from .errors import ParseError

_ESCAPES = {"n": "\n", "t": "\t", "r": "\r"}


class Parser:
    def __init__(self, source: str):
        self.source = source
        self.i = 0
        self.n_groups = 0

    def parse(self):
        node = self._alt()
        if self.i != len(self.source):
            raise ParseError(f"意外的字符 {self.source[self.i]!r}", self.i)
        return node

    def _peek(self):
        return self.source[self.i] if self.i < len(self.source) else ""

    def _alt(self):
        branches = [self._concat()]
        while self._peek() == "|":
            self.i += 1
            branches.append(self._concat())
        if len(branches) == 1:
            return branches[0]
        return Alt(Span(branches[0].span.start, self.i), tuple(branches))

    def _concat(self):
        start = self.i
        parts = []
        while self.i < len(self.source) and self._peek() not in ")|":
            parts.append(self._repeat())
        if not parts:
            return Epsilon(Span(start, start))
        if len(parts) == 1:
            return parts[0]
        return Concat(Span(start, self.i), tuple(parts))

    def _repeat(self):
        atom = self._atom()
        while self.i < len(self.source) and self._peek() in "*+?":
            op = self.source[self.i]
            self.i += 1
            span = Span(atom.span.start, self.i)
            if op == "*":
                atom = Star(span, atom)
            elif op == "+":
                atom = Concat(span, (atom, Star(span, atom)))
            else:
                atom = Alt(span, (atom, Epsilon(Span(self.i - 1, self.i))))
        return atom

    def _atom(self):
        ch = self._peek()
        start = self.i
        if ch == "":
            raise ParseError("表达式意外结束", self.i)
        if ch == "(":
            self.i += 1
            if self._peek() == ")":
                self.i += 1
                return Epsilon(Span(start, self.i))
            self.n_groups += 1
            index = self.n_groups
            body = self._alt()
            if self._peek() != ")":
                raise ParseError("缺少 )", self.i)
            self.i += 1
            return Group(Span(start, self.i), index, body)
        if ch in ")|":
            raise ParseError(f"意外的字符 {ch!r}", self.i)
        if ch in "*+?":
            raise ParseError("重复符缺少目标", start)
        if ch == "[":
            return self._char_class()
        if ch == ".":
            self.i += 1
            return CharClass(Span(start, self.i), frozenset(), True)
        if ch == "\\":
            return Literal(Span(start, self.i + 2), self._escape())
        self.i += 1
        return Literal(Span(start, self.i), ch)

    def _escape(self):
        start = self.i
        self.i += 1  # 跳过反斜杠
        if self.i >= len(self.source):
            raise ParseError("转义序列未完成", start)
        c = self.source[self.i]
        self.i += 1
        return _ESCAPES.get(c, c)

    def _class_char(self):
        if self.source[self.i] == "\\":
            return self._escape()
        c = self.source[self.i]
        self.i += 1
        return c

    def _char_class(self):
        start = self.i
        self.i += 1  # 跳过 [
        negated = False
        if self._peek() == "^":
            negated = True
            self.i += 1
        if self._peek() == "]":
            self.i += 1
            span = Span(start, self.i)
            if negated:
                return CharClass(span, frozenset(), True)
            return EmptyLang(span)
        chars = set()
        while True:
            if self.i >= len(self.source):
                raise ParseError("字符类缺少 ]", start)
            lo = self._class_char()
            if (self._peek() == "-" and self.i + 1 < len(self.source)
                    and self.source[self.i + 1] != "]"):
                self.i += 1
                hi = self._class_char()
                if ord(hi) < ord(lo):
                    raise ParseError("字符范围颠倒", self.i)
                chars.update(chr(c) for c in range(ord(lo), ord(hi) + 1))
            else:
                chars.add(lo)
            if self._peek() == "]":
                self.i += 1
                break
        return CharClass(Span(start, self.i), frozenset(chars), negated)

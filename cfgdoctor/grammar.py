"""显式字母表与产生式(转移结构)输入的 CFG 表示。

不使用任何现成的正则/自动机库;字母表与产生式全部由调用方显式给出,
构造时即校验封闭性,保证后续分析所依赖的符号集合是良定义的。
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Production:
    lhs: str
    rhs: tuple

    def __str__(self) -> str:
        body = " ".join(self.rhs) if self.rhs else "ε"
        return f"{self.lhs} -> {body}"


class Grammar:
    """上下文无关文法:显式终结符集、非终结符集、开始符与产生式列表。"""

    def __init__(self, terminals, nonterminals, start, productions):
        self.terminals = frozenset(terminals)
        self.nonterminals = frozenset(nonterminals)
        self.start = start
        self.productions = tuple(
            p if isinstance(p, Production) else Production(p[0], tuple(p[1]))
            for p in productions
        )
        self._validate()
        self._by_lhs = {}
        for i, p in enumerate(self.productions):
            self._by_lhs.setdefault(p.lhs, []).append(i)

    def _validate(self):
        overlap = self.terminals & self.nonterminals
        if overlap:
            raise ValueError(f"终结符与非终结符相交: {sorted(overlap)}")
        if self.start not in self.nonterminals:
            raise ValueError(f"开始符 {self.start!r} 不在非终结符集中")
        for p in self.productions:
            if p.lhs not in self.nonterminals:
                raise ValueError(f"产生式左部 {p.lhs!r} 不是已声明的非终结符")
            for s in p.rhs:
                if s not in self.terminals and s not in self.nonterminals:
                    raise ValueError(
                        f"符号 {s!r} 未在字母表中声明(字母表必须显式输入)")

    def production_indices_of(self, lhs):
        return list(self._by_lhs.get(lhs, ()))

    def productions_of(self, lhs):
        return [self.productions[i] for i in self._by_lhs.get(lhs, ())]

    def __repr__(self):
        return (f"Grammar(start={self.start!r}, "
                f"productions={len(self.productions)})")


def format_symbol_string(string) -> str:
    return " ".join(string) if string else "ε"


def sub_grammar(grammar, keep_indices):
    """按保留的产生式编号构造子文法(字母表与开始符不变)。"""
    kept = [grammar.productions[i] for i in keep_indices]
    return Grammar(grammar.terminals, grammar.nonterminals, grammar.start, kept)

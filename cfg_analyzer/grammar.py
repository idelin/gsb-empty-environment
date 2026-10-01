"""文法数据结构：字母表（终结符）与转移结构（产生式）均需显式输入。

不使用任何现成正则 / 自动机库；所有分析均在显式符号表与产生式表上实现。
"""
from __future__ import annotations

import json
from dataclasses import dataclass


class GrammarError(ValueError):
    """文法输入非法。"""


@dataclass(frozen=True)
class Production:
    lhs: str
    rhs: tuple[str, ...]  # 空元组表示 ε（epsilon 产生式）

    def __str__(self) -> str:
        body = " ".join(self.rhs) if self.rhs else "ε"
        return f"{self.lhs} → {body}"


@dataclass(frozen=True)
class Grammar:
    terminals: frozenset[str]
    nonterminals: frozenset[str]
    start: str
    productions: tuple[Production, ...]

    @staticmethod
    def build(terminals, nonterminals, start, productions) -> "Grammar":
        """显式校验字母表与转移结构。productions 元素为 (lhs, rhs可迭代)。"""
        if terminals is None or nonterminals is None:
            raise GrammarError("终结符字母表与非终结符集合必须显式给出（可为空集）")
        terms = frozenset(terminals)
        nonterms = frozenset(nonterminals)
        overlap = terms & nonterms
        if overlap:
            raise GrammarError(f"终结符与非终结符集合相交: {sorted(overlap)}")
        if start not in nonterms:
            raise GrammarError(f"开始符 {start!r} 不在非终结符集合中")
        prods: list[Production] = []
        for i, item in enumerate(productions):
            lhs, rhs = item[0], tuple(item[1])
            if lhs not in nonterms:
                raise GrammarError(f"产生式 #{i} 左部 {lhs!r} 不是非终结符")
            for sym in rhs:
                if sym not in terms and sym not in nonterms:
                    raise GrammarError(
                        f"产生式 #{i} 右部符号 {sym!r} 未在字母表中声明"
                    )
            prods.append(Production(lhs, rhs))
        return Grammar(terms, nonterms, start, tuple(prods))

    @staticmethod
    def from_json(path: str) -> "Grammar":
        """从 JSON 文件加载。格式:
        {"terminals": [...], "nonterminals": [...], "start": "S",
         "productions": [["S", "A b"], {"lhs": "A", "rhs": "a"}, ["B", ""]]}
        右部为空白分隔的符号串，空串表示 ε。
        """
        with open(path, "r", encoding="utf-8") as fh:
            data = json.load(fh)
        return Grammar.from_dict(data)

    @staticmethod
    def from_dict(data: dict) -> "Grammar":
        for key in ("terminals", "nonterminals", "start", "productions"):
            if key not in data:
                raise GrammarError(f"JSON 输入缺少必需字段 {key!r}（字母表与转移结构需显式给出）")
        prods = []
        for item in data["productions"]:
            if isinstance(item, dict):
                lhs, rhs_text = item["lhs"], item.get("rhs", "")
            else:
                lhs, rhs_text = item[0], (item[1] if len(item) > 1 else "")
            rhs = tuple(rhs_text.split()) if isinstance(rhs_text, str) else tuple(rhs_text)
            prods.append((lhs, rhs))
        return Grammar.build(data["terminals"], data["nonterminals"], data["start"], prods)

    def to_dict(self) -> dict:
        return {
            "terminals": sorted(self.terminals),
            "nonterminals": sorted(self.nonterminals),
            "start": self.start,
            "productions": [
                {"lhs": p.lhs, "rhs": list(p.rhs)} for p in self.productions
            ],
        }

    def __str__(self) -> str:
        lines = [
            f"终结符: {{{', '.join(sorted(self.terminals))}}}",
            f"非终结符: {{{', '.join(sorted(self.nonterminals))}}}",
            f"开始符: {self.start}",
            "产生式:",
        ]
        for i, p in enumerate(self.productions):
            lines.append(f"  #{i}: {p}")
        return "\n".join(lines)

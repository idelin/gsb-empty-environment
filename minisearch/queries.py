"""查询树节点与执行前成本估算。

成本模型（估算，单位：操作数）：
- Term:       posting 条目数（扫描倒排表）
- Phrase:     各词 posting 条目数之和 + 最小条目数 * 4（位置对齐检查的启发值）
- Fuzzy:      词表大小 * (len(term)+1) * (len(term)+1)（编辑距离 DP 上界）
              + 命中词 posting 条目数（估算时未知，按词表 10% 词命中、
              每词平均 posting 估算，取下限 1）
- And/Or:     子节点估算之和 + 子结果集合并成本（取子估算之和）
- Not:        子节点估算 + 文档总数（补集必须扫描全部文档，故宽泛 NOT 很贵）

估算是启发式的，只用于执行前的快速拒绝；执行过程中仍按真实操作
逐次 charge，实际超支同样会被预算中断。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .index import Index


class Query:
    def estimate(self, index: "Index") -> int:
        raise NotImplementedError


@dataclass(frozen=True)
class Term(Query):
    term: str

    def estimate(self, index: "Index") -> int:
        return max(1, index.posting_size(self.term))


@dataclass(frozen=True)
class Phrase(Query):
    terms: tuple[str, ...]

    def __init__(self, terms):
        object.__setattr__(self, "terms", tuple(terms))
        if not self.terms:
            raise ValueError("Phrase 至少需要一个词")

    def estimate(self, index: "Index") -> int:
        sizes = [max(1, index.posting_size(t)) for t in self.terms]
        return sum(sizes) + min(sizes) * 4


@dataclass(frozen=True)
class Fuzzy(Query):
    term: str
    max_edits: int = 1

    def estimate(self, index: "Index") -> int:
        vocab = len(index.vocabulary)
        dp_per_word = (len(self.term) + 1) * (len(self.term) + 1)
        avg_posting = (
            sum(index.posting_size(t) for t in index.vocabulary) / vocab
            if vocab else 0
        )
        hit_words = max(1, vocab // 10)
        return vocab * dp_per_word + int(hit_words * avg_posting)


@dataclass(frozen=True)
class And(Query):
    children: tuple[Query, ...] = field(default_factory=tuple)

    def __init__(self, *children: Query):
        object.__setattr__(self, "children", tuple(children))
        if not self.children:
            raise ValueError("And 至少需要一个子查询")

    def estimate(self, index: "Index") -> int:
        subs = [c.estimate(index) for c in self.children]
        return sum(subs) + sum(subs)  # 求值 + 交集合并


@dataclass(frozen=True)
class Or(Query):
    children: tuple[Query, ...] = field(default_factory=tuple)

    def __init__(self, *children: Query):
        object.__setattr__(self, "children", tuple(children))
        if not self.children:
            raise ValueError("Or 至少需要一个子查询")

    def estimate(self, index: "Index") -> int:
        subs = [c.estimate(index) for c in self.children]
        return sum(subs) + sum(subs)  # 求值 + 并集合并


@dataclass(frozen=True)
class Not(Query):
    child: Query

    def estimate(self, index: "Index") -> int:
        return self.child.estimate(index) + index.num_docs

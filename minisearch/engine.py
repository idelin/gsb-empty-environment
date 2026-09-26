"""查询执行器：共享预算、真实计费、未完成标记与统计。

语义约定：
- 每次 search 创建一个 Budget，贯穿整棵查询树，子查询共享、不可重置。
- 执行前用 Query.estimate 做快速拒绝：估算已超预算则直接返回
  incomplete=True、total_hits=None、hits=[]。
- 执行中按真实操作 charge；BudgetExhausted 被捕获后返回
  incomplete=True、total_hits=None（绝不把部分结果伪报为总命中），
  hits 为已算出的部分结果。
- limit 只截断展示条数：total_hits 仍是精确总数，
  stats.limit_applied=True，incomplete 保持 False。
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field

from .budget import Budget, BudgetExhausted
from .index import Index
from .queries import And, Fuzzy, Not, Or, Phrase, Query, Term


@dataclass
class Stats:
    max_ops: int
    ops_used: int = 0
    estimate: int = 0
    elapsed_ms: float = 0.0
    limit: int | None = None
    limit_applied: bool = False
    reason: str = ""  # 未完成原因，完成为空


@dataclass
class SearchResult:
    hits: list[tuple[str, float]] = field(default_factory=list)
    total_hits: int | None = None  # None 表示因预算耗尽而未知
    incomplete: bool = False
    stats: Stats | None = None


def _edit_distance_le(a: str, b: str, k: int, budget: Budget) -> bool:
    """编辑距离 <= k 判定；按 DP 单元格数真实计费，超预算抛 BudgetExhausted。"""
    la, lb = len(a), len(b)
    if abs(la - lb) > k:
        budget.charge(1)
        return False
    prev = list(range(lb + 1))
    budget.charge(lb + 1)
    for i in range(1, la + 1):
        cur = [i] + [0] * lb
        row_min = cur[0]
        for j in range(1, lb + 1):
            cost = 0 if a[i - 1] == b[j - 1] else 1
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost)
            row_min = min(row_min, cur[j])
        budget.charge(lb)
        if row_min > k:
            return False
        prev = cur
    return prev[lb] <= k


class Engine:
    def __init__(self, index: Index) -> None:
        self.index = index

    def search(
        self,
        query: Query,
        *,
        max_ops: int = 100_000,
        limit: int | None = None,
    ) -> SearchResult:
        stats = Stats(max_ops=max_ops, limit=limit)
        start = time.perf_counter()
        budget = Budget(max_ops)

        estimate = query.estimate(self.index)
        stats.estimate = estimate
        if estimate > max_ops:
            stats.reason = (
                f"估算成本 {estimate} 超过预算 {max_ops}，执行前拒绝"
            )
            stats.elapsed_ms = (time.perf_counter() - start) * 1000
            return SearchResult(hits=[], total_hits=None, incomplete=True,
                                stats=stats)

        try:
            scored = self._eval(query, budget)
        except BudgetExhausted as exc:
            stats.ops_used = budget.used
            stats.reason = str(exc)
            stats.elapsed_ms = (time.perf_counter() - start) * 1000
            partial = []  # 结果集可能只算了一半，不给可能误导的部分命中
            return SearchResult(hits=partial, total_hits=None, incomplete=True,
                                stats=stats)

        stats.ops_used = budget.used
        stats.elapsed_ms = (time.perf_counter() - start) * 1000
        ordered = sorted(scored.items(), key=lambda kv: (-kv[1], kv[0]))
        total = len(ordered)
        if limit is not None and limit < total:
            stats.limit_applied = True
            ordered = ordered[:limit]
        return SearchResult(hits=ordered, total_hits=total, incomplete=False,
                            stats=stats)

    # ---- 递归求值：全部共享同一个 budget ----

    def _eval(self, q: Query, budget: Budget) -> dict[str, float]:
        if isinstance(q, Term):
            return self._eval_term(q, budget)
        if isinstance(q, Phrase):
            return self._eval_phrase(q, budget)
        if isinstance(q, Fuzzy):
            return self._eval_fuzzy(q, budget)
        if isinstance(q, And):
            return self._eval_and(q, budget)
        if isinstance(q, Or):
            return self._eval_or(q, budget)
        if isinstance(q, Not):
            return self._eval_not(q, budget)
        raise TypeError(f"未知查询节点: {q!r}")

    def _eval_term(self, q: Term, budget: Budget) -> dict[str, float]:
        out: dict[str, float] = {}
        for doc_id, positions in self.index.postings(q.term).items():
            budget.charge(1)
            out[doc_id] = float(len(positions))
        return out

    def _eval_phrase(self, q: Phrase, budget: Budget) -> dict[str, float]:
        postings = []
        for t in q.terms:
            p = self.index.postings(t)
            budget.charge(max(1, len(p)))
            postings.append(p)
        common = set(postings[0])
        for p in postings[1:]:
            common &= set(p)
            budget.charge(len(p))
        out: dict[str, float] = {}
        for doc_id in common:
            budget.charge(1)
            first_positions = postings[0][doc_id]
            matched = 0
            for start in first_positions:
                budget.charge(1)
                ok = True
                for offset, p in enumerate(postings[1:], start=1):
                    budget.charge(1)
                    if start + offset not in p.get(doc_id, []):
                        ok = False
                        break
                if ok:
                    matched += 1
            if matched:
                out[doc_id] = float(matched)
        return out

    def _eval_fuzzy(self, q: Fuzzy, budget: Budget) -> dict[str, float]:
        out: dict[str, float] = {}
        for word in self.index.vocabulary:
            budget.charge(1)  # 词表扫描本身计费
            if _edit_distance_le(q.term, word, q.max_edits, budget):
                for doc_id, positions in self.index.postings(word).items():
                    budget.charge(1)
                    out[doc_id] = out.get(doc_id, 0.0) + len(positions)
        return out

    def _eval_and(self, q: And, budget: Budget) -> dict[str, float]:
        results = [self._eval(c, budget) for c in q.children]
        acc = results[0]
        for r in results[1:]:
            budget.charge(min(len(acc), len(r)))
            smaller, larger = (r, acc) if len(r) < len(acc) else (acc, r)
            acc = {d: smaller[d] + larger[d] for d in smaller if d in larger}
        return acc

    def _eval_or(self, q: Or, budget: Budget) -> dict[str, float]:
        acc: dict[str, float] = {}
        for c in q.children:
            r = self._eval(c, budget)
            budget.charge(len(r))
            for d, s in r.items():
                acc[d] = acc.get(d, 0.0) + s
        return acc

    def _eval_not(self, q: Not, budget: Budget) -> dict[str, float]:
        # 宽泛 NOT 受控：补集必须扫描全部文档，逐文档计费，
        # 预算不足时会被中断并返回 incomplete，而不是静默截断。
        excluded = self._eval(q.child, budget)
        out: dict[str, float] = {}
        for doc_id in self.index.doc_ids:
            budget.charge(1)
            if doc_id not in excluded:
                out[doc_id] = 1.0
        return out

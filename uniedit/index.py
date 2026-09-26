"""安全索引：度量时走 BK 树剪枝，否则回退到带可证明下界的全扫描。

- 重复规范词只保留首次出现（不重复输出）。
- 排序键为 (distance, position)，稳定且确定。
- 阈值含等号；TopK 返回第 k 小距离 tau 内的全部候选（等距完整）。
- 非度量成本（如换位过便宜、ins != del）自动回退全扫描，
  全扫描仅用 length_lower_bound 剪枝——该下界不依赖三角不等式，
  因此不会漏词；返回的始终是精确距离。
"""
from __future__ import annotations

import heapq
import unicodedata
from dataclasses import dataclass
from typing import Dict, Iterable, List, Optional, Tuple

from .bktree import BKTree
from .costs import CostModel
from .distance import distance, length_lower_bound
from .metric import MetricReport, check_metric
from .tokenize import normalize_query, tokenize


@dataclass(frozen=True)
class Hit:
    word: str  # 规范词（casefold）
    surface: str  # 首次出现的原始形式
    distance: float  # 精确距离
    position: int  # 首次出现的词元位置


@dataclass
class SearchResult:
    query: str
    hits: List[Hit]
    mode: str  # "bk-tree" | "full-scan"
    metric_ok: bool
    visited: int  # 实际计算精确距离的词条数
    total: int  # 索引词条总数


class WordIndex:
    def __init__(self, costs: CostModel) -> None:
        self.costs = costs
        self._words: List[str] = []  # 规范词，按首次出现位置排序
        self._surface: Dict[str, str] = {}
        self._position: Dict[str, int] = {}
        self._tree = BKTree(costs)
        self._alphabet: set = set()
        self._metric_cache: Dict[frozenset, MetricReport] = {}
        self.last_report: Optional[MetricReport] = None

    @classmethod
    def from_text(cls, text: str, costs: CostModel) -> "WordIndex":
        idx = cls(costs)
        for tok in tokenize(text):
            idx.add(tok.norm, tok.surface, tok.position)
        return idx

    @classmethod
    def from_words(cls, words: Iterable[str], costs: CostModel) -> "WordIndex":
        idx = cls(costs)
        for pos, w in enumerate(words):
            norm = unicodedata.normalize("NFC", w).casefold()
            idx.add(norm, w, pos)
        return idx

    def add(self, norm: str, surface: Optional[str] = None, position: Optional[int] = None) -> None:
        if norm in self._position:
            return  # 重复规范词不重复收录
        if position is None:
            position = len(self._words)
        self._words.append(norm)
        self._surface[norm] = norm if surface is None else surface
        self._position[norm] = position
        self._alphabet.update(norm)
        self._tree.add(norm)

    def __len__(self) -> int:
        return len(self._words)

    def metric_ok(self, query: str) -> bool:
        return self._report_for(query).ok

    def _report_for(self, query: str) -> MetricReport:
        alpha = frozenset(self._alphabet | set(query))
        report = self._metric_cache.get(alpha)
        if report is None:
            report = check_metric(self.costs, alpha)
            self._metric_cache[alpha] = report
        self.last_report = report
        return report

    def search(
        self,
        query: str,
        *,
        max_dist: Optional[float] = None,
        top_k: Optional[int] = None,
    ) -> SearchResult:
        """阈值（含等号）与/或 TopK 查询。返回精确距离、稳定排序。"""
        if max_dist is None and top_k is None:
            raise ValueError("max_dist 与 top_k 至少给出一个")
        query = normalize_query(query)
        ok = self._report_for(query).ok
        if ok:
            if top_k is not None:
                pairs, visited = self._tree.topk(query, top_k)
            else:
                pairs, visited = self._tree.search(query, max_dist)
            mode = "bk-tree"
        else:
            pairs, visited = self._full_scan(query, max_dist, top_k)
            mode = "full-scan"
        hits = [
            Hit(w, self._surface[w], d, self._position[w]) for w, d in pairs
        ]
        if max_dist is not None:
            hits = [h for h in hits if h.distance <= max_dist]
        hits.sort(key=lambda h: (h.distance, h.position))
        return SearchResult(query, hits, mode, ok, visited, len(self._words))

    def _full_scan(
        self,
        query: str,
        max_dist: Optional[float],
        top_k: Optional[int],
    ) -> Tuple[List[Tuple[str, float]], int]:
        """安全参考实现：仅用长度差下界剪枝，结果与全量精确计算一致。"""
        computed: List[Tuple[str, float]] = []
        visited = 0
        best: List[Tuple[float, str]] = []
        tau: Optional[float] = None
        for w in self._words:
            bound = max_dist
            if top_k is not None and tau is not None:
                bound = tau if bound is None else min(bound, tau)
            if bound is not None and length_lower_bound(query, w, self.costs) > bound:
                continue  # 下界不依赖三角不等式，剪掉安全
            d = distance(query, w, self.costs)
            visited += 1
            computed.append((w, d))
            if top_k is not None:
                heapq.heappush(best, (-d, w))
                if len(best) > top_k:
                    heapq.heappop(best)
                if len(best) == top_k:
                    tau = -best[0][0]
        if top_k is not None:
            if tau is None:
                tau = max((d for _, d in computed), default=0.0)
            pairs = [(w, d) for w, d in computed if d <= tau]
        else:
            pairs = [(w, d) for w, d in computed if d <= max_dist]
        return pairs, visited

"""BK 树：度量保证成立时的剪枝索引。

构造不变式：节点 p 的槽位 w 的子树中，所有词 x 满足 d(p, x) == w。
查询剪枝（需三角不等式 + 对称性，见 metric.py）：
    d(q, x) >= |d(q, p) - d(p, x)| = |d(q, p) - w|
故阈值 t 下只需进入 |d(q,p) - w| <= t 的槽位；TopK 用当前第 k 小
距离 tau 做分支限界，tau 单调下降，剪枝始终安全；最后再按 tau 做
一次阈值包含等号的收集，保证等距候选完整。
"""
from __future__ import annotations

import heapq
from typing import Dict, List, Optional, Tuple

from .costs import CostModel
from .distance import distance


class _Node:
    __slots__ = ("word", "children")

    def __init__(self, word: str) -> None:
        self.word = word
        self.children: Dict[float, "_Node"] = {}


class BKTree:
    def __init__(self, costs: CostModel) -> None:
        self.costs = costs
        self.root: Optional[_Node] = None
        self.size = 0

    def add(self, word: str) -> None:
        if self.root is None:
            self.root = _Node(word)
            self.size = 1
            return
        node = self.root
        while True:
            d = distance(node.word, word, self.costs)
            if d == 0:
                return  # 重复词（调用方已按规范词去重，此处仅防御）
            child = node.children.get(d)
            if child is None:
                node.children[d] = _Node(word)
                self.size += 1
                return
            node = child

    def search(self, query: str, max_dist: float) -> Tuple[List[Tuple[str, float]], int]:
        """阈值查询（含等号）。返回 (命中列表, 实际计算距离的节点数)。"""
        hits: List[Tuple[str, float]] = []
        if self.root is None:
            return hits, 0
        visited = 0
        stack = [self.root]
        while stack:
            node = stack.pop()
            visited += 1
            d = distance(query, node.word, self.costs)
            if d <= max_dist:
                hits.append((node.word, d))
            lo, hi = d - max_dist, d + max_dist
            for w, child in node.children.items():
                if lo <= w <= hi:
                    stack.append(child)
        return hits, visited

    def topk(self, query: str, k: int) -> Tuple[List[Tuple[str, float]], int]:
        """TopK 查询：返回距离第 k 小值 tau 内的全部词（等距完整）。"""
        if k <= 0 or self.root is None:
            return [], 0
        visited = 0
        best: List[Tuple[float, str]] = []  # 最大堆（距离取负）
        tau = None
        stack = [self.root]
        while stack:
            node = stack.pop()
            visited += 1
            d = distance(query, node.word, self.costs)
            heapq.heappush(best, (-d, node.word))
            if len(best) > k:
                heapq.heappop(best)
            if len(best) == k:
                tau = -best[0][0]
            if tau is not None:
                lo, hi = d - tau, d + tau
                for w, child in node.children.items():
                    if lo <= w <= hi:
                        stack.append(child)
            else:
                stack.extend(node.children.values())
        if tau is None:  # 词数不足 k：全部返回
            tau = -best[0][0] if best else 0.0
        hits, v2 = self.search(query, tau)  # 收集所有 <= tau 的候选（含等距）
        return hits, visited + v2

"""BK 树剪枝索引。

键函数 ``key(x, y) = c_min * damerau_levenshtein(x, y)`` 是度量，
并且是查询用精确加权距离的下界（证明见 README），
因此经典 BK 树剪枝在这里是 **安全的**：

    对查询 q、半径 r、节点 n（dk = key(q, n)），
    子树边权 e = key(n, x)（对该子树中所有 x 精确成立），
    若 |dk - e| > r，则子树中任意 x 满足
        exact(q, x) >= key(q, x) >= |dk - e| > r，
    可以整棵剪掉；阈值包含等号，所以只在严格大于时剪枝。

查询：
- ``search(query, max_dist)``：距离 <= max_dist（含等号）的全部候选；
- ``topk(query, k)``：距离最小的 k 名，**等距候选完整保留**
  （若第 k 名与第 k+1 名等距，则一并返回，返回数可超过 k）。
两者结果均按 (距离, 词表首次出现序号) 排序，稳定且确定。
"""

from __future__ import annotations

import heapq
from collections import namedtuple

from .distance import Weights, damerau_levenshtein, weighted_distance
from .tokenizer import normalize_query, tokenize, vocabulary

Match = namedtuple("Match", ["word", "distance"])


class _Node:
    __slots__ = ("word", "seq", "children")

    def __init__(self, word: str, seq: int) -> None:
        self.word = word
        self.seq = seq
        self.children: dict[float, "_Node"] = {}


class BKTreeIndex:
    """基于安全下界键的 BK 树索引。"""

    def __init__(self, weights: Weights = Weights()) -> None:
        self.weights = weights
        self.root: _Node | None = None
        self.size = 0
        # 查询统计（用于演示与测试剪枝有效性）
        self.last_visited = 0
        self.last_exact_computed = 0

    # ---------------- 构建 ----------------

    @classmethod
    def from_text(cls, text: str, weights: Weights = Weights()) -> "BKTreeIndex":
        """从本地 UTF-8 文本分词、去重并建索引。"""
        index = cls(weights)
        index.build(vocabulary(tokenize(text)))
        return index

    def build(self, words: list[str]) -> None:
        """按给定顺序插入（顺序即稳定排序用的序号）。"""
        for seq, word in enumerate(words):
            self._insert(word, seq)
        self.size = len(words)

    def _key(self, a: str, b: str) -> float:
        return self.weights.min_cost * damerau_levenshtein(a, b)

    def _insert(self, word: str, seq: int) -> None:
        if self.root is None:
            self.root = _Node(word, seq)
            return
        node = self.root
        while True:
            edge = self._key(node.word, word)
            child = node.children.get(edge)
            if child is None:
                node.children[edge] = _Node(word, seq)
                return
            node = child

    # ---------------- 查询 ----------------

    def search(self, query: str, max_dist: float) -> list[Match]:
        """阈值查询：返回距离 <= max_dist（含等号）的全部候选。"""
        q = normalize_query(query)
        found = self._collect(q, max_dist)
        return [Match(word, dist) for dist, _, word in found]

    def topk(self, query: str, k: int) -> list[Match]:
        """TopK 查询：第 k 名距离上的等距候选完整保留，顺序稳定。"""
        if k < 1:
            raise ValueError("k 必须 >= 1")
        q = normalize_query(query)
        # 阶段 1：自适应半径遍历，求第 k 小的距离 d_k。
        radius = self._find_kth_distance(q, k)
        # 阶段 2：以 d_k 为半径做阈值查询，收齐全部等距候选。
        found = self._collect(q, radius)
        return [Match(word, dist) for dist, _, word in found]

    # ---------------- 内部 ----------------

    def _exact(self, a: str, b: str) -> float:
        self.last_exact_computed += 1
        return weighted_distance(a, b, self.weights)

    def _collect(self, q: str, radius: float) -> list[tuple[float, int, str]]:
        """半径查询，返回 (精确距离, 序号, 词)，按 (距离, 序号) 稳定排序。"""
        self.last_visited = 0
        self.last_exact_computed = 0
        results: list[tuple[float, int, str]] = []
        if self.root is None:
            return results
        stack = [self.root]
        while stack:
            node = stack.pop()
            self.last_visited += 1
            dk = self._key(q, node.word)
            dist = self._exact(q, node.word)
            if dist <= radius:
                results.append((dist, node.seq, node.word))
            lo, hi = dk - radius, dk + radius
            for edge, child in node.children.items():
                # 仅当 |dk - edge| <= radius 时才需要进入子树；
                # 严格大于时下界已超阈值，剪枝安全（见模块 docstring）。
                if lo <= edge <= hi:
                    stack.append(child)
        results.sort(key=lambda item: (item[0], item[1]))
        return results

    def _find_kth_distance(self, q: str, k: int) -> float:
        """遍历索引求第 k 小的精确距离（半径随堆收缩而剪枝）。"""
        if self.root is None:
            return float("inf")
        heap: list[tuple[float, int, str]] = []  # 以 -距离 模拟最大堆
        radius = float("inf")
        stack = [self.root]
        while stack:
            node = stack.pop()
            dk = self._key(q, node.word)
            dist = weighted_distance(q, node.word, self.weights)
            if len(heap) < k:
                heapq.heappush(heap, (-dist, node.seq, node.word))
            elif dist < -heap[0][0]:
                heapq.heapreplace(heap, (-dist, node.seq, node.word))
            if len(heap) == k:
                radius = -heap[0][0]
            lo, hi = dk - radius, dk + radius
            for edge, child in node.children.items():
                if lo <= edge <= hi:
                    stack.append(child)
        return radius

"""准标识符泛化层级（树形）定义。

约定（与 README 一致）：
- 空值（None）单独成类，标签为 NULL_LABEL，不参与任何泛化合并；
  在所有泛化层级上保持独立类别。
- 层级用 child -> parent 映射描述，根节点的父节点指向自身。
- 泛化 level 表示从叶子向上爬的层数，到达根后不再上升。
"""
from __future__ import annotations

NULL_LABEL = "∅(空值)"


class Hierarchy:
    """单个准标识符的泛化层级树。"""

    def __init__(self, name: str, parents: dict[str, str]):
        self.name = name
        self.parents: dict[str, str] = {}
        for child, parent in parents.items():
            if child is None or parent is None:
                raise ValueError("空值不允许写入层级；空值自动单独成类")
            self.parents[str(child)] = str(parent)
        roots = [n for n, p in self.parents.items() if n == p]
        if not roots:
            raise ValueError(f"层级 {name} 缺少根节点（父节点指向自身的节点）")

        self.children: dict[str, list[str]] = {}
        for child, parent in self.parents.items():
            if child != parent:
                self.children.setdefault(parent, []).append(child)
        self.leaves = [n for n in self.parents if n not in self.children]

        self._depth_cache: dict[str, int] = {}
        for node in self.parents:
            self._depth(node)  # 同时校验无环
        self.max_level = max(self._depth(leaf) for leaf in self.leaves)

        # 空值单独作为一个类别计入总类别数
        self.total_categories = len(self.leaves) + 1

        self._leaf_count_cache: dict[str, int] = {}

    def _depth(self, node: str) -> int:
        if node in self._depth_cache:
            return self._depth_cache[node]
        depth = 0
        cur = node
        seen = set()
        while self.parents[cur] != cur:
            if cur in seen:
                raise ValueError(f"层级 {self.name} 存在环，节点 {node} 不可达根")
            seen.add(cur)
            cur = self.parents[cur]
            depth += 1
        self._depth_cache[node] = depth
        return depth

    def generalize(self, value, level: int) -> str:
        """把原始值泛化 level 层；空值恒返回 NULL_LABEL。"""
        if value is None:
            return NULL_LABEL
        node = str(value)
        if node not in self.parents:
            raise KeyError(f"值 {value!r} 不在层级 {self.name!r} 中")
        for _ in range(level):
            parent = self.parents[node]
            if parent == node:
                break
            node = parent
        return node

    def _leaf_count(self, node: str) -> int:
        if node in self._leaf_count_cache:
            return self._leaf_count_cache[node]
        kids = self.children.get(node)
        count = 1 if not kids else sum(self._leaf_count(k) for k in kids)
        self._leaf_count_cache[node] = count
        return count

    def weight(self, value, level: int) -> float:
        """NCP 权重：泛化节点覆盖的叶子类别数 / 总类别数。

        空值为单例类别，权重恒为 1 / total_categories。
        """
        if value is None:
            return 1.0 / self.total_categories
        node = self.generalize(value, level)
        return self._leaf_count(node) / self.total_categories

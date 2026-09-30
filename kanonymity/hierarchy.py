"""准标识符泛化层级（generalization hierarchy）。

定义
----
- 每个准标识符（QI）对应一棵泛化树：叶子为原始取值，内部节点为泛化类别，
  根节点为 ``*``（完全泛化）。
- 层级（level）：叶子为 0，每向上一层 +1；根所在层为最大层。
- 空值（None 或空串）被映射为独立的 ``NULL`` 类别：它是直接挂在根下的
  独立叶子，不与任何非空取值合并，除非泛化到根层 ``*``。
- 信息损失采用 NCP（Normalized Certainty Penalty）的类别形式：
  取值泛化到节点 n 的损失为 ``(|leaves(n)| - 1) / (|leaves(root)| - 1)``，
  叶子层为 0，根层为 1，随层级单调不减。
"""
from __future__ import annotations

from dataclasses import dataclass, field

NULL = "<NULL>"  # 空值独立类别
ROOT = "*"


@dataclass
class Hierarchy:
    """单个准标识符的泛化层级树。"""

    name: str
    parent: dict[str, str | None]  # node -> parent；根的 parent 为 None
    _children: dict[str, list[str]] = field(default_factory=dict, init=False)
    _depth: dict[str, int] = field(default_factory=dict, init=False)
    _leaves_cache: dict[str, frozenset[str]] = field(default_factory=dict, init=False)

    def __post_init__(self) -> None:
        if ROOT not in self.parent:
            raise ValueError(f"hierarchy {self.name!r} 缺少根节点 {ROOT!r}")
        if self.parent[ROOT] is not None:
            raise ValueError("根节点的 parent 必须为 None")
        if NULL not in self.parent:
            # 空值自动注册为根下的独立叶子
            self.parent[NULL] = ROOT
        children: dict[str, list[str]] = {node: [] for node in self.parent}
        for node, par in self.parent.items():
            if par is not None:
                if par not in self.parent:
                    raise ValueError(f"节点 {node!r} 的父节点 {par!r} 未定义")
                children[par].append(node)
        self._children = children
        self._depth = {}
        for node in self.parent:
            self._depth[node] = self._compute_depth(node)

    def _compute_depth(self, node: str) -> int:
        depth = 0
        cur = node
        seen = set()
        while self.parent[cur] is not None:
            if cur in seen:
                raise ValueError(f"层级树存在环：{cur!r}")
            seen.add(cur)
            cur = self.parent[cur]  # type: ignore[index]
            depth += 1
        return depth

    @property
    def max_level(self) -> int:
        """最大泛化层：树中最深叶子的深度（根所在层）。"""
        return max(self._depth.values())

    @property
    def leaves(self) -> frozenset[str]:
        return self._leaves_of(ROOT)

    def _leaves_of(self, node: str) -> frozenset[str]:
        if node in self._leaves_cache:
            return self._leaves_cache[node]
        if not self._children[node]:
            result = frozenset({node})
        else:
            result = frozenset().union(
                *(self._leaves_of(c) for c in self._children[node])
            )
        self._leaves_cache[node] = result
        return result

    @staticmethod
    def normalize(value: object) -> str:
        """空值（None / 空白串）映射为独立的 NULL 类别。"""
        if value is None:
            return NULL
        text = str(value)
        if text.strip() == "":
            return NULL
        return text

    def generalize(self, value: object, level: int) -> str:
        """把取值泛化到指定层；超过根层时钳制在根。"""
        node = self.normalize(value)
        if node not in self.parent:
            raise KeyError(f"取值 {node!r} 不在层级 {self.name!r} 的域中")
        steps = min(level, self.max_level)
        while steps > 0 and self.parent[node] is not None:
            node = self.parent[node]  # type: ignore[index]
            steps -= 1
        return node

    def ncp(self, value: object, level: int) -> float:
        """取值泛化到指定层的归一化信息损失，范围 [0, 1]。"""
        node = self.generalize(value, level)
        total = len(self._leaves_of(ROOT))
        if total <= 1:
            return 0.0
        return (len(self._leaves_of(node)) - 1) / (total - 1)

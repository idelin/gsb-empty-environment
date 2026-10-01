"""敏感值层级：由 child -> parent 映射生成指定层级的合并映射。"""

from __future__ import annotations

from typing import Hashable


class Hierarchy:
    def __init__(self, parents: dict[Hashable, Hashable]):
        self.parents = dict(parents)

    def ancestor(self, value: Hashable, levels: int) -> Hashable:
        current = value
        for _ in range(levels):
            if current not in self.parents:
                break
            current = self.parents[current]
        return current

    def mapping_at_level(self, values, levels: int) -> dict:
        """返回 {原值: 层级祖先}；到达顶层后保持不变（恒等映射）。"""
        return {value: self.ancestor(value, levels) for value in values}

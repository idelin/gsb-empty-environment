"""敏感值层级：child -> parent 映射，合并时解析到根节点。

合并通过 ClassStats.merge_values 执行，原贡献保留在撤销日志中，
rollback() 可精确恢复。
"""
from __future__ import annotations

from typing import Hashable, Iterable


class Hierarchy:
    def __init__(self, parent_map: dict[Hashable, Hashable]):
        self.parent_map = dict(parent_map)

    def root(self, value: Hashable) -> Hashable:
        """沿 parent 链解析到根；带环检测，成环时停在环入口。"""
        seen = set()
        current = value
        while current in self.parent_map and current not in seen:
            seen.add(current)
            current = self.parent_map[current]
        return current

    def merge_mapping(self, values: Iterable[Hashable]) -> dict[Hashable, Hashable]:
        """为给定值生成 {原值: 根} 的合并映射；null 与已是根的值不映射。"""
        mapping = {}
        for value in values:
            if value is None:
                continue
            root = self.root(value)
            if root != value:
                mapping[value] = root
        return mapping

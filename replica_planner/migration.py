"""迁移计划：从当前放置迁移到目标放置，全程满足最低副本数。"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class MigrationOp:
    """单步迁移操作：add 表示向节点迁入副本，remove 表示从节点移除副本。"""

    action: str
    node_id: str

    def __post_init__(self):
        if self.action not in ("add", "remove"):
            raise ValueError(f"未知操作: {self.action}")


def build_migration_plan(current, target, min_replicas: int) -> list[MigrationOp]:
    """生成迁移操作序列：先全部 add 再全部 remove。

    这样副本数从 |current| 单调上升到 |current| + |adds|，再下降回 |target|，
    任意时刻都不低于 min(|current|, |target|) >= min_replicas，
    即迁移全程满足最低副本数约束。
    """
    current = tuple(current)
    target = tuple(target)
    if len(set(current)) != len(current):
        raise ValueError("当前放置存在重复节点（一个副本不能重复计在同节点）")
    if len(set(target)) != len(target):
        raise ValueError("目标放置存在重复节点（一个副本不能重复计在同节点）")
    if len(current) < min_replicas:
        raise ValueError(
            f"当前副本数 {len(current)} 已低于最低副本数 {min_replicas}，"
            "无法构造满足不变式的迁移计划"
        )
    if len(target) < min_replicas:
        raise ValueError(
            f"目标副本数 {len(target)} 低于最低副本数 {min_replicas}"
        )

    current_set = set(current)
    target_set = set(target)
    plan = [MigrationOp("add", nid) for nid in target if nid not in current_set]
    plan += [MigrationOp("remove", nid) for nid in current if nid not in target_set]

    count = len(current)
    for op in plan:
        count += 1 if op.action == "add" else -1
        if count < min_replicas:
            raise AssertionError(f"迁移过程中副本数跌破最低副本数: {count} < {min_replicas}")
    return plan


def simulate_plan(plan, initial_count: int) -> list[int]:
    """返回执行计划每一步之后的副本数序列，便于校验最低副本数不变式。"""
    counts = []
    count = initial_count
    for op in plan:
        count += 1 if op.action == "add" else -1
        counts.append(count)
    return counts

"""核心数据模型：记录、等价类、k-匿名判定与信息损失。

定义
----
- 等价类（equivalence class）：在给定泛化层向量下，泛化后准标识符元组
  完全相同的记录集合。
- k-匿名：每个未抑制等价类的“计数”>= k。计数方式由 ``count_mode`` 决定：
  ``record`` 按记录数；``subject`` 按不同主体数（同一主体多条记录只计 1）。
- 不足 k 记录：所属等价类计数 < k 的记录。
- 抑制（suppression）：把记录从发布集中移除，每条被抑制记录计入
  ``suppression_penalty`` 的损失。
- 重要：每次泛化层变化后，等价类与计数都从原始记录重新计算，
  不沿用任何旧分组计数。
"""
from __future__ import annotations

from dataclasses import dataclass

from .hierarchy import Hierarchy


@dataclass(frozen=True)
class Record:
    """一条合成数据记录。attrs 与 hierarchies 的顺序对齐。"""

    rid: str
    subject: str
    attrs: tuple[object, ...]


@dataclass(frozen=True)
class EquivalenceClass:
    key: tuple[str, ...]
    member_rids: tuple[str, ...]
    count: int  # 按 count_mode 计算
    size: int   # 记录条数


def generalized_key(
    record: Record, levels: tuple[int, ...], hierarchies: tuple[Hierarchy, ...]
) -> tuple[str, ...]:
    return tuple(
        h.generalize(v, lv) for h, v, lv in zip(hierarchies, record.attrs, levels)
    )


def compute_classes(
    records: list[Record],
    levels: tuple[int, ...],
    hierarchies: tuple[Hierarchy, ...],
    count_mode: str = "record",
) -> dict[tuple[str, ...], EquivalenceClass]:
    """从原始记录重新分组并重新计数（不沿用旧计数）。"""
    if count_mode not in ("record", "subject"):
        raise ValueError(f"未知 count_mode: {count_mode!r}")
    groups: dict[tuple[str, ...], list[Record]] = {}
    for rec in records:
        key = generalized_key(rec, levels, hierarchies)
        groups.setdefault(key, []).append(rec)
    classes: dict[tuple[str, ...], EquivalenceClass] = {}
    for key, members in groups.items():
        if count_mode == "subject":
            count = len({m.subject for m in members})
        else:
            count = len(members)
        classes[key] = EquivalenceClass(
            key=key,
            member_rids=tuple(m.rid for m in members),
            count=count,
            size=len(members),
        )
    return classes


def record_loss(
    record: Record, levels: tuple[int, ...], hierarchies: tuple[Hierarchy, ...]
) -> float:
    """单条记录的泛化损失：各 QI 的 NCP 平均值。"""
    if not hierarchies:
        return 0.0
    return sum(
        h.ncp(v, lv) for h, v, lv in zip(hierarchies, record.attrs, levels)
    ) / len(hierarchies)


@dataclass(frozen=True)
class Evaluation:
    """一个泛化层向量的评估结果。"""

    levels: tuple[int, ...]
    classes: dict[tuple[str, ...], EquivalenceClass]
    deficient_keys: frozenset[tuple[str, ...]]
    min_suppression: int  # 使该层向量可行的最少抑制记录数
    gen_loss: float       # 未抑制记录的泛化损失之和
    suppression_loss: float
    total_loss: float

    def feasible(self, budget: int) -> bool:
        return self.min_suppression <= budget


def evaluate(
    records: list[Record],
    levels: tuple[int, ...],
    hierarchies: tuple[Hierarchy, ...],
    k: int,
    count_mode: str = "record",
    suppression_penalty: float = 1.0,
) -> Evaluation:
    """评估一个泛化层向量。

    对固定层向量，唯一有益的抑制是整体移除计数 < k 的等价类
    （从已达 k 的类中再删记录只会增加损失），因此最少抑制数
    为所有不足 k 类的成员记录数之和。
    """
    classes = compute_classes(records, levels, hierarchies, count_mode)
    deficient = frozenset(key for key, ec in classes.items() if ec.count < k)
    suppressed_rids = {
        rid for key in deficient for rid in classes[key].member_rids
    }
    gen_loss = sum(
        record_loss(rec, levels, hierarchies)
        for rec in records
        if rec.rid not in suppressed_rids
    )
    suppression_loss = suppression_penalty * len(suppressed_rids)
    return Evaluation(
        levels=levels,
        classes=classes,
        deficient_keys=deficient,
        min_suppression=len(suppressed_rids),
        gen_loss=gen_loss,
        suppression_loss=suppression_loss,
        total_loss=gen_loss + suppression_loss,
    )

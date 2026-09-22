"""可分解聚合：本地（local）/ 全局（global）两阶段执行，不依赖 SQL 引擎。

数据模型与语义约定
------------------
- 表（Table）是行（dict）的**多重集合**：重复行重复计数，不去重。
- NULL 用 ``None`` 表示；所有聚合（COUNT(*) 除外）都忽略 NULL。
- NULL 语义与空表一致：对空集（空表或全 NULL 列）：
    - SUM / AVG / MIN / MAX -> None（SQL 的 NULL）
    - COUNT / COUNT DISTINCT -> 0
- AVG 的中间状态是 (sum, count) 二元组，**不能**对各分片均值再取平均；
  空分片贡献 (0, 0)，不进入分母。
- COUNT DISTINCT 需要额外状态（值集合），分片间合并为集合并集。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Iterable, Sequence

Row = dict[str, Any]
Table = list[Row]

COUNT_STAR = "count_star"
COUNT = "count"
SUM = "sum"
AVG = "avg"
MIN = "min"
MAX = "max"
COUNT_DISTINCT = "count_distinct"

_KINDS = {COUNT_STAR, COUNT, SUM, AVG, MIN, MAX, COUNT_DISTINCT}


@dataclass(frozen=True)
class AggSpec:
    """一个聚合运算的描述。column 为 None 时表示 COUNT(*)。"""

    kind: str
    column: str | None = None

    def __post_init__(self) -> None:
        if self.kind not in _KINDS:
            raise ValueError(f"未知聚合类型: {self.kind!r}")
        if self.kind == COUNT_STAR:
            if self.column is not None:
                raise ValueError("COUNT(*) 不接受列名")
        elif self.column is None:
            raise ValueError(f"{self.kind} 需要列名")


@dataclass
class Partial:
    """单个聚合在单个分片上的本地中间状态（可序列化、可合并）。"""

    spec: AggSpec
    count: int = 0            # 非 NULL 值个数（COUNT(*) 时为行数）
    total: Any = 0            # 非 NULL 值之和（SUM/AVG 用）
    has_value: bool = False   # 是否见过至少一个非 NULL 值
    extreme: Any = None       # MIN/MAX 当前极值
    distinct: set = field(default_factory=set)  # COUNT DISTINCT 的额外状态


def init_partial(spec: AggSpec) -> Partial:
    return Partial(spec=spec)


def update(partial: Partial, row: Row) -> None:
    """本地阶段：把一行折叠进中间状态。NULL 一律跳过（COUNT(*) 除外）。"""
    kind = partial.spec.kind
    if kind == COUNT_STAR:
        partial.count += 1
        return
    value = row.get(partial.spec.column)
    if value is None:
        return
    partial.has_value = True
    if kind == COUNT:
        partial.count += 1
    elif kind == SUM:
        partial.total += value
    elif kind == AVG:
        partial.total += value
        partial.count += 1
    elif kind == MIN:
        if partial.extreme is None or value < partial.extreme:
            partial.extreme = value
    elif kind == MAX:
        if partial.extreme is None or value > partial.extreme:
            partial.extreme = value
    elif kind == COUNT_DISTINCT:
        partial.distinct.add(value)


def merge(target: Partial, other: Partial) -> None:
    """全局阶段：把另一个分片的中间状态合并进 target。"""
    if target.spec != other.spec:
        raise ValueError(f"聚合描述不一致: {target.spec} vs {other.spec}")
    kind = target.spec.kind
    if kind in (COUNT_STAR, COUNT):
        target.count += other.count
    elif kind == SUM:
        target.total += other.total
    elif kind == AVG:
        # 关键：合并 (sum, count)，而不是对均值取平均。
        # 空分片 other.count == 0，自然不贡献分母。
        target.total += other.total
        target.count += other.count
    elif kind == MIN:
        if other.has_value and (
            not target.has_value or other.extreme < target.extreme
        ):
            target.extreme = other.extreme
    elif kind == MAX:
        if other.has_value and (
            not target.has_value or other.extreme > target.extreme
        ):
            target.extreme = other.extreme
    elif kind == COUNT_DISTINCT:
        target.distinct |= other.distinct
    target.has_value = target.has_value or other.has_value


def finalize(partial: Partial) -> Any:
    """全局阶段：把合并后的中间状态折算成最终结果。"""
    kind = partial.spec.kind
    if kind in (COUNT_STAR, COUNT):
        return partial.count
    if kind == SUM:
        # 全 NULL（或空表）的 SUM 返回 NULL，而不是 0。
        return partial.total if partial.has_value else None
    if kind == AVG:
        return partial.total / partial.count if partial.count else None
    if kind in (MIN, MAX):
        return partial.extreme if partial.has_value else None
    if kind == COUNT_DISTINCT:
        return len(partial.distinct)
    raise AssertionError(f"unreachable: {kind}")


def local_phase(shard: Table, specs: Sequence[AggSpec]) -> list[Partial]:
    """本地阶段：在单个分片上为每个聚合各算出一个中间状态。"""
    partials = [init_partial(spec) for spec in specs]
    for row in shard:
        for partial in partials:
            update(partial, row)
    return partials


def global_phase(
    per_shard_partials: Iterable[Sequence[Partial]], specs: Sequence[AggSpec]
) -> list[Any]:
    """全局阶段：合并所有分片的中间状态并折算出最终结果。"""
    merged = [init_partial(spec) for spec in specs]
    for shard_partials in per_shard_partials:
        if len(shard_partials) != len(merged):
            raise ValueError("分片中间状态数量与聚合描述不一致")
        for target, other in zip(merged, shard_partials):
            merge(target, other)
    return [finalize(partial) for partial in merged]


def distributed_aggregate(
    shards: Iterable[Table], specs: Sequence[AggSpec]
) -> list[Any]:
    """端到端：对每个分片跑本地阶段，再跑全局阶段。"""
    return global_phase((local_phase(shard, specs) for shard in shards), specs)

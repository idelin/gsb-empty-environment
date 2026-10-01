"""等价类指标：不同敏感值数（distinct）与熵（entropy）。

定义：
- 等价类：准标识符取值相同的一组记录，此处以其键 class_key 标识。
- 不同敏感值数：类内敏感值去重后的类别数（同值出现 10 次仍只算 1 种）。
- 熵：H = -sum(p_i * log_base(p_i))，p_i = count_i / n，对数底由配置指定。

仅处理合成数据；统计实验结果不构成形式隐私证明。
"""
from __future__ import annotations

import math
from collections import Counter
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Hashable, Iterable

from intervals import Decision, Interval, decide_at_least


class NullPolicy(Enum):
    """null 敏感值的显式处理策略。"""

    IGNORE = "ignore"  # null 不进入任何统计
    COUNT_AS_CATEGORY = "count_as_category"  # null 视为一个独立类别


@dataclass(frozen=True)
class Config:
    """评估参数。所有阈值均为“至少达到”语义。"""

    distinct_threshold: int = 1  # 不同敏感值数下限 L
    entropy_threshold: float = 0.0  # 熵下限 E
    log_base: float = 2.0  # 熵的对数底
    null_policy: NullPolicy = NullPolicy.IGNORE
    count_all_null_class: bool = True  # 全 null 类是否仍计入评估

    def __post_init__(self) -> None:
        if self.distinct_threshold < 1:
            raise ValueError("distinct_threshold 必须 >= 1")
        if self.entropy_threshold < 0.0:
            raise ValueError("entropy_threshold 必须 >= 0")
        if self.log_base <= 1.0:
            raise ValueError("log_base 必须 > 1")


@dataclass(frozen=True)
class Contribution:
    """单个敏感值对类内统计的贡献。"""

    value: Hashable
    count: int
    share: float  # p_i = count / n
    entropy_term: float  # -p_i * log_base(p_i)


class Status(Enum):
    PASS = "pass"
    FAIL = "fail"
    UNDECIDED = "undecided"  # 熵区间与阈值重叠，浮点精度无法判定
    SKIPPED = "skipped"  # 全 null 且配置为不计入评估


@dataclass
class ClassReport:
    class_key: Any
    n: int
    distinct: int
    entropy: float
    entropy_interval: Interval
    distinct_ok: bool
    entropy_decision: Decision
    status: Status
    failed_metrics: tuple[str, ...]
    contributions: dict[Hashable, Contribution]


def entropy_of_counts(counts: dict[Hashable, int], base: float) -> float:
    """普通浮点熵（用于报告展示）。"""
    n = sum(c for c in counts.values() if c > 0)
    if n == 0:
        return 0.0
    inv_log_base = 1.0 / math.log(base)
    h = 0.0
    for c in counts.values():
        if c <= 0:
            continue
        p = c / n
        h -= p * math.log(p) * inv_log_base
    return h


def entropy_interval_of_counts(counts: dict[Hashable, int], base: float) -> Interval:
    """熵的误差区间：逐项向外舍入，误差界随项数可加累积。"""
    n = sum(c for c in counts.values() if c > 0)
    if n == 0:
        return Interval.point(0.0)
    lb = math.log(base)
    log_base = Interval(
        math.nextafter(math.nextafter(lb, -math.inf), -math.inf),
        math.nextafter(math.nextafter(lb, math.inf), math.inf),
    )
    total = Interval.point(0.0)
    for c in counts.values():
        if c <= 0:
            continue
        p = Interval.point(float(c)) / Interval.point(float(n))
        term = (p * p.log()) / log_base  # p*log(p) <= 0
        total = total - term
    # 熵在数学上非负，与 [0, +inf) 求交是安全的收紧
    return Interval(max(total.lo, 0.0), total.hi)


class ClassStats:
    """单个等价类的增量统计。

    add/delete 只更新计数器，不重扫原始记录；verify() 用原始记录
    独立重算以校验增量维护的正确性。
    """

    def __init__(self, config: Config, class_key: Any = None):
        self.config = config
        self.class_key = class_key
        self.counts: Counter = Counter()
        self.n = 0
        self._undo_log: list[dict] = []

    def _accepted(self, value: Hashable) -> bool:
        if value is None and self.config.null_policy is NullPolicy.IGNORE:
            return False
        return True

    def add(self, value: Hashable) -> bool:
        if not self._accepted(value):
            return False
        self.counts[value] += 1
        self.n += 1
        return True

    def delete(self, value: Hashable) -> bool:
        """增量删除一条记录并更新统计。"""
        if not self._accepted(value):
            return False
        if self.counts.get(value, 0) <= 0:
            raise KeyError(f"值 {value!r} 不存在，无法删除")
        self.counts[value] -= 1
        if self.counts[value] == 0:
            del self.counts[value]
        self.n -= 1
        return True

    def distinct_count(self) -> int:
        return len(self.counts)

    def entropy(self) -> float:
        return entropy_of_counts(self.counts, self.config.log_base)

    def entropy_interval(self) -> Interval:
        return entropy_interval_of_counts(self.counts, self.config.log_base)

    def contributions(self) -> dict[Hashable, Contribution]:
        if self.n == 0:
            return {}
        inv_log_base = 1.0 / math.log(self.config.log_base)
        out = {}
        for value, count in self.counts.items():
            p = count / self.n
            out[value] = Contribution(
                value=value,
                count=count,
                share=p,
                entropy_term=-p * math.log(p) * inv_log_base + 0.0,
            )
        return out

    def merge_shard(self, other: "ClassStats") -> None:
        """合并另一个分片的统计（计数可加）。"""
        if self.config != other.config:
            raise ValueError("分片配置不一致，无法合并")
        for value, count in other.counts.items():
            self.counts[value] += count
        self.n += other.n

    def merge_values(self, mapping: dict[Hashable, Hashable]) -> bool:
        """按层级映射合并敏感值，保留原贡献到撤销日志以支持撤回。"""
        removed: dict[Hashable, int] = {}
        added: Counter = Counter()
        for value, parent in mapping.items():
            if value is None or value == parent:
                continue
            count = self.counts.get(value, 0)
            if count:
                removed[value] = count
                added[parent] += count
        if not removed:
            return False
        entry = {
            "removed": removed,
            "added": dict(added),
            "contributions_before": self.contributions(),
        }
        for value in removed:
            del self.counts[value]
        for parent, count in added.items():
            self.counts[parent] += count
        self._undo_log.append(entry)
        return True

    def rollback(self) -> dict[Hashable, Contribution]:
        """撤回最近一次 merge_values，返回恢复后的原贡献快照。"""
        entry = self._undo_log.pop()
        for parent, count in entry["added"].items():
            new_count = self.counts[parent] - count
            if new_count:
                self.counts[parent] = new_count
            else:
                del self.counts[parent]
        for value, count in entry["removed"].items():
            self.counts[value] += count
        return entry["contributions_before"]

    def evaluate(self) -> ClassReport:
        distinct = self.distinct_count()
        interval = self.entropy_interval()
        contributions = self.contributions()
        if self.n == 0 and not self.config.count_all_null_class:
            return ClassReport(
                class_key=self.class_key,
                n=0,
                distinct=distinct,
                entropy=0.0,
                entropy_interval=interval,
                distinct_ok=True,
                entropy_decision=Decision.SATISFIED,
                status=Status.SKIPPED,
                failed_metrics=(),
                contributions=contributions,
            )
        distinct_ok = distinct >= self.config.distinct_threshold
        decision = decide_at_least(interval, self.config.entropy_threshold)
        failed = []
        if not distinct_ok:
            failed.append("distinct")
        if decision is Decision.VIOLATED:
            failed.append("entropy")
        if failed:
            status = Status.FAIL
        elif decision is Decision.UNDECIDED:
            status = Status.UNDECIDED
        else:
            status = Status.PASS
        return ClassReport(
            class_key=self.class_key,
            n=self.n,
            distinct=distinct,
            entropy=self.entropy(),
            entropy_interval=interval,
            distinct_ok=distinct_ok,
            entropy_decision=decision,
            status=status,
            failed_metrics=tuple(failed),
            contributions=contributions,
        )

    def verify(self, records: Iterable[Hashable], tol: float = 1e-9) -> bool:
        """用原始记录独立重算并与当前增量统计比对。"""
        fresh = recompute_stats(records, self.config, self.class_key)
        if dict(self.counts) != dict(fresh.counts):
            return False
        return abs(self.entropy() - fresh.entropy()) <= tol


def recompute_stats(
    records: Iterable[Hashable], config: Config, class_key: Any = None
) -> ClassStats:
    """从原始记录独立重算一个类的统计（验证用基准）。"""
    stats = ClassStats(config, class_key)
    for value in records:
        stats.add(value)
    return stats

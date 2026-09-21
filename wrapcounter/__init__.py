"""乱序环绕计数器分析库。

核心能力：

- 按 (时间, seq) 排序计算相邻采样的确定增量，正确识别 8 位等计数器环绕（250 -> 3）。
- 支持显式 reset 标记；跨 reset 的区间增量不可知，绝不伪造增长。
- 每个来源可配置位宽与“最大可判定采样间隔”，超过间隔标不确定。
- 支持迟到插入（只修订相邻差分）与撤回（作废相邻区间），输出作废/替换修订流。
- 提供全量排序重算参考实现，可与增量修订序列逐步对照。
- 所有结果保留判断所使用的采样 ID 与配置版本。
"""

from .model import (
    Sample,
    SourceConfig,
    Status,
    Reason,
    IntervalResult,
    SourceTotal,
    Revision,
    RevisionOp,
    DuplicatePolicy,
)
from .judge import judge_interval, modulus
from .reference import recompute_source, recompute_all
from .engine import CounterAnalyzer

__all__ = [
    "Sample",
    "SourceConfig",
    "Status",
    "Reason",
    "IntervalResult",
    "SourceTotal",
    "Revision",
    "RevisionOp",
    "DuplicatePolicy",
    "judge_interval",
    "modulus",
    "recompute_source",
    "recompute_all",
    "CounterAnalyzer",
]

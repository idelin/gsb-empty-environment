"""数据模型：采样、来源配置、区间结果、修订事件。"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Optional


class Status(str, Enum):
    """相邻区间状态。"""

    DEFINITE = "definite"      # 增量可唯一确定
    UNCERTAIN = "uncertain"    # 增量不可唯一确定，不计入确定总数


class Reason(str, Enum):
    """判定理由（含不确定原因）。"""

    DIRECT = "direct"                  # 单调递增，无环绕
    WRAP = "wrap"                      # 一次环绕，增量 = M - prev + curr
    ZERO_GROWTH = "zero_growth"        # 读数相同且无 reset：确定增量 0
    RESET_CROSSING = "reset_crossing"  # 区间跨越显式 reset
    GAP_TOO_LARGE = "gap_too_large"    # 时间间隔超过最大可判定采样间隔
    EQUAL_AFTER_RESET = "equal_after_reset"  # reset 边界读数与前值相同：0/一圈/M 不可区分
    SOLE_SAMPLE = "sole_sample"        # 来源仅一个采样，无前序差分


class RevisionOp(str, Enum):
    """增量修订操作类型。"""

    UPSERT = "upsert"  # 新区间产出或替换既有区间
    REVOKE = "revoke"  # 区间作废（撤回采样 / 配置更新）


class DuplicatePolicy(str, Enum):
    """同 source 下 (event_time, seq) 重复时的策略。"""

    ERROR = "error"    # 抛 ValueError
    REPLACE = "replace"  # 用新采样覆盖同键旧采样（按 sample_id 区分）
    IGNORE = "ignore"  # 保留先到采样


@dataclass(frozen=True)
class SourceConfig:
    """一个来源的计数器配置。

    width_bits: 计数器位宽（1..64），模数 M = 2**width_bits。
    max_gap_seconds: 最大可判定采样间隔（>0）。合同：只有间隔不超过该值时，
        才允许按“真实增量 < M（至多 0 次完整环绕）”判定；超过则不确定。
    config_version: 配置版本，随每次配置更新而变化，结果会回传所用版本。
    """

    source: str
    width_bits: int = 8
    max_gap_seconds: float = 30.0
    config_version: int = 1

    def __post_init__(self) -> None:
        if not isinstance(self.source, str) or not self.source:
            raise ValueError("source 必须是非空字符串")
        if not 1 <= self.width_bits <= 64:
            raise ValueError("width_bits 必须在 1..64 之间")
        if self.max_gap_seconds <= 0:
            raise ValueError("max_gap_seconds 必须为正数")
        if self.config_version < 1:
            raise ValueError("config_version 必须 >= 1")


@dataclass(frozen=True)
class Sample:
    """一次计数器采样。

    sample_id: 全局唯一采样 ID，所有结果都携带它以保证可追溯。
    source: 来源标识。
    value: 原始计数读数 [0, M)。
    event_time: 事件时间（秒，数值型；建议单调时钟）。
    seq: 同一时间戳的决胜序号，小者在前。
    reset: 该采样是否为显式设备重置点（重置后首个读数）。
    ingest_order: 到达顺序，仅用于调试与排序参考，不参与时序排序。
    """

    sample_id: str
    source: str
    value: int
    event_time: float
    seq: int = 0
    reset: bool = False
    ingest_order: int = 0

    def __post_init__(self) -> None:
        if not isinstance(self.sample_id, str) or not self.sample_id:
            raise ValueError("sample_id 必须是非空字符串")
        if not isinstance(self.source, str) or not self.source:
            raise ValueError("source 必须是非空字符串")
        if not isinstance(self.value, int) or self.value < 0:
            raise ValueError("value 必须是非负整数")
        if not isinstance(self.seq, int) or self.seq < 0:
            raise ValueError("seq 必须是非负整数")


@dataclass(frozen=True)
class IntervalResult:
    """相邻两个采样之间的判定结果。"""

    interval_id: str
    source: str
    prev_sample_id: Optional[str]
    curr_sample_id: str
    prev_value: Optional[int]
    curr_value: int
    dt_seconds: float
    status: Status
    reason: Reason
    delta: Optional[int]                 # DEFINITE 时为确定增量；UNCERTAIN 时为 None
    wraps: Optional[int]                 # 判定到的环绕次数（0/1）；不确定为 None
    reset_boundary: bool                 # 右端采样是否带 reset 标记
    config_version: int                  # 判定时使用的配置版本
    rule: str                            # 人类可读的判定规则说明


@dataclass(frozen=True)
class SourceTotal:
    """一个来源的可确定总量聚合。"""

    source: str
    definite_delta: int                  # 确定区间增量之和
    complete: bool                       # 是否所有相邻区间都确定（无缺失/重置洞）
    uncertain_interval_ids: tuple[str, ...]
    ordered_sample_ids: tuple[str, ...]  # 按 (event_time, seq) 全序
    interval_ids: tuple[str, ...]        # 与全序对齐的区间
    sample_count: int
    config_version: int


@dataclass(frozen=True)
class Revision:
    """一条增量修订：迟到插入/撤回只影响相邻差分。

    UPSERT: 产出（或替换）一个区间；replaced_interval_id 非空时说明
        本次修订使哪个旧区间结果作废并被新区间集合替代。
    REVOKE: 区间作废（撤回采样或配置更新后重算）。
    """

    revision_id: int
    op: RevisionOp
    source: str
    interval: Optional[IntervalResult]
    interval_id: Optional[str]
    replaced_interval_id: Optional[str]
    reason: str
    config_version: int
    sample_id: Optional[str] = None


def build_source_total(
    source: str,
    samples_in_order: list[Sample],
    intervals: list[IntervalResult],
    config: SourceConfig,
) -> SourceTotal:
    """根据全序采样与对齐区间聚合可确定总量。

    区间列表与“相邻对”对齐：第 i 个区间以第 i+1 个采样为右端。
    任一区间不确定则 complete=False；确定总数只累加确定段，
    不会因为丢弃不确定段而伪造成完整总数。
    """

    definite = 0
    uncertain_ids: list[str] = []
    for iv in intervals:
        if iv.status is Status.DEFINITE and iv.delta is not None:
            definite += iv.delta
        else:
            uncertain_ids.append(iv.interval_id)
    return SourceTotal(
        source=source,
        definite_delta=definite,
        complete=not uncertain_ids and len(samples_in_order) >= 1,
        uncertain_interval_ids=tuple(uncertain_ids),
        ordered_sample_ids=tuple(s.sample_id for s in samples_in_order),
        interval_ids=tuple(iv.interval_id for iv in intervals),
        sample_count=len(samples_in_order),
        config_version=config.config_version,
    )

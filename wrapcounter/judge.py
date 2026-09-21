"""相邻采样差分判定。

判定合同（与来源配置绑定，结果回传 config_version）：

设模数 M = 2**width_bits，区间时间长度 dt = t_curr - t_prev。

1. 读数必须落在 [0, M)，否则 ValueError。
2. 右端采样带 reset 标记：设备在此区间内重置。重置前的真实增量与重置前
   读数都不可知，因此该区间恒为 UNCERTAIN / RESET_CROSSING
   （首尾读数恰好相等时细化为 EQUAL_AFTER_RESET：0、一整圈 M、重置后相等
    三种解释不可区分）。
3. dt > max_gap_seconds：不满足“该间隔最多一次环绕”所需的采样密度，
   无法排除中间多次环绕，UNCERTAIN / GAP_TOO_LARGE。
4. dt <= max_gap_seconds 且无 reset：合同保证真实增量 0 <= delta < M
   （不足一整圈），故读数差可唯一反解：
     - curr > prev：DIRECT，delta = curr - prev，wraps = 0
     - curr < prev：WRAP，  delta = M - prev + curr，wraps = 1
     - curr == prev：ZERO_GROWTH，delta = 0，wraps = 0
   注意 250 -> 3（M=256）确定为 wrap，delta = 9，而不是“掉了 247”。
"""

from __future__ import annotations

from typing import Optional

from .model import IntervalResult, Reason, Sample, SourceConfig, Status


def modulus(config: SourceConfig) -> int:
    """计数器模数 M = 2**width_bits。"""
    return 1 << config.width_bits


def _rule_text(reason: Reason) -> str:
    return {
        Reason.DIRECT: "读数上升且间隔内不足一圈：delta = curr - prev",
        Reason.WRAP: "读数回绕且间隔内不足一圈：delta = M - prev + curr（250->3 => 9）",
        Reason.ZERO_GROWTH: "读数相同且无 reset：合同下增量唯一为 0",
        Reason.RESET_CROSSING: "区间跨越显式 reset：重置前增量不可知",
        Reason.GAP_TOO_LARGE: "采样间隔超过最大可判定间隔：无法排除多次环绕",
        Reason.EQUAL_AFTER_RESET: "reset 边界读数相同：0 / 一整圈 M / 重置相等 不可区分",
        Reason.SOLE_SAMPLE: "首个采样无前序差分",
    }[reason]


def make_interval_id(prev: Optional[Sample], curr: Sample) -> str:
    """生成区间 ID：以前后采样 ID 命名，便于修订追踪。"""
    prev_id = prev.sample_id if prev is not None else "ROOT"
    return f"{curr.source}:{prev_id}->{curr.sample_id}"


def judge_interval(
    prev: Optional[Sample],
    curr: Sample,
    config: SourceConfig,
) -> IntervalResult:
    """判定单个相邻区间。prev 为 None 表示该来源的首个采样（只有锚点）。"""

    mod = modulus(config)
    for s in (prev, curr):
        if s is not None and not (0 <= s.value < mod):
            raise ValueError(
                f"采样 {s.sample_id} 的 value={s.value} 超出 {config.width_bits} 位范围 [0,{mod})"
            )
    if prev is not None and prev.source != curr.source:
        raise ValueError("不能跨 source 判定区间")
    if prev is not None and (curr.event_time, curr.seq) < (prev.event_time, prev.seq):
        raise ValueError("相邻采样必须按 (event_time, seq) 非降序排列")

    dt = 0.0 if prev is None else curr.event_time - prev.event_time

    if prev is None:
        return IntervalResult(
            interval_id=make_interval_id(None, curr),
            source=curr.source,
            prev_sample_id=None,
            curr_sample_id=curr.sample_id,
            prev_value=None,
            curr_value=curr.value,
            dt_seconds=0.0,
            status=Status.UNCERTAIN,
            reason=Reason.SOLE_SAMPLE,
            delta=None,
            wraps=None,
            reset_boundary=curr.reset,
            config_version=config.config_version,
            rule=_rule_text(Reason.SOLE_SAMPLE),
        )

    reset_boundary = bool(curr.reset)
    if reset_boundary:
        if curr.value == prev.value:
            reason = Reason.EQUAL_AFTER_RESET
        else:
            reason = Reason.RESET_CROSSING
        return IntervalResult(
            interval_id=make_interval_id(prev, curr),
            source=curr.source,
            prev_sample_id=prev.sample_id,
            curr_sample_id=curr.sample_id,
            prev_value=prev.value,
            curr_value=curr.value,
            dt_seconds=dt,
            status=Status.UNCERTAIN,
            reason=reason,
            delta=None,
            wraps=None,
            reset_boundary=True,
            config_version=config.config_version,
            rule=_rule_text(reason),
        )

    if dt > config.max_gap_seconds:
        return IntervalResult(
            interval_id=make_interval_id(prev, curr),
            source=curr.source,
            prev_sample_id=prev.sample_id,
            curr_sample_id=curr.sample_id,
            prev_value=prev.value,
            curr_value=curr.value,
            dt_seconds=dt,
            status=Status.UNCERTAIN,
            reason=Reason.GAP_TOO_LARGE,
            delta=None,
            wraps=None,
            reset_boundary=False,
            config_version=config.config_version,
            rule=_rule_text(Reason.GAP_TOO_LARGE),
        )

    if curr.value > prev.value:
        reason, delta, wraps = Reason.DIRECT, curr.value - prev.value, 0
    elif curr.value < prev.value:
        reason, delta, wraps = Reason.WRAP, mod - prev.value + curr.value, 1
    else:
        reason, delta, wraps = Reason.ZERO_GROWTH, 0, 0

    return IntervalResult(
        interval_id=make_interval_id(prev, curr),
        source=curr.source,
        prev_sample_id=prev.sample_id,
        curr_sample_id=curr.sample_id,
        prev_value=prev.value,
        curr_value=curr.value,
        dt_seconds=dt,
        status=Status.DEFINITE,
        reason=reason,
        delta=delta,
        wraps=wraps,
        reset_boundary=False,
        config_version=config.config_version,
        rule=_rule_text(reason),
    )

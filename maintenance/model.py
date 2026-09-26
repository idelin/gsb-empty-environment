"""数据模型：合成设备维修选择问题。

题内约定（合成数据，仅用于演示算法）：
- 时间窗口由若干“班次”(shift)组成，班次之间为不可用间隔；
  只有班次长度（整数槽数）参与约束。
- 每台设备（作业）属于一个停机组；同组在任一槽内同时维修数不超过该组上限。
  由于班次内各槽等价，约束以“组 x 班次”的聚合形式表达：
  组 g 在班次 k 的负载 <= limit_g * len_k；单个作业在同一班次内的
  工作量不超过 len_k（一台设备同一时刻只能由一个工位处理）。
- 作业可跨班次中断继续；每多占用一个班次计一次额外启动成本
  （中断恢复成本）。班次内不另计中断。
- 风险分数仅作为题内权重（收益），不解释为真实失效概率。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Union

Rational = Union[int, Fraction]


def to_rational(value: Rational) -> Fraction:
    """把 int/Fraction 归一为 Fraction，保证精确有理数运算。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):  # bool 是 int 子类，拒绝静默接受
        raise TypeError("score/startup 不接受 bool")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"不支持的有理数类型: {type(value)!r}")


@dataclass(frozen=True)
class Shift:
    """一个可用班次：连续 length 个整数槽。"""

    name: str
    length: int


@dataclass(frozen=True)
class Group:
    """停机组：limit 为同组同时维修数上限（工位数）。"""

    name: str
    limit: int


@dataclass(frozen=True)
class Job:
    """维修作业（合成设备）。

    duration: 维修工时（整数槽，>= 1）
    score:    失效风险分数（题内权重，非负有理数）
    startup:  每次（重新）启动成本（非负有理数）
    """

    job_id: str
    group: str
    duration: int
    score: Fraction
    startup: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "score", to_rational(self.score))
        object.__setattr__(self, "startup", to_rational(self.startup))


@dataclass(frozen=True)
class Instance:
    shifts: tuple[Shift, ...]
    groups: tuple[Group, ...]
    jobs: tuple[Job, ...]


def validate(instance: Instance) -> None:
    """校验合成约束数据；不合法时抛 ValueError。"""
    if not instance.shifts:
        raise ValueError("至少需要一个班次")
    seen = set()
    for shift in instance.shifts:
        if shift.name in seen:
            raise ValueError(f"班次名重复: {shift.name}")
        seen.add(shift.name)
        if not isinstance(shift.length, int) or shift.length < 1:
            raise ValueError(f"班次 {shift.name} 长度必须为正整数")

    group_names = set()
    for group in instance.groups:
        if group.name in group_names:
            raise ValueError(f"停机组名重复: {group.name}")
        group_names.add(group.name)
        if not isinstance(group.limit, int) or group.limit < 1:
            raise ValueError(f"停机组 {group.name} 上限必须为正整数")

    job_ids = set()
    for job in instance.jobs:
        if job.job_id in job_ids:
            raise ValueError(f"作业 id 重复: {job.job_id}")
        job_ids.add(job.job_id)
        if job.group not in group_names:
            raise ValueError(f"作业 {job.job_id} 引用未知停机组 {job.group}")
        if not isinstance(job.duration, int) or job.duration < 1:
            raise ValueError(f"作业 {job.job_id} 工时必须为正整数")
        if job.score < 0:
            raise ValueError(f"作业 {job.job_id} 风险分数不能为负")
        if job.startup < 0:
            raise ValueError(f"作业 {job.job_id} 启动成本不能为负")

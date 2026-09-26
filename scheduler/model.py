"""单机调度数据模型：任务、类型切换耗时矩阵、排程结果。

所有时间量一律用 fractions.Fraction 精确表示，
可接受 int / float / str / Fraction 输入（如 "3/2"、"1.5"）。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Hashable, Mapping, Optional, Tuple


def to_fraction(value) -> Fraction:
    """把 int / float / str / Fraction 统一转成 Fraction（精确）。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool 不是合法的时间量")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        return Fraction(str(value))
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"无法转换为 Fraction: {value!r}")


class SetupMatrix:
    """类型切换耗时矩阵（序列相关）。

    同类型之间切换耗时恒为 0；不同类型之间按矩阵取值，
    未显式给出的有序对取 default。
    """

    def __init__(
        self,
        times: Optional[Mapping[Tuple[Hashable, Hashable], object]] = None,
        default: object = 0,
    ):
        self._default = to_fraction(default)
        self._times = {}
        for (a, b), v in (times or {}).items():
            self._times[(a, b)] = to_fraction(v)

    @classmethod
    def uniform(cls, types, setup) -> "SetupMatrix":
        """所有不同类型之间的切换耗时都相同。"""
        value = to_fraction(setup)
        return cls({(a, b): value for a in types for b in types if a != b})

    @classmethod
    def symmetric(cls, pairs, default: object = 0) -> "SetupMatrix":
        """由无序对构造对称矩阵。"""
        times = {}
        for (a, b), v in pairs.items():
            times[(a, b)] = v
            times[(b, a)] = v
        return cls(times, default=default)

    def between(self, prev_type: Hashable, next_type: Hashable) -> Fraction:
        if prev_type == next_type:
            return Fraction(0)
        return self._times.get((prev_type, next_type), self._default)


@dataclass(frozen=True)
class Job:
    """任务：释放时间 release、加工时长 processing、截止时间 due（用于迟延）、类型 job_type。"""

    job_id: Hashable
    job_type: Hashable
    release: Fraction = Fraction(0)
    processing: Fraction = Fraction(0)
    due: Fraction = Fraction(0)

    def __init__(self, job_id, job_type, release=0, processing=0, due=0):
        p = to_fraction(processing)
        if p < 0:
            raise ValueError("processing 不能为负")
        object.__setattr__(self, "job_id", job_id)
        object.__setattr__(self, "job_type", job_type)
        object.__setattr__(self, "release", to_fraction(release))
        object.__setattr__(self, "processing", p)
        object.__setattr__(self, "due", to_fraction(due))


@dataclass(frozen=True)
class ScheduledJob:
    """排程中的一个已排任务。"""

    job: Job
    setup: Fraction        # 与前一任务之间的切换耗时
    idle: Fraction         # 开工前的空闲（等待释放）
    start: Fraction
    completion: Fraction

    @property
    def tardiness(self) -> Fraction:
        late = self.completion - self.job.due
        return late if late > 0 else Fraction(0)


@dataclass(frozen=True)
class Schedule:
    """完整排程：按执行顺序排列的 ScheduledJob 元组。"""

    entries: Tuple[ScheduledJob, ...]

    @property
    def total_tardiness(self) -> Fraction:
        return sum((e.tardiness for e in self.entries), Fraction(0))

    @property
    def makespan(self) -> Fraction:
        return self.entries[-1].completion if self.entries else Fraction(0)

    @property
    def order(self) -> Tuple[Hashable, ...]:
        return tuple(e.job.job_id for e in self.entries)

    def violations(self, setups: SetupMatrix) -> list:
        """校验释放时间与切换耗时约束，返回违例描述列表（空列表表示可行）。"""
        problems = []
        t = Fraction(0)
        prev_type = None
        for e in self.entries:
            want_setup = Fraction(0) if prev_type is None else setups.between(prev_type, e.job.job_type)
            if e.setup != want_setup:
                problems.append(f"{e.job.job_id}: 切换耗时 {e.setup} != {want_setup}")
            earliest = t + want_setup
            if earliest < e.job.release:
                earliest = e.job.release
            if e.start != earliest:
                problems.append(f"{e.job.job_id}: 开工 {e.start} 早于/晚于最早可开工 {earliest}")
            if e.completion != e.start + e.job.processing:
                problems.append(f"{e.job.job_id}: 完工时间不一致")
            t = e.completion
            prev_type = e.job.job_type
        return problems


def build_schedule(jobs_in_order, setups: SetupMatrix) -> Schedule:
    """按给定次序构造最早开工（半主动）排程。

    机器空闲但次序中的下一任务尚未释放时，插入被迫空闲；
    给定次序下不存在更优的主动空闲（总迟延是正则目标）。
    """
    entries = []
    t = Fraction(0)
    prev_type = None
    for job in jobs_in_order:
        setup = Fraction(0) if prev_type is None else setups.between(prev_type, job.job_type)
        ready = t + setup
        start = ready if ready > job.release else job.release
        idle = start - ready
        completion = start + job.processing
        entries.append(ScheduledJob(job=job, setup=setup, idle=idle, start=start, completion=completion))
        t = completion
        prev_type = job.job_type
    return Schedule(tuple(entries))

"""计算作业分批调度：按相容类型分批，批固定启动成本与资源上限，最小化总成本。

模型假设
--------
- 单机顺序处理各个批；批的处理时间 = 批内各作业处理时间之和（串行批）。
- 批内所有作业在该批完成时刻同时完成，完成时刻不得超过各自截止时间。
- 每个批有固定启动成本 startup_cost（按批计，不按作业计）与资源容量 capacity。
- 只有相同类型（相容）的作业才能同批；不相容作业同批属于硬约束违反。
- 作业不可拆分，必须完整放入唯一一个批。
- 所有数值用 fractions.Fraction 精确表示（支持合成整数与有理数）。

求解方法
--------
精确子集 DP（不调用任何外部求解器）：dp[S] 表示恰好调度集合 S 所需的最少批数，
枚举 S 的最后一个批 B（要求类型纯、资源和不超容量、批内作业截止时刻 >= S 的处理
时间总和）。该 DP 隐式枚举了所有划分与所有批顺序，因此结果是可证最优的。
当状态数 / 转移数超过预算时，回退到 EDF 贪心构造可行解，并按
OPTIMAL / FEASIBLE / INFEASIBLE / UNDECIDED 四种状态分别报告。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Optional, Sequence


class Status(Enum):
    """求解状态：最优 / 可行但未证最优 / 已证不可行 / 预算耗尽未决。"""

    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    INFEASIBLE = "infeasible"
    UNDECIDED = "undecided"


@dataclass(frozen=True)
class Job:
    """单个计算作业（不可拆分）。"""

    job_id: str
    job_type: str
    resource: Fraction
    proc_time: Fraction
    deadline: Fraction

    def __init__(self, job_id, job_type, resource, proc_time, deadline):
        object.__setattr__(self, "job_id", str(job_id))
        object.__setattr__(self, "job_type", str(job_type))
        object.__setattr__(self, "resource", Fraction(resource))
        object.__setattr__(self, "proc_time", Fraction(proc_time))
        object.__setattr__(self, "deadline", Fraction(deadline))


@dataclass(frozen=True)
class Instance:
    """问题实例：作业集合 + 每批资源上限 + 每批固定启动成本。"""

    jobs: tuple
    capacity: Fraction
    startup_cost: Fraction

    def __init__(self, jobs, capacity, startup_cost):
        object.__setattr__(self, "jobs", tuple(jobs))
        object.__setattr__(self, "capacity", Fraction(capacity))
        object.__setattr__(self, "startup_cost", Fraction(startup_cost))


@dataclass
class Solution:
    """求解结果。batches 为按调度顺序排列的批，每批是作业下标列表。"""

    status: Status
    batches: list
    total_cost: Optional[Fraction]
    message: str
    transitions_used: int = 0

    @property
    def num_batches(self) -> int:
        return len(self.batches)


class _BudgetExceeded(Exception):
    """内部异常：状态/转移预算耗尽。"""


def solve(
    instance: Instance,
    *,
    state_budget: int = 1 << 20,
    transition_budget: int = 5_000_000,
) -> Solution:
    """求解分批问题，返回 Solution（含四种状态之一）。

    state_budget: 精确 DP 允许的最大状态数（2^n 上限）。
    transition_budget: 精确 DP 允许的最大转移（松弛）次数。
    """
    jobs = instance.jobs
    n = len(jobs)

    oversized = [j.job_id for j in jobs if j.resource > instance.capacity]
    if oversized:
        return Solution(
            Status.INFEASIBLE,
            [],
            None,
            f"拒绝：作业 {oversized} 单作业资源超过批容量 {instance.capacity}",
        )
    negative = [j.job_id for j in jobs if j.resource < 0 or j.proc_time < 0]
    if negative:
        return Solution(
            Status.INFEASIBLE, [], None, f"拒绝：作业 {negative} 含负资源或负处理时间"
        )
    if n == 0:
        return Solution(Status.OPTIMAL, [], Fraction(0), "空实例：0 个批，成本 0")

    try:
        batches, transitions = _exact_dp(instance, state_budget, transition_budget)
    except _BudgetExceeded as exc:
        greedy = _greedy_edf(instance)
        if greedy is not None:
            cost = instance.startup_cost * len(greedy)
            return Solution(
                Status.FEASIBLE,
                greedy,
                cost,
                "预算耗尽：返回贪心可行解，未证明最优",
                exc.args[0],
            )
        return Solution(
            Status.UNDECIDED,
            [],
            None,
            "预算耗尽：贪心未找到可行解，可行性未决",
            exc.args[0],
        )

    if batches is None:
        return Solution(
            Status.INFEASIBLE,
            [],
            None,
            "已证不可行：不存在满足全部截止约束的分批方案",
            transitions,
        )
    cost = instance.startup_cost * len(batches)
    return Solution(Status.OPTIMAL, batches, cost, "已证最优", transitions)


def _exact_dp(instance: Instance, state_budget: int, transition_budget: int):
    """子集 DP。返回 (按调度顺序的批列表, 转移次数)；不可行时批列表为 None。"""
    jobs = instance.jobs
    n = len(jobs)
    size = 1 << n
    if size > state_budget:
        raise _BudgetExceeded(0)

    cap = instance.capacity
    res_sum = [Fraction(0)] * size   # 子集资源之和
    proc_sum = [Fraction(0)] * size  # 子集处理时间之和
    min_dl = [None] * size           # 子集内最小截止时刻
    valid = [False] * size           # 子集是否可作为合法批
    type_of = [None] * size          # 子集类型标签；混合类型为 MIXED
    MIXED = object()
    for s in range(1, size):
        lb = s & -s
        i = lb.bit_length() - 1
        prev = s ^ lb
        j = jobs[i]
        res_sum[s] = res_sum[prev] + j.resource
        proc_sum[s] = proc_sum[prev] + j.proc_time
        min_dl[s] = j.deadline if prev == 0 else min(min_dl[prev], j.deadline)
        t = j.job_type
        type_of[s] = t if prev == 0 else (type_of[prev] if type_of[prev] == t else MIXED)
        valid[s] = type_of[s] is not MIXED and res_sum[s] <= cap

    INF = n + 1
    dp = [INF] * size
    dp[0] = 0
    parent = [0] * size
    transitions = 0
    for s in range(1, size):
        finish = proc_sum[s]  # 最后一个批的完成时刻（只与集合有关，与顺序无关）
        best = INF
        best_b = 0
        b = s
        while b:
            transitions += 1
            if transitions > transition_budget:
                raise _BudgetExceeded(transitions)
            if valid[b] and min_dl[b] >= finish:
                cand = dp[s ^ b] + 1
                if cand < best:
                    best = cand
                    best_b = b
            b = (b - 1) & s
        dp[s] = best
        parent[s] = best_b

    if dp[size - 1] == INF:
        return None, transitions
    batches = []
    s = size - 1
    while s:
        b = parent[s]
        batches.append([i for i in range(n) if (b >> i) & 1])
        s ^= b
    batches.reverse()  # 还原为调度先后顺序
    return batches, transitions


def _greedy_edf(instance: Instance):
    """EDF 贪心：类型内按截止时刻排序后依次装批，批间按批内最早截止排序。

    返回可行批列表；若构造结果违反截止约束则返回 None。
    """
    jobs = instance.jobs
    by_type: dict = {}
    for i, j in enumerate(jobs):
        by_type.setdefault(j.job_type, []).append(i)

    batches = []  # (作业下标列表, 批内最早截止, 批处理时间)
    for idxs in by_type.values():
        idxs.sort(key=lambda i: (jobs[i].deadline, -jobs[i].resource))
        cur: list = []
        cur_res = Fraction(0)
        cur_proc = Fraction(0)
        cur_dl: Optional[Fraction] = None
        for i in idxs:
            j = jobs[i]
            if cur and cur_res + j.resource > instance.capacity:
                batches.append((cur, cur_dl, cur_proc))
                cur, cur_res, cur_proc, cur_dl = [], Fraction(0), Fraction(0), None
            cur.append(i)
            cur_res += j.resource
            cur_proc += j.proc_time
            cur_dl = j.deadline if cur_dl is None else min(cur_dl, j.deadline)
        if cur:
            batches.append((cur, cur_dl, cur_proc))

    batches.sort(key=lambda item: item[1])
    clock = Fraction(0)
    for _, dl, proc in batches:
        clock += proc
        if clock > dl:
            return None
    return [list(idxs) for idxs, _, _ in batches]


def verify(instance: Instance, batches: Sequence[Sequence[int]]):
    """校验一个分批方案。返回 (是否可行, 错误信息列表)。"""
    errors: list = []
    n = len(instance.jobs)
    seen: list = []
    for b in batches:
        if not b:
            errors.append("存在空批")
            continue
        types = {instance.jobs[i].job_type for i in b}
        if len(types) > 1:
            errors.append(f"批 {list(b)} 混装了不相容类型 {sorted(types)}")
        load = sum((instance.jobs[i].resource for i in b), Fraction(0))
        if load > instance.capacity:
            errors.append(f"批 {list(b)} 资源 {load} 超容量 {instance.capacity}")
        seen.extend(b)
    if sorted(seen) != list(range(n)):
        errors.append("各批未构成对作业的划分（存在遗漏或重复，作业不可拆分）")

    clock = Fraction(0)
    for b in batches:
        clock += sum((instance.jobs[i].proc_time for i in b), Fraction(0))
        for i in b:
            if instance.jobs[i].deadline < clock:
                errors.append(
                    f"作业 {instance.jobs[i].job_id} 完成时刻 {clock} 超过截止 {instance.jobs[i].deadline}"
                )
    return not errors, errors

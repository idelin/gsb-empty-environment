"""整数时钟时间自动机可达性检查器(Python 3.11, 仅标准库)。

语义约定
--------
- 时钟取非负整数值, 时间以整数步推进; 全局时间上界 ``time_bound`` 显式给定,
  任何延迟不得使累计时间超过该上界。
- 等价截断(equivalence truncation): 令 K 为模型全部 guard 与 invariant 中
  出现的最大常数, 任何时钟值 v > K 在离散状态表示中统一截断为 K+1。
  由于所有比较常数均 <= K, 截断前后 guard/invariant 的判定结果完全一致,
  故该截断保持可达性等价, 且保证状态空间有限、搜索必然终止。
- reset 把对应时钟置 0, 旧时钟值不保留。
- 零时间环(不改变离散状态的自环, 如无 reset 的离散迁移)被 visited 集合
  吸收, 不会无限扩展。
- 验证对象仅限给定有限状态模型: 若扩展预算 ``max_expansions`` 耗尽而
  搜索队列非空, 结论保持 UNKNOWN(未决), 绝不误报 UNREACHABLE。
- 反例(可达轨迹)由 Delay/Take 步骤序列构成, 可通过 ``replay`` 确定性
  重放并逐步校验。
"""

from __future__ import annotations

import operator
from collections import deque
from dataclasses import dataclass, field
from enum import Enum, auto

_OPS = {
    "<": operator.lt,
    "<=": operator.le,
    "==": operator.eq,
    ">=": operator.ge,
    ">": operator.gt,
}


@dataclass(frozen=True)
class Constraint:
    """形如 ``x < 3`` 的原子约束; 严格与非严格比较语义不同。"""

    clock: str
    op: str
    value: int

    def __post_init__(self) -> None:
        if self.op not in _OPS:
            raise ValueError(f"不支持的比较运算符: {self.op!r}")
        if self.value < 0:
            raise ValueError("约束常数必须为非负整数")

    def holds(self, valuation: dict[str, int]) -> bool:
        return _OPS[self.op](valuation[self.clock], self.value)

    def __str__(self) -> str:
        return f"{self.clock}{self.op}{self.value}"


@dataclass(frozen=True)
class Transition:
    """离散迁移: 源/目标位置、guard(合取)、reset 时钟集合。"""

    name: str
    source: str
    target: str
    guard: tuple[Constraint, ...] = ()
    resets: frozenset[str] = frozenset()


@dataclass(frozen=True)
class Delay:
    """轨迹步骤: 整数时间推进 duration 个单位。"""

    duration: int


@dataclass(frozen=True)
class Take:
    """轨迹步骤: 触发指定名称的离散迁移。"""

    transition: str


Step = Delay | Take


class Verdict(Enum):
    REACHABLE = auto()    # 可达, 附可重放轨迹
    UNREACHABLE = auto()  # 状态空间已穷尽, 不可达
    UNKNOWN = auto()      # 未搜索完(预算耗尽), 保持未决


@dataclass
class CheckResult:
    verdict: Verdict
    trace: list[Step] | None
    explored_states: int


@dataclass
class TimedAutomaton:
    """有限状态时间自动机: 整数时钟、guard、reset、位置 invariant。"""

    clocks: tuple[str, ...]
    initial: str
    transitions: tuple[Transition, ...]
    invariants: dict[str, tuple[Constraint, ...]] = field(default_factory=dict)

    def __post_init__(self) -> None:
        names = [t.name for t in self.transitions]
        if len(names) != len(set(names)):
            raise ValueError("迁移名称必须唯一(轨迹按名称重放)")
        clock_set = set(self.clocks)
        for t in self.transitions:
            unknown = set(t.resets) - clock_set
            if unknown:
                raise ValueError(f"迁移 {t.name} reset 了未声明的时钟: {unknown}")
            for c in t.guard:
                if c.clock not in clock_set:
                    raise ValueError(f"迁移 {t.name} guard 引用了未声明的时钟: {c.clock}")
        for loc, cons in self.invariants.items():
            for c in cons:
                if c.clock not in clock_set:
                    raise ValueError(f"位置 {loc} 的 invariant 引用了未声明的时钟: {c.clock}")
        self._by_source: dict[str, list[Transition]] = {}
        for t in self.transitions:
            self._by_source.setdefault(t.source, []).append(t)
        self._by_name = {t.name: t for t in self.transitions}

    @property
    def max_constant(self) -> int:
        """模型中出现的最大约束常数 K, 截断阈值为 K+1。"""
        constants = [c.value for t in self.transitions for c in t.guard]
        constants += [c.value for cons in self.invariants.values() for c in cons]
        return max(constants, default=0)

    def transitions_from(self, location: str) -> list[Transition]:
        return self._by_source.get(location, [])

    def transition_named(self, name: str) -> Transition:
        try:
            return self._by_name[name]
        except KeyError:
            raise ValueError(f"不存在的迁移: {name!r}") from None

    def invariant_holds(self, location: str, valuation: dict[str, int]) -> bool:
        return all(c.holds(valuation) for c in self.invariants.get(location, ()))


def check_reachability(
    automaton: TimedAutomaton,
    target: str,
    time_bound: int,
    max_expansions: int = 100_000,
) -> CheckResult:
    """BFS 显式状态搜索。

    状态 = (位置, 截断后的时钟赋值); visited 记录每个状态的最早到达时间。
    搜索穷尽返回 UNREACHABLE; 预算耗尽且队列非空返回 UNKNOWN。
    """
    if time_bound < 0:
        raise ValueError("time_bound 必须为非负整数")
    clocks = automaton.clocks
    cap = automaton.max_constant + 1

    def capped(valuation: tuple[int, ...]) -> tuple[int, ...]:
        return tuple(min(v, cap) for v in valuation)

    def as_dict(valuation: tuple[int, ...]) -> dict[str, int]:
        return dict(zip(clocks, valuation))

    init_val = tuple(0 for _ in clocks)
    if not automaton.invariant_holds(automaton.initial, as_dict(init_val)):
        raise ValueError("初始位置不满足其 invariant")

    visited: dict[tuple[str, tuple[int, ...]], int] = {
        (automaton.initial, init_val): 0
    }
    queue: deque[tuple[str, tuple[int, ...], int, list[Step]]] = deque(
        [(automaton.initial, init_val, 0, [])]
    )
    expansions = 0

    while queue:
        loc, val, time, trace = queue.popleft()
        if loc == target:
            return CheckResult(Verdict.REACHABLE, trace, len(visited))
        if expansions >= max_expansions:
            return CheckResult(Verdict.UNKNOWN, None, len(visited))
        expansions += 1

        # 离散迁移(零时间): guard 在截断赋值上判定, 与原赋值等价。
        for tr in automaton.transitions_from(loc):
            if not all(c.holds(as_dict(val)) for c in tr.guard):
                continue
            new_val = tuple(
                0 if clock in tr.resets else v for clock, v in zip(clocks, val)
            )
            if not automaton.invariant_holds(tr.target, as_dict(new_val)):
                continue
            key = (tr.target, new_val)
            if key not in visited or time < visited[key]:
                visited[key] = time
                queue.append((tr.target, new_val, time, trace + [Take(tr.name)]))

        # 整数时间推进: 受全局上界与位置 invariant 约束。
        # invariant 为上界型约束的合取, 端点满足即整段满足。
        for duration in range(1, time_bound - time + 1):
            new_val = capped(tuple(v + duration for v in val))
            if not automaton.invariant_holds(loc, as_dict(new_val)):
                break  # 上界型 invariant 一旦违反, 更大延迟也违反
            key = (loc, new_val)
            new_time = time + duration
            if key not in visited or new_time < visited[key]:
                visited[key] = new_time
                queue.append((loc, new_val, new_time, trace + [Delay(duration)]))

    return CheckResult(Verdict.UNREACHABLE, None, len(visited))


class ReplayError(Exception):
    """轨迹重放校验失败。"""


def replay(
    automaton: TimedAutomaton, trace: list[Step], time_bound: int
) -> list[tuple[int, str, dict[str, int]]]:
    """确定性重放轨迹, 返回每步之后的 (累计时间, 位置, 未截断时钟赋值)。

    任何一步不合法(超上界、违反 invariant、guard 不满足、迁移名错误等)
    都抛出 ReplayError。
    """
    valuation = {c: 0 for c in automaton.clocks}
    location = automaton.initial
    time = 0
    if not automaton.invariant_holds(location, valuation):
        raise ReplayError(f"初始位置 {location} 不满足 invariant")
    snapshots = [(time, location, dict(valuation))]

    for index, step in enumerate(trace):
        if isinstance(step, Delay):
            if step.duration < 1:
                raise ReplayError(f"第 {index} 步: 延迟必须为正整数")
            time += step.duration
            if time > time_bound:
                raise ReplayError(f"第 {index} 步: 累计时间 {time} 超过上界 {time_bound}")
            for clock in valuation:
                valuation[clock] += step.duration
            if not automaton.invariant_holds(location, valuation):
                raise ReplayError(f"第 {index} 步: 延迟后违反 {location} 的 invariant")
        elif isinstance(step, Take):
            try:
                tr = automaton.transition_named(step.transition)
            except ValueError as exc:
                raise ReplayError(f"第 {index} 步: {exc}") from exc
            if tr.source != location:
                raise ReplayError(
                    f"第 {index} 步: 迁移 {tr.name} 的源位置 {tr.source} != 当前位置 {location}"
                )
            if not all(c.holds(valuation) for c in tr.guard):
                raise ReplayError(f"第 {index} 步: 迁移 {tr.name} 的 guard 不满足")
            for clock in tr.resets:
                valuation[clock] = 0  # reset: 旧值不保留
            location = tr.target
            if not automaton.invariant_holds(location, valuation):
                raise ReplayError(f"第 {index} 步: 进入 {location} 违反其 invariant")
        else:
            raise ReplayError(f"第 {index} 步: 未知步骤类型 {step!r}")
        snapshots.append((time, location, dict(valuation)))

    return snapshots


def format_trajectory(
    automaton: TimedAutomaton, trace: list[Step], time_bound: int
) -> str:
    """把轨迹格式化为时间推进轨迹文本(内部先 replay 校验)。"""
    snapshots = replay(automaton, trace, time_bound)

    def fmt(time: int, location: str, valuation: dict[str, int]) -> str:
        clocks = ", ".join(f"{c}={v}" for c, v in valuation.items())
        return f"t={time:<3} @ {location:<10} {{{clocks}}}"

    lines = [fmt(*snapshots[0])]
    for step, snapshot in zip(trace, snapshots[1:]):
        if isinstance(step, Delay):
            lines.append(f"  -- delay {step.duration} -->")
        else:
            lines.append(f"  -- take {step.transition} -->")
        lines.append(fmt(*snapshot))
    return "\n".join(lines)

"""反例重放验证器。

给定模型与 lasso 形反例（prefix + cycle），逐步在模型上重放：
- 每条迁移必须真实存在于模型中且首尾相接；
- cycle 必须非空且闭合（可无限重复）；
- cycle 上必须存在 request 置位状态，且无任何 response 置位状态；
- cycle 必须满足给定的公平性假设（按显式动作集合逐条核验）。

只有真实闭合循环才构成违反：有限前缀（截断轨迹）上未见到响应，
重放器会拒绝判定其为反例。
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .checker import Counterexample, FairnessEvidence, FairnessSet
from .model import Model


@dataclass
class ReplayReport:
    ok: bool
    problems: list = field(default_factory=list)
    fairness: list = field(default_factory=list)  # list[FairnessEvidence]
    cycle_states: tuple = ()

    def render(self) -> str:
        lines = [f"重放结论: {'通过（反例成立）' if self.ok else '失败（反例不成立）'}"]
        if self.cycle_states:
            lines.append(f"循环状态: {', '.join(self.cycle_states)}")
        for e in self.fairness:
            lines.append(f"公平性[{e.kind}] {e.set_name}: {e.detail}")
        for p in self.problems:
            lines.append(f"问题: {p}")
        return "\n".join(lines)


def _follow(model: Model, state, transition, problems, where):
    if transition.src != state:
        problems.append(
            f"{where}: 迁移源状态 {transition.src!r} 与当前状态 {state!r} 不符"
        )
        return state
    if not model.has_transition(transition):
        problems.append(f"{where}: 模型中不存在迁移 {transition}")
        return state
    return transition.dst


def replay(model: Model, counterexample: Counterexample,
           request_prop="req", response_prop="resp",
           fairness=()) -> ReplayReport:
    fairness = tuple(fairness)
    problems = []

    state = model.initial
    for i, t in enumerate(counterexample.prefix):
        state = _follow(model, state, t, problems, f"prefix[{i}]")

    cycle = counterexample.cycle
    if not cycle:
        problems.append(
            "cycle 为空：有限截断轨迹上未见到响应不构成违反，"
            "只有闭合循环才是有效反例"
        )
        return ReplayReport(False, problems, [], ())

    cycle_start = state
    cycle_states = []
    for i, t in enumerate(cycle):
        cycle_states.append(state)
        state = _follow(model, state, t, problems, f"cycle[{i}]")
    if state != cycle_start:
        problems.append(
            f"cycle 不闭合：终点 {state!r} != 起点 {cycle_start!r}，无法无限重复"
        )

    distinct_states = set(cycle_states)
    if not any(request_prop in model.props(s) for s in distinct_states):
        problems.append("cycle 上没有任何 request 置位状态，无未决请求")
    if any(response_prop in model.props(s) for s in distinct_states):
        problems.append("cycle 上存在 response 置位状态，请求已被响应")

    evidence = []
    for fs in fairness:
        assert isinstance(fs, FairnessSet)
        taken = [t.action for t in cycle if t.action in fs.actions]
        enabled_states = [
            s for s in distinct_states
            if model.enabled_actions(s) & fs.actions
        ]
        if fs.kind == "weak":
            continuously_enabled = len(enabled_states) == len(distinct_states)
            fair = bool(taken) or not continuously_enabled
            detail = (
                f"集合动作在循环全部 {len(distinct_states)} 个状态上"
                f"{'持续可执行' if continuously_enabled else '非持续可执行'}"
                f"（可执行状态 {len(enabled_states)} 个），"
                f"循环内{'执行了 ' + ','.join(taken) if taken else '未执行任何集合动作'}"
            )
        else:
            fair = bool(taken) or not enabled_states
            detail = (
                f"集合动作在循环经过的状态上"
                f"{'可执行于 ' + ','.join(sorted(enabled_states)) if enabled_states else '处处不可执行'}，"
                f"循环内{'执行了 ' + ','.join(taken) if taken else '未执行任何集合动作'}"
            )
        evidence.append(FairnessEvidence(fs.name, fs.kind, detail))
        if not fair:
            problems.append(
                f"cycle 违反公平性约束 '{fs.name}'（{fs.kind}），不构成有效反例"
            )

    return ReplayReport(not problems, problems, evidence,
                        tuple(sorted(distinct_states)))

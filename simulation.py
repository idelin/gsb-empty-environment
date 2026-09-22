"""交替模拟（alternating simulation）检查。

- 最大模拟关系用不动点迭代计算，有限状态集合上单调递减，必然终止
  （含循环的协议也能正常结束）；
- 初始对不在关系中时，沿"坏对"图做广度优先搜索，提取最短的失败
  交互路径作为反例；
- 反例可逐步重放，并可导出为独立可执行的 Python 脚本。
"""
from __future__ import annotations

import json
from collections import deque
from dataclasses import dataclass, field

from machine import Machine

MISSING_INPUT = "missing_input"        # 候选机缺少规范要求的输入
UNEXPECTED_OUTPUT = "unexpected_output"  # 候选机发出规范未允许的输出


class ReplayError(Exception):
    """反例重放过程中发现与机器不符时抛出。"""


@dataclass(frozen=True)
class Step:
    """交互路径中的一步：候选机与规范机在同一动作上同步前进。"""
    action: str
    kind: str  # "input" 或 "output"
    candidate_src: str
    candidate_dst: str
    spec_src: str
    spec_dst: str

    def label(self):
        mark = "?" if self.kind == "input" else "!"
        return self.action + mark


@dataclass(frozen=True)
class Failure:
    """路径终点的失败原因。"""
    kind: str  # MISSING_INPUT 或 UNEXPECTED_OUTPUT
    action: str
    candidate_state: str
    spec_state: str

    def describe(self):
        if self.kind == MISSING_INPUT:
            return (f"候选机在状态 {self.candidate_state} 无法接收"
                    f"规范要求的输入 {self.action}?")
        return (f"候选机在状态 {self.candidate_state} 发出"
                f"规范未允许的输出 {self.action}!")


@dataclass
class Counterexample:
    """一条可逐步重放的失败交互路径。"""
    steps: list[Step] = field(default_factory=list)
    failure: Failure | None = None

    def format(self):
        lines = ["反例交互路径:"]
        if not self.steps:
            lines.append("  (初始状态即失败，无需任何交互步骤)")
        for i, step in enumerate(self.steps, 1):
            lines.append(
                f"  {i}. ({step.candidate_src}, {step.spec_src})"
                f" --{step.label()}-->"
                f" ({step.candidate_dst}, {step.spec_dst})"
            )
        lines.append(f"  失败: {self.failure.describe()}")
        return "\n".join(lines)

    def replay(self, candidate: Machine, spec: Machine, on_step=None):
        """在两台机器上逐步重放路径，验证每个转移与终点失败。

        on_step: 可选回调 on_step(index, step)，用于逐步展示。
        重放成功返回 True，否则抛出 ReplayError。
        """
        c, s = candidate.initial, spec.initial
        for i, step in enumerate(self.steps, 1):
            if step.candidate_src != c or step.spec_src != s:
                raise ReplayError(
                    f"第 {i} 步: 起点 ({step.candidate_src}, {step.spec_src})"
                    f" 与当前状态 ({c}, {s}) 不符"
                )
            if step.candidate_dst not in candidate.post(c, step.action):
                raise ReplayError(
                    f"第 {i} 步: 候选机不存在转移 {c} --{step.action}-->"
                    f" {step.candidate_dst}"
                )
            if step.spec_dst not in spec.post(s, step.action):
                raise ReplayError(
                    f"第 {i} 步: 规范机不存在转移 {s} --{step.action}-->"
                    f" {step.spec_dst}"
                )
            if on_step is not None:
                on_step(i, step)
            c, s = step.candidate_dst, step.spec_dst

        f = self.failure
        if f.candidate_state != c or f.spec_state != s:
            raise ReplayError(
                f"终点状态 ({f.candidate_state}, {f.spec_state})"
                f" 与重放到达的 ({c}, {s}) 不符"
            )
        if f.kind == MISSING_INPUT:
            if f.action not in spec.enabled_inputs(s):
                raise ReplayError(f"终点: 规范在 {s} 并不要求输入 {f.action}?")
            if candidate.enabled(c, f.action):
                raise ReplayError(f"终点: 候选机在 {c} 实际可以接收 {f.action}?")
        elif f.kind == UNEXPECTED_OUTPUT:
            if f.action not in candidate.enabled_outputs(c):
                raise ReplayError(f"终点: 候选机在 {c} 并不会发出 {f.action}!")
            if spec.enabled(s, f.action):
                raise ReplayError(f"终点: 规范在 {s} 实际允许输出 {f.action}!")
        else:
            raise ReplayError(f"未知的失败类型: {f.kind}")
        return True

    def to_script(self, candidate: Machine, spec: Machine):
        """导出为独立可执行的 Python 脚本（仅依赖标准库）。"""
        payload = {
            "candidate": candidate.to_dict(),
            "spec": spec.to_dict(),
            "steps": [
                {
                    "action": st.action,
                    "candidate_src": st.candidate_src,
                    "candidate_dst": st.candidate_dst,
                    "spec_src": st.spec_src,
                    "spec_dst": st.spec_dst,
                }
                for st in self.steps
            ],
            "failure": {
                "kind": self.failure.kind,
                "action": self.failure.action,
                "candidate_state": self.failure.candidate_state,
                "spec_state": self.failure.spec_state,
            },
        }
        data = json.dumps(payload, ensure_ascii=False, indent=2)
        return _SCRIPT_TEMPLATE.replace("__PAYLOAD__", data)


@dataclass
class SimulationResult:
    ok: bool
    iterations: int                 # 不动点迭代轮数（含循环也能终止的证据）
    relation: frozenset             # 最大模拟关系
    counterexample: Counterexample | None = None


# ----------------------------------------------------------------------
# 不动点计算
# ----------------------------------------------------------------------
def _consistent(candidate, spec, c, s, relation):
    """检查状态对 (c, s) 相对于 relation 的一步一致性。

    输入分支（逆变）：规范在 s 要求的每个输入，候选在 c 必须能接收，
    且规范的每个后继都要有候选后继落在 relation 中；
    候选多接收的输入不做限制。
    输出分支（协变）：候选在 c 发出的每个输出，规范在 s 必须允许，
    且候选的每个后继都要有规范后继落在 relation 中。
    """
    for a in spec.enabled_inputs(s):
        if not candidate.enabled(c, a):
            return False
        for s2 in spec.post(s, a):
            if not any((c2, s2) in relation for c2 in candidate.post(c, a)):
                return False
    for a in candidate.enabled_outputs(c):
        if not spec.enabled(s, a):
            return False
        for c2 in candidate.post(c, a):
            if not any((c2, s2) in relation for s2 in spec.post(s, a)):
                return False
    return True


def greatest_simulation(candidate: Machine, spec: Machine):
    """最大模拟关系的不动点计算。返回 (关系, 迭代轮数)。"""
    relation = {(c, s) for c in candidate.states for s in spec.states}
    iterations = 0
    while True:
        iterations += 1
        nxt = {(c, s) for (c, s) in relation
               if _consistent(candidate, spec, c, s, relation)}
        if nxt == relation:
            return frozenset(relation), iterations
        relation = nxt  # 单调递减且有限，必然终止


# ----------------------------------------------------------------------
# 反例提取
# ----------------------------------------------------------------------
def _find_counterexample(candidate, spec, good):
    """在"坏对"图上从初始对做 BFS，寻找通向局部失败的最短路径。"""
    start = (candidate.initial, spec.initial)
    parents = {start: None}  # pair -> (prev_pair, Step)
    queue = deque([start])
    while queue:
        c, s = pair = queue.popleft()
        # 局部失败一：候选机缺少规范要求的输入
        for a in sorted(spec.enabled_inputs(s)):
            if not candidate.enabled(c, a):
                return _build_path(pair, parents,
                                   Failure(MISSING_INPUT, a, c, s))
        # 局部失败二：候选机发出规范未允许的输出
        for a in sorted(candidate.enabled_outputs(c)):
            if not spec.enabled(s, a):
                return _build_path(pair, parents,
                                   Failure(UNEXPECTED_OUTPUT, a, c, s))
        # 否则沿坏后继继续前进（不在最大关系中的对必有坏后继）
        for a in sorted(spec.enabled_inputs(s)):
            for s2 in sorted(spec.post(s, a)):
                if any((c2, s2) in good for c2 in candidate.post(c, a)):
                    continue
                c2 = sorted(candidate.post(c, a))[0]
                nxt = (c2, s2)
                if nxt not in parents:
                    parents[nxt] = (pair, Step(a, "input", c, c2, s, s2))
                    queue.append(nxt)
        for a in sorted(candidate.enabled_outputs(c)):
            for c2 in sorted(candidate.post(c, a)):
                if any((c2, s2) in good for s2 in spec.post(s, a)):
                    continue
                s2 = sorted(spec.post(s, a))[0]
                nxt = (c2, s2)
                if nxt not in parents:
                    parents[nxt] = (pair, Step(a, "output", c, c2, s, s2))
                    queue.append(nxt)
    raise RuntimeError("不可达: 坏对必然通向局部失败")


def _build_path(pair, parents, failure):
    steps = []
    node = pair
    while parents[node] is not None:
        prev, step = parents[node]
        steps.append(step)
        node = prev
    steps.reverse()
    return Counterexample(steps=steps, failure=failure)


def check_simulation(candidate: Machine, spec: Machine) -> SimulationResult:
    """检查候选机是否被规范机（交替）模拟，即候选机是否兼容。"""
    relation, iterations = greatest_simulation(candidate, spec)
    start = (candidate.initial, spec.initial)
    if start in relation:
        return SimulationResult(True, iterations, relation, None)
    cex = _find_counterexample(candidate, spec, relation)
    return SimulationResult(False, iterations, relation, cex)


# ----------------------------------------------------------------------
# 独立可执行反例脚本的模板
# ----------------------------------------------------------------------
_SCRIPT_TEMPLATE = '''#!/usr/bin/env python3
# 由 iosim 生成的独立反例重放脚本：不依赖原项目代码，可直接执行。
# 退出码 0 表示反例在两台机器上重放成功（失败确实发生）。
import json
import sys

DATA = json.loads("""__PAYLOAD__""")


def index(machine):
    post = {}
    for src, action, dst in machine["transitions"]:
        post.setdefault((src, action), set()).add(dst)
    return post


def main():
    cand, spec = DATA["candidate"], DATA["spec"]
    cp, sp = index(cand), index(spec)
    c, s = cand["initial"], spec["initial"]
    print("重放反例: 候选机 %s vs 规范 %s" % (cand["name"], spec["name"]))
    for i, step in enumerate(DATA["steps"], 1):
        action = step["action"]
        ok = (
            step["candidate_src"] == c
            and step["spec_src"] == s
            and step["candidate_dst"] in cp.get((c, action), set())
            and step["spec_dst"] in sp.get((s, action), set())
        )
        if not ok:
            print("第 %d 步重放失败: 转移不存在" % i)
            return 1
        mark = "?" if action in cand["inputs"] else "!"
        print("  步骤 %d: (%s, %s) --%s%s--> (%s, %s)" % (
            i, c, s, action, mark, step["candidate_dst"], step["spec_dst"]))
        c, s = step["candidate_dst"], step["spec_dst"]

    failure = DATA["failure"]
    action = failure["action"]
    if failure["kind"] == "missing_input":
        ok = (action in spec["inputs"]
              and (s, action) in sp
              and (c, action) not in cp)
        msg = "候选机在状态 %s 无法接收规范要求的输入 %s?" % (c, action)
    else:
        ok = (action in cand["outputs"]
              and (c, action) in cp
              and (s, action) not in sp)
        msg = "候选机在状态 %s 发出规范未允许的输出 %s!" % (c, action)
    if not ok:
        print("终点失败条件不成立，重放失败")
        return 1
    print("终点失败确认: " + msg)
    print("反例重放成功")
    return 0


if __name__ == "__main__":
    sys.exit(main())
'''

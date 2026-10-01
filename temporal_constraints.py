#!/usr/bin/env python3
"""不确定时间事件因果验真（差分约束可行性判定）。

每个事件 i 的发生时间 x_i 为闭区间 [lo_i, hi_i] 内的未知量；
每条因果约束 (u, v, d) 表示下游事件 v 至少比上游事件 u 晚整数 d：
    x_v - x_u >= d
判定全部约束（含区间上下界）是否可同时满足：
  - 可行：输出一组满足全部约束的时间赋值；
  - 不可行：输出负环（或等价的冲突证据）。

支持增量添加约束：添加导致不可行时自动回滚，原始时间区间永不被修改。

求解方法：将系统化为 x_v - x_u <= w 形式的最短路问题，
用 Bellman-Ford 检测负环；无负环时最短路距离即为一组合法赋值。
"""

from __future__ import annotations

import json
import sys
from dataclasses import dataclass, field


@dataclass(frozen=True)
class DelayConstraint:
    """下游 downstream 至少比上游 upstream 晚 min_delay（整数）。"""

    upstream: str
    downstream: str
    min_delay: int

    def describe(self) -> str:
        return (
            f"{self.downstream} - {self.upstream} >= {self.min_delay}"
            f"  (下游 {self.downstream} 至少比上游 {self.upstream} 晚 {self.min_delay})"
        )


@dataclass(frozen=True)
class _Edge:
    """最短路图中的边：dist[v] <= dist[u] + w。"""

    u: str
    v: str
    w: float
    desc: str  # 面向用户的约束来源描述（>= 形式）


@dataclass
class CycleEvidence:
    """不可行证据：一个负环，即沿环一周要求 x - x >= K (K > 0)，矛盾。"""

    chain: list[str]  # 构成环的约束描述（按环的顺序）
    required_self_delay: float  # K，环上延迟之和（>0 即矛盾）

    def to_dict(self) -> dict:
        k = self.required_self_delay
        k = int(k) if float(k).is_integer() else k
        return {
            "type": "negative_cycle",
            "chain": self.chain,
            "contradiction": (
                f"沿环一周要求同一事件比自身晚 {k}，即 0 >= {k}，矛盾"
            ),
        }


@dataclass
class AddResult:
    ok: bool
    evidence: CycleEvidence | None = None


class TemporalConstraintSystem:
    """事件时间区间固定，差分约束可增量添加、失败自动回滚。"""

    SOURCE = "__source__"

    def __init__(self, intervals: dict[str, tuple[float, float]]):
        if not intervals:
            raise ValueError("至少需要一个事件")
        self._intervals: dict[str, tuple[float, float]] = {}
        for name, (lo, hi) in intervals.items():
            if name == self.SOURCE:
                raise ValueError(f"事件名 {name!r} 为保留字")
            if lo > hi:
                raise ValueError(f"事件 {name} 区间非法：[{lo}, {hi}]，下界大于上界")
            self._intervals[name] = (lo, hi)
        self._constraints: list[DelayConstraint] = []

    @property
    def intervals(self) -> dict[str, tuple[float, float]]:
        # 只读视图：原始时间区间不允许被修改
        return dict(self._intervals)

    @property
    def constraints(self) -> list[DelayConstraint]:
        return list(self._constraints)

    def add_constraint(self, constraint: DelayConstraint) -> AddResult:
        """增量添加约束；若导致不可行则回滚并返回负环证据。"""
        for name in (constraint.upstream, constraint.downstream):
            if name not in self._intervals:
                raise ValueError(f"约束引用了未定义的事件: {name}")
        self._constraints.append(constraint)
        assignment, evidence = self._solve()
        if evidence is not None:
            self._constraints.pop()  # 回滚，保持系统可行
            return AddResult(ok=False, evidence=evidence)
        return AddResult(ok=True)

    def solve(self) -> tuple[dict[str, float] | None, CycleEvidence | None]:
        """求解当前系统。可行返回赋值，不可行返回负环证据。"""
        return self._solve()

    # ---- 内部实现 ----

    def _build_edges(self) -> list[_Edge]:
        edges: list[_Edge] = []
        src = self.SOURCE
        for name, (lo, hi) in self._intervals.items():
            # x_i <= hi  ->  边 src -> i，权 hi
            edges.append(_Edge(src, name, hi, f"{name} <= {hi}  (区间上界)"))
            # x_i >= lo 即 x_src - x_i <= -lo  ->  边 i -> src，权 -lo
            edges.append(_Edge(name, src, -lo, f"{name} >= {lo}  (区间下界)"))
        for c in self._constraints:
            # x_v - x_u >= d 即 x_u - x_v <= -d  ->  边 v -> u，权 -d
            edges.append(_Edge(c.downstream, c.upstream, -c.min_delay, c.describe()))
        return edges

    def _solve(self) -> tuple[dict[str, float] | None, CycleEvidence | None]:
        nodes = [self.SOURCE, *self._intervals.keys()]
        edges = self._build_edges()
        dist: dict[str, float] = {n: 0 for n in nodes}
        pred: dict[str, tuple[str, _Edge] | None] = {n: None for n in nodes}
        n = len(nodes)
        hot = None
        for _ in range(n):
            hot = None
            for e in edges:
                if dist[e.u] + e.w < dist[e.v]:
                    dist[e.v] = dist[e.u] + e.w
                    pred[e.v] = (e.u, e)
                    hot = e.v
            if hot is None:
                break
        if hot is not None:
            return None, self._extract_cycle(hot, pred)

        # 无负环：dist 满足全部 <= 约束。以源点为基准平移，得到事件时间赋值。
        base = dist[self.SOURCE]
        assignment = {name: dist[name] - base for name in self._intervals}
        assert self._check_assignment(assignment)  # 构造保证，防御性校验
        return assignment, None

    @staticmethod
    def _extract_cycle(
        hot: str, pred: dict[str, tuple[str, _Edge] | None]
    ) -> CycleEvidence:
        # 从第 n 轮仍可松弛的点出发，沿前驱走 n 步必进入负环
        node = hot
        for _ in range(len(pred)):
            node = pred[node][0]  # type: ignore[index]
        start = node
        chain: list[str] = []
        total_w = 0.0
        cur = start
        while True:
            prev, edge = pred[cur]  # type: ignore[misc]
            chain.append(edge.desc)
            total_w += edge.w
            cur = prev
            if cur == start:
                break
        chain.reverse()
        # 环上 <= 约束求和得 0 <= total_w；不可行 <=> total_w < 0，
        # 等价于 >= 形式下要求 0 >= -total_w（正的自延迟）。
        return CycleEvidence(chain=chain, required_self_delay=-total_w)

    def _check_assignment(self, assignment: dict[str, float]) -> bool:
        for name, (lo, hi) in self._intervals.items():
            if not (lo <= assignment[name] <= hi):
                return False
        for c in self._constraints:
            if assignment[c.downstream] - assignment[c.upstream] < c.min_delay:
                return False
        return True


# ---------------- CLI ----------------

def load_instance(data: dict) -> tuple[TemporalConstraintSystem, list[DelayConstraint]]:
    events = data.get("events")
    if not isinstance(events, dict) or not events:
        raise ValueError("输入缺少非空的 'events' 对象：{事件名: [下界, 上界]}")
    intervals: dict[str, tuple[float, float]] = {}
    for name, bounds in events.items():
        if (
            not isinstance(bounds, list)
            or len(bounds) != 2
            or not all(isinstance(b, (int, float)) for b in bounds)
        ):
            raise ValueError(f"事件 {name} 的区间必须是 [下界, 上界] 二元数组")
        intervals[name] = (bounds[0], bounds[1])
    system = TemporalConstraintSystem(intervals)

    constraints: list[DelayConstraint] = []
    for i, item in enumerate(data.get("constraints", [])):
        try:
            upstream = item["upstream"]
            downstream = item["downstream"]
            delay = item["min_delay"]
        except (TypeError, KeyError) as exc:
            raise ValueError(
                f"第 {i} 条约束缺少字段 upstream/downstream/min_delay"
            ) from exc
        if not isinstance(delay, int):
            raise ValueError(f"第 {i} 条约束的 min_delay 必须是整数")
        constraints.append(DelayConstraint(upstream, downstream, delay))
    return system, constraints


def run_instance(data: dict) -> dict:
    """按输入顺序增量添加约束，失败的回滚；返回完整报告。"""
    system, constraints = load_instance(data)
    steps = []
    for i, c in enumerate(constraints):
        result = system.add_constraint(c)
        step = {
            "index": i,
            "constraint": {
                "upstream": c.upstream,
                "downstream": c.downstream,
                "min_delay": c.min_delay,
            },
            "accepted": result.ok,
        }
        if not result.ok:
            step["rolled_back"] = True
            step["evidence"] = result.evidence.to_dict()  # type: ignore[union-attr]
        steps.append(step)

    assignment, evidence = system.solve()
    report = {
        "steps": steps,
        "feasible": evidence is None,
        "active_constraints": [
            {
                "upstream": c.upstream,
                "downstream": c.downstream,
                "min_delay": c.min_delay,
            }
            for c in system.constraints
        ],
        "intervals": {k: list(v) for k, v in system.intervals.items()},
    }
    if assignment is not None:
        report["assignment"] = assignment
        report["assignment_verified"] = system._check_assignment(assignment)
    else:
        report["evidence"] = evidence.to_dict()  # type: ignore[union-attr]
    return report


def main(argv: list[str]) -> int:
    path = argv[1] if len(argv) > 1 else None
    try:
        if path:
            with open(path, "r", encoding="utf-8") as fh:
                data = json.load(fh)
        else:
            data = json.load(sys.stdin)
        report = run_instance(data)
    except (ValueError, KeyError, json.JSONDecodeError) as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False), file=sys.stderr)
        return 2
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if report["feasible"] else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))

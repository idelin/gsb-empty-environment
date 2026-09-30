#!/usr/bin/env python3.11
"""不确定时间事件因果验真（差分约束可行性求解）。

模型：
  - 每个事件 e 的发生时间 x_e 落在闭区间 [lo_e, hi_e]（整数）。
  - 每条因果约束 (upstream, downstream, d) 表示 x_downstream - x_upstream >= d，
    即下游至少比上游晚整数 d。

判定：把所有约束统一改写为 x_v <= x_u + w 的差分约束，用 Bellman-Ford
检测负环（对应原问题中的“正时差环”）。可行时 dist 即为一组合法赋值；
不可行时抽出负环并翻译回可读的冲突证据。

增量：add_constraint 逐条添加，若导致不可行则自动回滚该条约束，
原始时间区间始终不被修改。
"""
from __future__ import annotations

import argparse
import json
import sys

SOURCE = "__source__"  # 参考点变量，区间上下界借此表达


class ConstraintSystem:
    """差分约束系统：x_v - x_u >= d，外加每个变量的闭区间上下界。"""

    def __init__(self) -> None:
        self.intervals: dict[str, tuple[int, int]] = {}
        self.constraints: list[tuple[str, str, int]] = []

    # ---------------------------------------------------------------- 建模
    def add_event(self, name: str, lo: int, hi: int) -> None:
        if name == SOURCE:
            raise ValueError(f"保留事件名: {name!r}")
        if lo > hi:
            raise ValueError(f"事件 {name} 的区间为空: [{lo}, {hi}]")
        self.intervals[name] = (int(lo), int(hi))

    def _edges(self) -> list[tuple[str, str, int, str]]:
        """统一转成 x_v <= x_u + w 形式，返回 (u, v, w, 可读标签)。"""
        edges: list[tuple[str, str, int, str]] = []
        for name, (lo, hi) in self.intervals.items():
            edges.append((SOURCE, name, hi, f"{name} <= {hi}（区间上界）"))
            edges.append((name, SOURCE, -lo, f"{name} >= {lo}（区间下界）"))
        for upstream, downstream, delay in self.constraints:
            # x_downstream >= x_upstream + d  <=>  x_upstream <= x_downstream - d
            edges.append((downstream, upstream, -delay,
                          f"{downstream} >= {upstream} + {delay}"))
        return edges

    # ---------------------------------------------------------------- 求解
    def solve(self):
        """返回 (assignment, None) 或 (None, conflict)。

        assignment: {事件名: 整数发生时间}，满足全部区间与差分约束。
        conflict: {
            "cycle": [构成冲突的可读约束列表],
            "min_self_delay": K,   # 约束链要求某事件至少比自己晚 K（K > 0）
            "explanation": 人类可读的矛盾说明,
        }
        """
        nodes = [SOURCE, *self.intervals]
        edges = self._edges()
        dist = {n: 0 for n in nodes}
        pred: dict[str, tuple[str, int, str] | None] = {n: None for n in nodes}

        changed = None
        for _ in range(len(nodes)):
            changed = None
            for u, v, w, label in edges:
                if dist[u] + w < dist[v]:
                    dist[v] = dist[u] + w
                    pred[v] = (u, w, label)
                    changed = v

        if changed is None:
            # dist 满足所有 x_v <= x_u + w，即一组合法赋值
            # 约束均为差分形式，整体平移不改变可行性；
            # 归一化使参考点 SOURCE 取 0，从而 lo <= x_e <= hi 严格成立。
            offset = dist[SOURCE]
            return {n: dist[n] - offset for n in self.intervals}, None

        # 从被松弛的节点沿前驱走 |V| 步，必落在负环上
        y = changed
        for _ in range(len(nodes)):
            y = pred[y][0]  # type: ignore[index]
        cycle_labels: list[str] = []
        total_w = 0
        cur = y
        while True:
            u, w, label = pred[cur]  # type: ignore[misc]
            cycle_labels.append(label)
            total_w += w
            cur = u
            if cur == y:
                break
        cycle_labels.reverse()
        min_self_delay = -total_w  # 负环权重和 < 0，故该值为正
        conflict = {
            "cycle": cycle_labels,
            "min_self_delay": min_self_delay,
            "explanation": (
                "上述约束构成正时差环：依次传递后要求某事件至少比自己晚 "
                f"{min_self_delay}，矛盾（0 < {min_self_delay} <= 0 不成立）。"
            ),
        }
        return None, conflict

    # ---------------------------------------------------------------- 增量
    def add_constraint(self, upstream: str, downstream: str, delay: int):
        """增量添加约束 x_downstream >= x_upstream + delay。

        可行：返回 (True, assignment)；
        不可行：回滚该约束（系统保持添加前的可行状态），返回 (False, conflict)。
        原始时间区间不被修改。
        """
        for ev in (upstream, downstream):
            if ev not in self.intervals:
                raise KeyError(f"未知事件: {ev!r}")
        self.constraints.append((upstream, downstream, int(delay)))
        assignment, conflict = self.solve()
        if conflict is not None:
            self.constraints.pop()  # 回滚
            return False, conflict
        return True, assignment


# --------------------------------------------------------------------- CLI
def run_spec(spec: dict) -> dict:
    """按输入规格求解，返回可 JSON 序列化的结果字典。"""
    system = ConstraintSystem()
    for name, bounds in spec.get("events", {}).items():
        lo, hi = bounds
        system.add_event(name, lo, hi)

    steps = []
    all_accepted = True
    assignment, conflict = system.solve()
    for c in spec.get("constraints", []):
        upstream, downstream, delay = c["upstream"], c["downstream"], c["delay"]
        ok, payload = system.add_constraint(upstream, downstream, delay)
        if ok:
            assignment = payload
            steps.append({
                "constraint": f"{downstream} >= {upstream} + {delay}",
                "status": "accepted",
            })
        else:
            all_accepted = False
            conflict = payload
            steps.append({
                "constraint": f"{downstream} >= {upstream} + {delay}",
                "status": "rolled_back",
                "conflict": payload,
            })

    result = {
        "feasible": all_accepted,
        "assignment": assignment,
        "committed_constraints": [
            f"{v} >= {u} + {d}" for (u, v, d) in system.constraints
        ],
        "steps": steps,
    }
    if not all_accepted:
        result["conflict"] = conflict
        result["note"] = "部分约束不可行已回滚；assignment 为已提交约束下的一组合法解。"
    return result


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="不确定时间事件因果验真：差分约束可行性判定与赋值求解")
    parser.add_argument("input", nargs="?",
                        help="输入 JSON 文件路径（缺省从标准输入读取）")
    args = parser.parse_args(argv)

    text = open(args.input, encoding="utf-8").read() if args.input else sys.stdin.read()
    spec = json.loads(text)
    result = run_spec(spec)
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0 if result["feasible"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

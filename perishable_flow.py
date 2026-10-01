#!/usr/bin/env python3
"""保质期转运网络（perishable transshipment network）最小成本流精确求解器。

模型
----
- 有向运输边：正整数时长、每单位时间槽容量、非负整数成本（按每单位货物计）。
- 批次货物：数量、出发时间、失效时间；到达目的地时刻必须严格小于失效时刻
  （到达时刻 == 失效时刻视为过期）。货物可在任意节点免费、无限量等待。
- 容量按 (边, 出发时刻) 计，所有批次共享；不同批次的失效约束不可混淆，
  因此按批次分别枚举路径，再做跨批次的精确组合分配。

算法
----
1. 在时间展开 DAG（时间严格递增，无环）上为每个批次枚举全部可行路径；
2. 深度优先 + 分支限界在“单位货物 -> 路径”的分配空间上精确搜索最小成本；
3. 搜索完整结束 => 结果已证最优；若某批可行路径数超过 --max-paths 限制
   则中止并在输出中明确标记 status="aborted"（未证最优）。

复杂度为指数级，适用于小规模实例的精确求解与枚举核验。
"""
from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from dataclasses import dataclass


# ---------------------------------------------------------------- 数据模型

@dataclass(frozen=True)
class Edge:
    id: str
    src: str
    dst: str
    duration: int   # 正整数，出发 t -> 到达 t + duration
    capacity: int   # 每个出发时刻时间槽的最大单位数
    cost: int       # 非负整数，每单位货物


@dataclass(frozen=True)
class Batch:
    id: str
    origin: str
    destination: str
    quantity: int
    departure: int  # 货物在 origin 可用的时刻
    expiry: int     # 到达 destination 的时刻必须 < expiry


@dataclass(frozen=True)
class Instance:
    edges: tuple
    batches: tuple
    horizon: int | None  # 有限整数时间轴上界（可选）


class PathLimitExceeded(Exception):
    """单个批次可行路径数超过限制，精确性无法保证。"""


def validate_instance(inst: Instance) -> None:
    if inst.horizon is not None and inst.horizon < 0:
        raise ValueError("horizon 必须为非负整数")
    edge_ids, batch_ids = set(), set()
    for e in inst.edges:
        if e.id in edge_ids:
            raise ValueError(f"边 id 重复: {e.id}")
        edge_ids.add(e.id)
        if not isinstance(e.duration, int) or e.duration < 1:
            raise ValueError(f"边 {e.id} 的 duration 必须为正整数")
        if not isinstance(e.capacity, int) or e.capacity < 0:
            raise ValueError(f"边 {e.id} 的 capacity 必须为非负整数")
        if not isinstance(e.cost, int) or e.cost < 0:
            raise ValueError(f"边 {e.id} 的 cost 必须为非负整数")
    for b in inst.batches:
        if b.id in batch_ids:
            raise ValueError(f"批次 id 重复: {b.id}")
        batch_ids.add(b.id)
        if not isinstance(b.quantity, int) or b.quantity < 0:
            raise ValueError(f"批次 {b.id} 的 quantity 必须为非负整数")
        if not isinstance(b.departure, int) or b.departure < 0:
            raise ValueError(f"批次 {b.id} 的 departure 必须为非负整数")
        if not isinstance(b.expiry, int) or b.expiry <= b.departure:
            raise ValueError(f"批次 {b.id} 的 expiry 必须大于 departure")


def instance_from_dict(d: dict) -> Instance:
    edges = tuple(
        Edge(str(e["id"]), str(e["from"]), str(e["to"]),
             int(e["duration"]), int(e["capacity"]), int(e["cost"]))
        for e in d["edges"]
    )
    batches = tuple(
        Batch(str(b["id"]), str(b["origin"]), str(b["destination"]),
              int(b["quantity"]), int(b["departure"]), int(b["expiry"]))
        for b in d["batches"]
    )
    horizon = d.get("horizon")
    inst = Instance(edges, batches, int(horizon) if horizon is not None else None)
    validate_instance(inst)
    return inst


# ---------------------------------------------------------------- 路径枚举

def enumerate_paths(inst: Instance, batch: Batch, edges_by_src: dict,
                    max_paths: int) -> list:
    """枚举批次在时间展开 DAG 上的全部可行路径。

    路径为 ((edge_index, depart_time), ...) 元组；等待由相邻边的时间差隐含表示。
    所有状态（含中间节点）的时刻都 <= expiry - 1：到达等于失效即过期，
    而边时长 >= 1，更晚的时刻不可能产生合法交付。
    首次到达 destination 即停止（成本非负，绕行或继续不会更优）。
    """
    t_cap = batch.expiry - 1
    if inst.horizon is not None:
        t_cap = min(t_cap, inst.horizon)
    paths = []
    if batch.departure > t_cap and batch.origin != batch.destination:
        return paths
    stack = [(batch.origin, batch.departure, ())]
    while stack:
        node, time, path = stack.pop()
        if node == batch.destination:
            paths.append(path)
            if len(paths) > max_paths:
                raise PathLimitExceeded(batch.id)
            continue
        if time + 1 <= t_cap:  # 仓库等待
            stack.append((node, time + 1, path))
        for ei in edges_by_src.get(node, ()):
            e = inst.edges[ei]
            arrive = time + e.duration
            if arrive <= t_cap:
                stack.append((e.dst, arrive, path + ((ei, time),)))
    return paths


def path_cost(inst: Instance, path: tuple) -> int:
    return sum(inst.edges[ei].cost for ei, _ in path)


# ---------------------------------------------------------------- 精确求解

def solve(inst: Instance, max_paths: int = 100_000) -> dict:
    """精确求解。返回 JSON 可序列化的解字典。

    status: "optimal"（已证最优）| "infeasible"（已证不可行）| "aborted"（未证最优）。
    """
    validate_instance(inst)
    edges_by_src = defaultdict(list)
    for i, e in enumerate(inst.edges):
        edges_by_src[e.src].append(i)

    try:
        paths_per_batch = [
            enumerate_paths(inst, b, edges_by_src, max_paths) for b in inst.batches
        ]
    except PathLimitExceeded as exc:
        return {
            "status": "aborted",
            "optimal": False,
            "message": f"批次 {exc.args[0]} 的可行路径数超过限制 {max_paths}，"
                       f"搜索中止，结果未证最优",
        }

    for b, ps in zip(inst.batches, paths_per_batch):
        if b.quantity > 0 and not ps:
            return {
                "status": "infeasible",
                "optimal": False,
                "message": f"批次 {b.id} 在失效前不存在任何可行路径（已证不可行）",
            }

    n = len(inst.batches)
    pcosts = [[path_cost(inst, p) for p in ps] for ps in paths_per_batch]
    min_cost = [min(c) if c else 0 for c in pcosts]
    suffix = [0] * (n + 1)  # suffix[i] = 批次 i..n-1 的最小成本下界
    for i in range(n - 1, -1, -1):
        suffix[i] = suffix[i + 1] + min_cost[i] * inst.batches[i].quantity

    used = defaultdict(int)          # (edge_index, depart_time) -> 已用容量
    assign = [[0] * len(ps) for ps in paths_per_batch]
    best = {"cost": None, "assign": None}

    def rec(bi: int, unit: int, prev_idx: int, cost: int) -> None:
        if best["cost"] is not None:
            lb = cost
            if bi < n:
                lb += min_cost[bi] * (inst.batches[bi].quantity - unit) + suffix[bi + 1]
            if lb >= best["cost"]:
                return
        if bi == n:
            best["cost"] = cost
            best["assign"] = [row[:] for row in assign]
            return
        b = inst.batches[bi]
        if unit == b.quantity:
            rec(bi + 1, 0, 0, cost)
            return
        ps = paths_per_batch[bi]
        # 同一批次的单位货物不可区分，路径下标不降以避免重复排列
        for idx in range(prev_idx, len(ps)):
            p = ps[idx]
            if any(used[(ei, t)] + 1 > inst.edges[ei].capacity for ei, t in p):
                continue
            for ei, t in p:
                used[(ei, t)] += 1
            assign[bi][idx] += 1
            rec(bi, unit + 1, idx, cost + pcosts[bi][idx])
            assign[bi][idx] -= 1
            for ei, t in p:
                used[(ei, t)] -= 1

    rec(0, 0, 0, 0)

    if best["cost"] is None:
        return {
            "status": "infeasible",
            "optimal": False,
            "message": "边容量不足，不存在满足全部批次需求的分配（已证不可行）",
        }
    return _build_solution(inst, paths_per_batch, pcosts, best["assign"], best["cost"])


def _build_solution(inst, paths_per_batch, pcosts, assign, total_cost) -> dict:
    batches_out, usage = [], defaultdict(int)
    for b, ps, costs, counts in zip(inst.batches, paths_per_batch, pcosts, assign):
        routes = []
        for idx, cnt in enumerate(counts):
            if cnt == 0:
                continue
            steps = []
            for ei, t in ps[idx]:
                e = inst.edges[ei]
                steps.append({"edge": e.id, "from": e.src, "to": e.dst,
                              "depart": t, "arrive": t + e.duration})
                usage[(e.id, t)] += cnt
            arrival = steps[-1]["arrive"] if steps else b.departure
            routes.append({"amount": cnt, "arrival": arrival,
                           "cost": costs[idx], "steps": steps})
        batches_out.append({"id": b.id, "quantity": b.quantity, "routes": routes})
    cap = {e.id: e.capacity for e in inst.edges}
    edge_usage = [{"edge": eid, "depart": t, "used": u, "capacity": cap[eid]}
                  for (eid, t), u in sorted(usage.items())]
    return {
        "status": "optimal",
        "optimal": True,
        "total_cost": total_cost,
        "batches": batches_out,
        "edge_usage": edge_usage,
        "capacity_ok": all(u["used"] <= u["capacity"] for u in edge_usage),
    }


# ---------------------------------------------------------------- 解的核验

def verify(inst: Instance, sol: dict) -> tuple:
    """独立核验解：批次需求、路径连通与时序、过期约束、容量守恒、总成本。

    返回 (ok, errors)。
    """
    errors = []
    edge_by_id = {e.id: e for e in inst.edges}
    batch_by_id = {b.id: b for b in inst.batches}
    usage, total, seen = defaultdict(int), 0, set()

    for bs in sol.get("batches", []):
        b = batch_by_id.get(bs.get("id"))
        if b is None:
            errors.append(f"未知批次: {bs.get('id')}")
            continue
        seen.add(b.id)
        amount_sum = 0
        for r in bs.get("routes", []):
            amount = r.get("amount", 0)
            if not isinstance(amount, int) or amount < 1:
                errors.append(f"批次 {b.id} 的路线 amount 非法: {amount}")
                continue
            amount_sum += amount
            node, time, rcost = b.origin, b.departure, 0
            for s in r.get("steps", []):
                e = edge_by_id.get(s.get("edge"))
                if e is None:
                    errors.append(f"批次 {b.id} 使用未知边: {s.get('edge')}")
                    break
                if s.get("from") != node or e.src != node or e.dst != s.get("to"):
                    errors.append(f"批次 {b.id} 路径在 {node} 处不连通")
                    break
                if s.get("depart") < time:
                    errors.append(f"批次 {b.id} 在边 {e.id} 上出发早于到达（等待为负）")
                    break
                if s.get("arrive") != s.get("depart") + e.duration:
                    errors.append(f"批次 {b.id} 边 {e.id} 的到达时刻与时长不符")
                    break
                usage[(e.id, s["depart"])] += amount
                rcost += e.cost
                node, time = e.dst, s["arrive"]
            if node != b.destination:
                errors.append(f"批次 {b.id} 未到达目的地 {b.destination}")
            if time >= b.expiry:
                errors.append(f"批次 {b.id} 到达时刻 {time} 不早于失效时刻 {b.expiry}（过期）")
            if inst.horizon is not None and time > inst.horizon:
                errors.append(f"批次 {b.id} 到达时刻 {time} 超过时间轴上界 {inst.horizon}")
            if r.get("arrival") != time:
                errors.append(f"批次 {b.id} 声明的 arrival 与实际不符")
            if r.get("cost") != rcost:
                errors.append(f"批次 {b.id} 声明的单位成本与实际不符")
            total += rcost * amount
        if amount_sum != b.quantity:
            errors.append(f"批次 {b.id} 交付 {amount_sum} != 需求 {b.quantity}（流量不守恒）")
    for b in inst.batches:
        if b.quantity > 0 and b.id not in seen:
            errors.append(f"批次 {b.id} 缺失")
    for (eid, t), u in usage.items():
        if u > edge_by_id[eid].capacity:
            errors.append(f"边 {eid} 时刻 {t} 使用 {u} 超过容量 "
                          f"{edge_by_id[eid].capacity}（容量不守恒）")
    if sol.get("total_cost") != total:
        errors.append(f"声明总成本 {sol.get('total_cost')} != 实际 {total}")
    return (not errors, errors)


# ---------------------------------------------------------------- CLI

def main(argv=None) -> int:
    ap = argparse.ArgumentParser(
        prog="perishable_flow",
        description="保质期转运网络最小成本流精确求解器（Python 3.11 标准库）")
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("solve", help="求解 JSON 实例")
    s.add_argument("input", help="输入 JSON 文件（'-' 表示标准输入）")
    s.add_argument("-o", "--output", help="输出 JSON 文件（默认标准输出）")
    s.add_argument("--max-paths", type=int, default=100_000,
                   help="单批可行路径数上限，超过则中止并标记未证最优")
    v = sub.add_parser("verify", help="核验解 JSON")
    v.add_argument("input", help="实例 JSON 文件")
    v.add_argument("solution", help="解 JSON 文件")
    args = ap.parse_args(argv)

    if args.cmd == "solve":
        try:
            raw = sys.stdin.read() if args.input == "-" else open(args.input).read()
            inst = instance_from_dict(json.loads(raw))
        except (OSError, json.JSONDecodeError, KeyError, ValueError) as exc:
            print(f"输入非法: {exc}", file=sys.stderr)
            return 2
        sol = solve(inst, max_paths=args.max_paths)
        if sol["status"] == "optimal":
            ok, errs = verify(inst, sol)
            sol["verified"] = ok
            if not ok:
                sol["verify_errors"] = errs
        out = json.dumps(sol, ensure_ascii=False, indent=2)
        if args.output:
            with open(args.output, "w") as f:
                f.write(out + "\n")
        else:
            print(out)
        return {"optimal": 0, "infeasible": 1}.get(sol["status"], 2)

    inst = instance_from_dict(json.load(open(args.input)))
    sol = json.load(open(args.solution))
    ok, errs = verify(inst, sol)
    print(json.dumps({"verified": ok, "errors": errs}, ensure_ascii=False, indent=2))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

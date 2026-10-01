#!/usr/bin/env python3
"""非交换迁移变更排序调度器。

数据库迁移用抽象读集合、写集合、显式前置依赖表示。两个迁移的
读/写或写/写集合相交（含写/读）时，必须保持原始相对顺序（非交换）；
其余迁移可并行。给定每轮并行上限 k，生成满足约束且轮次最少
（单位工时）的执行计划。

输入格式（JSON 文件，或 '-' 表示标准输入）::

    {
      "k": 2,
      "migrations": [
        {"name": "A", "reads": [], "writes": ["x"], "deps": []},
        {"name": "B", "reads": ["x"], "writes": []},
        {"name": "C", "writes": ["y"]}
      ]
    }

  - name   : 迁移唯一名称（必填）；列表顺序即原始相对顺序。
  - reads  : 读集合，字符串数组，可省略，默认空。
  - writes : 写集合，字符串数组，可省略，默认空。
  - deps   : 显式前置依赖（名称数组），可省略，默认空。
  - k      : 每轮最多执行的迁移数（>=1），也可用 --k 覆盖。

输出格式（JSON，写往标准输出）::

    {
      "k": 2,
      "migration_count": 3,
      "precedence_edges": [["A", "B"]],
      "rounds": [["A", "C"], ["B"]],
      "round_count": 2,
      "lower_bound": 2,
      "optimal": true,
      "optimality_note": "...",
      "greedy_round_count": 2,
      "evidence": {"ok": true, "checks": [...], "ready_per_round": [...]},
      "budget": {"budget_rounds": 1, "lower_completed": 2,
                 "upper_completed": 2, "exact_max_completed": 2, "exact": true}
    }

命令行用法::

    python3 migration_scheduler.py examples/acceptance.json
    python3 migration_scheduler.py examples/acceptance.json --budget 1
    python3 migration_scheduler.py - --k 2 < input.json

仅使用 Python 3.11 标准库。
"""
from __future__ import annotations

import argparse
import json
import math
import pathlib
import sys
from dataclasses import dataclass
from functools import lru_cache
from itertools import combinations


@dataclass(frozen=True)
class Migration:
    """一次迁移：名称、读集合、写集合、显式前置依赖。"""

    name: str
    reads: frozenset = frozenset()
    writes: frozenset = frozenset()
    deps: tuple = ()


def conflicts(earlier: Migration, later: Migration) -> bool:
    """earlier 与 later 是否存在读/写、写/读或写/写相交（读/读不算）。"""
    return bool(
        (earlier.writes & (later.reads | later.writes))
        or (earlier.reads & later.writes)
    )


def build_predecessors(migrations):
    """构建前置关系 {name: set(pred_names)} = 冲突保序边 + 显式依赖。"""
    names = [m.name for m in migrations]
    if len(set(names)) != len(names):
        raise ValueError("迁移名称重复")
    known = set(names)
    preds = {n: set() for n in names}
    for m in migrations:
        for d in m.deps:
            if d not in known:
                raise ValueError(f"未知前置依赖: {d!r}")
            if d == m.name:
                raise ValueError(f"迁移 {d!r} 不能依赖自身")
            preds[m.name].add(d)
    for i in range(len(migrations)):
        for j in range(i + 1, len(migrations)):
            if conflicts(migrations[i], migrations[j]):
                preds[names[j]].add(names[i])
    return preds


def topo_order(migrations, preds):
    """按原始顺序破缺的拓扑排序；检测显式依赖造成的环。"""
    names = [m.name for m in migrations]
    done = set()
    order = []
    while len(order) < len(names):
        for n in names:
            if n not in done and preds[n] <= done:
                done.add(n)
                order.append(n)
                break
        else:
            raise ValueError("前置依赖存在环，无法排序")
    return order


def compute_depths(names, preds):
    """每个迁移的最早轮次下标（最长前置链长度）。"""
    depth = {}

    def d(n):
        if n not in depth:
            depth[n] = 0 if not preds[n] else 1 + max(d(p) for p in preds[n])
        return depth[n]

    for n in names:
        d(n)
    return depth


def lower_bound_rounds(names, k, preds):
    """轮次下界：max(ceil(n/k), 关键路径长度)。"""
    if not names:
        return 0
    depth = compute_depths(names, preds)
    return max(math.ceil(len(names) / k), max(depth.values()) + 1)


def greedy_schedule(migrations, k, preds):
    """列表调度：每轮按原始顺序取至多 k 个就绪迁移。"""
    names = [m.name for m in migrations]
    done = set()
    rounds = []
    while len(done) < len(names):
        ready = [n for n in names if n not in done and preds[n] <= done]
        if not ready:
            raise ValueError("前置依赖存在环，无法调度")
        take = ready[:k]
        rounds.append(take)
        done.update(take)
    return rounds


def _bit_setup(migrations, preds):
    names = [m.name for m in migrations]
    index = {n: i for i, n in enumerate(names)}
    pred_mask = [0] * len(names)
    for i, m in enumerate(migrations):
        for p in preds[m.name]:
            pred_mask[i] |= 1 << index[p]
    return names, pred_mask


def _ready_indices(state, pred_mask):
    return [
        i
        for i in range(len(pred_mask))
        if not (state >> i) & 1 and (pred_mask[i] & ~state) == 0
    ]


def exact_min_rounds(migrations, k, preds):
    """精确最少轮次（记忆化搜索 + 下界剪枝），返回 (轮次, 计划)。

    单位工时 + 前置约束下，存在某最优计划每轮都取满 min(k, |ready|) 个
    （就绪而未选的任务必在更晚轮次，提前之不会更差），故只需枚举
    就绪集合的大小为 min(k, |ready|) 的子集。
    """
    names, pred_mask = _bit_setup(migrations, preds)
    n = len(names)
    full = (1 << n) - 1

    @lru_cache(maxsize=None)
    def dp(state):
        if state == full:
            return 0
        ready = _ready_indices(state, pred_mask)
        take = min(k, len(ready))
        remaining = n - state.bit_count()
        best = math.inf
        for subset in combinations(ready, take):
            ns = state
            for i in subset:
                ns |= 1 << i
            best = min(best, 1 + dp(ns))
        # 下界剪枝的静态下界（剩余任务数 / k）由调用结构天然满足
        return best

    total = dp(0)
    schedule = []
    state = 0
    while state != full:
        ready = _ready_indices(state, pred_mask)
        take = min(k, len(ready))
        for subset in combinations(ready, take):
            ns = state
            for i in subset:
                ns |= 1 << i
            if 1 + dp(ns) == dp(state):
                schedule.append([names[i] for i in subset])
                state = ns
                break
    return total, schedule


def max_tasks_within(migrations, k, preds, budget_rounds):
    """预算中断精确解：T 轮内最多可完成的迁移数（按轮 BFS 状态集）。"""
    names, pred_mask = _bit_setup(migrations, preds)
    states = {0}
    best = 0
    for _ in range(budget_rounds):
        nxt = set()
        for s in states:
            ready = _ready_indices(s, pred_mask)
            take = min(k, len(ready))
            if take == 0:
                nxt.add(s)
                continue
            for subset in combinations(ready, take):
                ns = s
                for i in subset:
                    ns |= 1 << i
                nxt.add(ns)
        states = nxt
        best = max(best, max(s.bit_count() for s in states))
    return best


def budget_bounds(migrations, k, preds, budget_rounds, exact_limit=20):
    """预算中断上下界：T 轮预算内可完成迁移数的下界/上界/精确值。"""
    names = [m.name for m in migrations]
    n = len(names)
    depth = compute_depths(names, preds)
    greedy = greedy_schedule(migrations, k, preds)
    lower = sum(len(r) for r in greedy[:budget_rounds])
    # 上界：容量 k*T、总数 n、以及 T 轮内最早可启动（深度 < T）的任务数
    reachable = sum(1 for name in names if depth[name] < budget_rounds)
    upper = min(n, k * budget_rounds, reachable)
    exact = None
    if n <= exact_limit:
        exact = max_tasks_within(migrations, k, preds, budget_rounds)
    return {
        "budget_rounds": budget_rounds,
        "lower_completed": lower,
        "upper_completed": upper,
        "exact_max_completed": exact,
        "exact": exact is not None,
    }


def verify_schedule(migrations, k, preds, rounds):
    """可行调度证据：逐项检查并给出每轮就绪集合。"""
    names = [m.name for m in migrations]
    checks = []
    ok = True

    def check(label, passed, detail):
        nonlocal ok
        ok = ok and passed
        checks.append({"check": label, "ok": passed, "detail": detail})

    flat = [n for r in rounds for n in r]
    check(
        "每个迁移恰好调度一次",
        sorted(flat) == sorted(names),
        f"scheduled={len(flat)} expected={len(names)}",
    )
    oversize = [i for i, r in enumerate(rounds) if not 1 <= len(r) <= k]
    check(
        "每轮规模在 [1, k] 内",
        not oversize,
        f"k={k} 违规轮次={oversize}",
    )
    pos = {n: i for i, r in enumerate(rounds) for n in r}
    violations = [
        [p, n]
        for n in names
        for p in preds[n]
        if pos.get(p, -1) >= pos.get(n, len(rounds) + 1)
    ]
    check(
        "前置约束（冲突保序 + 显式依赖）全部满足",
        not violations,
        f"违规边={violations}",
    )
    done = set()
    ready_per_round = []
    for r in rounds:
        ready_per_round.append(
            [n for n in names if n not in done and preds[n] <= done]
        )
        done.update(r)
    return {"ok": ok, "checks": checks, "ready_per_round": ready_per_round}


def solve(migrations, k, exact_limit=20, budget=None):
    """完整求解：前置关系、最优/贪心计划、下界、验证证据、预算界。"""
    if k < 1:
        raise ValueError("k 必须 >= 1")
    preds = build_predecessors(migrations)
    topo_order(migrations, preds)  # 环检测
    names = [m.name for m in migrations]
    lb = lower_bound_rounds(names, k, preds)
    greedy = greedy_schedule(migrations, k, preds)

    edges = sorted(
        [p, n] for n in names for p in preds[n]
    )
    result = {
        "k": k,
        "migration_count": len(names),
        "precedence_edges": edges,
        "lower_bound": lb,
        "greedy_round_count": len(greedy),
    }
    if len(names) <= exact_limit:
        total, schedule = exact_min_rounds(migrations, k, preds)
        result.update(
            rounds=schedule,
            round_count=total,
            optimal=True,
            optimality_note=(
                f"精确搜索证明最优：{total} 轮（下界 {lb}，"
                f"贪心 {len(greedy)} 轮）"
            ),
        )
    else:
        result.update(
            rounds=greedy,
            round_count=len(greedy),
            optimal=False,
            optimality_note=(
                f"规模 {len(names)} 超过精确上限 {exact_limit}，"
                f"使用贪心结果 {len(greedy)} 轮，未证最优（下界 {lb}）"
            ),
        )
    result["evidence"] = verify_schedule(migrations, k, preds, result["rounds"])
    if budget is not None:
        result["budget"] = budget_bounds(
            migrations, k, preds, budget, exact_limit=exact_limit
        )
    return result


def load_migrations(data):
    migrations = []
    for item in data.get("migrations", []):
        migrations.append(
            Migration(
                name=item["name"],
                reads=frozenset(item.get("reads", [])),
                writes=frozenset(item.get("writes", [])),
                deps=tuple(item.get("deps", [])),
            )
        )
    return migrations


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="非交换迁移变更排序：最少轮次并行调度"
    )
    parser.add_argument("input", help="输入 JSON 文件路径，'-' 表示标准输入")
    parser.add_argument("--k", type=int, default=None, help="每轮最大并行数（覆盖输入 k）")
    parser.add_argument("--budget", type=int, default=None, metavar="T",
                        help="预算中断：可用轮次上限 T，输出可完成数上下界")
    parser.add_argument("--exact-limit", type=int, default=20, metavar="N",
                        help="精确搜索的迁移数上限（默认 20）")
    parser.add_argument("--no-exact", action="store_true", help="禁用精确搜索，只用贪心")
    args = parser.parse_args(argv)

    raw = (
        sys.stdin.read()
        if args.input == "-"
        else pathlib.Path(args.input).read_text(encoding="utf-8")
    )
    data = json.loads(raw)
    k = args.k if args.k is not None else data.get("k")
    if not isinstance(k, int) or isinstance(k, bool) or k < 1:
        parser.error("k 必须是 >= 1 的整数（输入 JSON 或 --k 提供）")
    migrations = load_migrations(data)
    exact_limit = 0 if args.no_exact else args.exact_limit
    try:
        result = solve(migrations, k, exact_limit=exact_limit, budget=args.budget)
    except ValueError as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False), file=sys.stderr)
        return 2
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())

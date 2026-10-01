"""非交换迁移变更排序：冲突检测、前置依赖图与最少轮次调度。

迁移以抽象读集合、写集合与显式前置依赖表示。任意两个迁移的读写或
写写集合相交（任一方向）时，必须保持其原始相对顺序；其余情况可并行。
所有迁移单位工时，每轮至多 k 项，目标为最少轮次。
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import combinations
from math import ceil


class CycleError(ValueError):
    """显式依赖与冲突顺序边共同构成循环时抛出。"""


@dataclass(frozen=True)
class Migration:
    """一次数据库迁移的抽象表示。"""

    name: str
    reads: frozenset = frozenset()
    writes: frozenset = frozenset()
    deps: tuple = ()


def conflicts(a: Migration, b: Migration) -> bool:
    """读写或写写任一方向相交即冲突（读读不相交不冲突）。"""
    return bool(
        a.writes & (b.reads | b.writes) or b.writes & (a.reads | a.writes)
    )


def _topo_order(preds):
    """Kahn 拓扑排序；同时返回后继表。存在循环时抛 CycleError。"""
    n = len(preds)
    succs = [[] for _ in range(n)]
    indeg = [0] * n
    for j in range(n):
        indeg[j] = len(preds[j])
        for i in preds[j]:
            succs[i].append(j)
    queue = [i for i in range(n) if indeg[i] == 0]
    order = []
    head = 0
    while head < len(queue):
        v = queue[head]
        head += 1
        order.append(v)
        for w in succs[v]:
            indeg[w] -= 1
            if indeg[w] == 0:
                queue.append(w)
    if len(order) != n:
        raise CycleError("显式依赖与冲突顺序约束构成循环，无法排序")
    return order, succs


def build_predecessors(migrations):
    """构造前置约束。

    返回 (preds, conflict_edges, explicit_edges)：
    preds[j] 为必须排在 j 之前的迁移下标集合；冲突边按原始顺序由前指向后。
    """
    n = len(migrations)
    index = {}
    for i, m in enumerate(migrations):
        if m.name in index:
            raise ValueError(f"迁移名重复: {m.name!r}")
        index[m.name] = i

    preds = [set() for _ in range(n)]
    explicit_edges = []
    for j, m in enumerate(migrations):
        for dep in m.deps:
            if dep not in index:
                raise ValueError(f"迁移 {m.name!r} 依赖未知迁移 {dep!r}")
            i = index[dep]
            if i == j:
                raise ValueError(f"迁移 {m.name!r} 不能依赖自身")
            if i not in preds[j]:
                preds[j].add(i)
                explicit_edges.append((i, j))

    conflict_edges = []
    for i in range(n):
        for j in range(i + 1, n):
            if conflicts(migrations[i], migrations[j]):
                preds[j].add(i)
                conflict_edges.append((i, j))

    _topo_order(preds)
    return preds, conflict_edges, explicit_edges


def critical_path_len(preds) -> int:
    """前置 DAG 上最长链的节点数（轮次的下界之一）。"""
    if not preds:
        return 0
    order, _ = _topo_order(preds)
    dist = [0] * len(preds)
    for v in order:
        best = 1
        for u in preds[v]:
            if dist[u] + 1 > best:
                best = dist[u] + 1
        dist[v] = best
    return max(dist)


def lower_bound(preds, k: int) -> int:
    """静态下界：max(ceil(n/k), 关键路径长度)。"""
    n = len(preds)
    if n == 0:
        return 0
    return max(ceil(n / k), critical_path_len(preds))


def _reconstruct(parent, state):
    rounds = []
    while parent[state] is not None:
        prev, combo = parent[state]
        rounds.append(list(combo))
        state = prev
    rounds.reverse()
    return rounds


def exact_schedule(preds, k: int, budget: int | None = None):
    """状态空间 BFS 精确求解最少轮次。

    利用“无空闲支配”性质：存在最优调度使得只要存在就绪迁移就不留空槽，
    故每轮只需枚举 min(k, 就绪数) 大小的子集。

    返回 (rounds, expanded, proven_lb)。若扩展状态数超过 budget 而中断，
    rounds 为 None，proven_lb 为已证明的轮次下界（已完整排除的层数 + 1）。
    """
    n = len(preds)
    if n == 0:
        return [], 0, 0
    pre_mask = [0] * n
    for j in range(n):
        mask = 0
        for i in preds[j]:
            mask |= 1 << i
        pre_mask[j] = mask
    full = (1 << n) - 1
    parent = {0: None}
    frontier = [0]
    rounds = 0
    expanded = 0
    while frontier:
        nxt = {}
        for state in frontier:
            if budget is not None and expanded >= budget:
                return None, expanded, rounds + 1
            expanded += 1
            avail = [
                i
                for i in range(n)
                if not (state >> i) & 1 and pre_mask[i] & ~state == 0
            ]
            if not avail:
                continue
            take = min(k, len(avail))
            for combo in combinations(avail, take):
                ns = state
                for i in combo:
                    ns |= 1 << i
                if ns in parent or ns in nxt:
                    continue
                nxt[ns] = (state, combo)
                if ns == full:
                    parent.update(nxt)
                    plan_rounds = _reconstruct(parent, full)
                    return plan_rounds, expanded, len(plan_rounds)
        parent.update(nxt)
        frontier = list(nxt)
        rounds += 1
    raise AssertionError("DAG 上 BFS 必然可达终态，不应执行到这里")


def list_schedule(preds, k: int):
    """关键路径优先的列表调度启发式，产出可行调度（上界）。"""
    n = len(preds)
    if n == 0:
        return []
    order, succs = _topo_order(preds)
    cp = [1] * n
    for v in reversed(order):
        for w in succs[v]:
            if cp[w] + 1 > cp[v]:
                cp[v] = cp[w] + 1
    done = [False] * n
    done_count = 0
    rounds = []
    while done_count < n:
        ready = [
            i
            for i in range(n)
            if not done[i] and all(done[p] for p in preds[i])
        ]
        ready.sort(key=lambda i: (-cp[i], i))
        take = ready[:k]
        rounds.append(take)
        for i in take:
            done[i] = True
        done_count += len(take)
    return rounds


def verify_schedule(migrations, preds, rounds, k: int) -> dict:
    """校验调度可行性：恰好一次、容量、前置约束。返回证据字典。"""
    errors = []
    n = len(migrations)
    round_of = {}
    for r, rnd in enumerate(rounds):
        if not rnd:
            errors.append(f"第 {r + 1} 轮为空轮")
        if len(rnd) > k:
            errors.append(f"第 {r + 1} 轮含 {len(rnd)} 项，超过容量 k={k}")
        for i in rnd:
            if not isinstance(i, int) or not 0 <= i < n:
                errors.append(f"第 {r + 1} 轮含非法迁移下标 {i!r}")
                continue
            if i in round_of:
                errors.append(
                    f"迁移 {migrations[i].name!r} 被重复调度"
                )
            round_of[i] = r
    for i in range(n):
        if i not in round_of:
            errors.append(f"迁移 {migrations[i].name!r} 未被调度")
    checked = 0
    for j in range(n):
        for i in preds[j]:
            if i in round_of and j in round_of:
                checked += 1
                if round_of[i] >= round_of[j]:
                    errors.append(
                        f"前置约束被违反: {migrations[i].name!r} 必须早于 "
                        f"{migrations[j].name!r}（第 {round_of[i] + 1} 轮 vs "
                        f"第 {round_of[j] + 1} 轮）"
                    )
    return {
        "ok": not errors,
        "errors": errors,
        "checked_precedence_edges": checked,
        "scheduled": len(round_of),
        "total": n,
    }


def plan(migrations, k: int, exact_limit: int = 16, budget: int = 200000) -> dict:
    """生成调度计划及证据。返回可 JSON 序列化的结果字典。"""
    if k < 1:
        raise ValueError("k 必须为正整数")
    preds, conflict_edges, explicit_edges = build_predecessors(migrations)
    n = len(migrations)
    lb = lower_bound(preds, k)
    notes = []
    rounds = None
    expanded = 0

    if n <= exact_limit:
        exact_rounds, expanded, proven_lb = exact_schedule(preds, k, budget)
        if exact_rounds is not None:
            rounds = exact_rounds
            status = "optimal"
            lb = len(rounds)
            notes.append("精确搜索完成，轮次数已证明最优。")
        else:
            status = "interrupted"
            lb = max(lb, proven_lb)
            notes.append(
                f"精确搜索在扩展 {expanded} 个状态后超出预算 budget={budget} "
                f"而中断，结果未证最优；已证明下界 {proven_lb}。"
            )
    else:
        status = "heuristic"
        notes.append(
            f"规模 n={n} 超过精确求解上限 exact_limit={exact_limit}，"
            "改用关键路径优先列表调度，结果未证最优。"
        )

    if rounds is None:
        rounds = list_schedule(preds, k)

    verification = verify_schedule(migrations, preds, rounds, k)
    ub = len(rounds)
    optimal = status == "optimal"
    if not optimal and ub == lb:
        optimal = True
        notes.append("启发式上界与下界吻合，轮次数间接证明最优。")
    if not verification["ok"]:
        notes.append("警告：调度未通过可行性校验！")

    names = [[migrations[i].name for i in rnd] for rnd in rounds]
    return {
        "status": status,
        "optimal": optimal,
        "k": k,
        "num_migrations": n,
        "rounds": names,
        "num_rounds": ub,
        "lower_bound": lb,
        "upper_bound": ub,
        "gap": ub - lb,
        "states_expanded": expanded,
        "conflict_edges": [
            [migrations[i].name, migrations[j].name] for i, j in conflict_edges
        ],
        "explicit_edges": [
            [migrations[i].name, migrations[j].name] for i, j in explicit_edges
        ],
        "critical_path_rounds": critical_path_len(preds),
        "verification": verification,
        "notes": notes,
    }

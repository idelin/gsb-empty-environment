"""约束式抽签可复核分配。

给定候选人（带组别）、奖项（带组别配额）、每人最多一奖、禁配边：
1. 用指定整数种子驱动固定定义的 PRNG（SplitMix64），为所有候选边生成确定性全序；
2. 在所有"最大基数"可行分配中，选取按该全序排列后边序列字典序最小者。

仅使用 Python 3.11 标准库；不依赖任何运行时随机 hash 排序，跨重启可复现。
"""

from __future__ import annotations

import argparse
import json
import sys

MASK64 = (1 << 64) - 1


class SplitMix64:
    """固定整数 PRNG：SplitMix64（与语言/进程无关的纯整数定义）。

    状态转移: state <- state + 0x9E3779B97F4A7C15 (mod 2^64)
    输出函数: 对 state 做两轮 xor-shift 与乘法混合（常数见下），均为 64 位无符号运算。
    给定相同整数种子，任意机器、任意次重启后输出序列完全一致。
    """

    def __init__(self, seed: int):
        if not isinstance(seed, int) or isinstance(seed, bool):
            raise TypeError("seed 必须为整数")
        self._state = seed & MASK64

    def next(self) -> int:
        """返回下一个 64 位无符号整数。"""
        self._state = (self._state + 0x9E3779B97F4A7C15) & MASK64
        z = self._state
        z = ((z ^ (z >> 30)) * 0xBF58476D1CE4E5B9) & MASK64
        z = ((z ^ (z >> 27)) * 0x94D049BB133111EB) & MASK64
        return (z ^ (z >> 31)) & MASK64


def edge_total_order(edges, seed):
    """为候选边集合生成确定性全序。

    按 (candidate, award) 字典序遍历边并依次取 PRNG 输出作为排序键，
    再按 (key, candidate, award) 排序。排序键冲突时用边自身字典序兜底，
    因此结果是严格全序，且与 set/dict 的迭代顺序无关。
    """
    prng = SplitMix64(seed)
    keyed = [(prng.next(), cand, award) for cand, award in sorted(edges)]
    keyed.sort(key=lambda item: (item[0], item[1], item[2]))
    return [(cand, award) for _, cand, award in keyed]


class _Dinic:
    """Dinic 最大流（标准库实现，用于 b-匹配可行性判定）。"""

    def __init__(self, n):
        self._g = [[] for _ in range(n)]

    def add_edge(self, u, v, cap):
        self._g[u].append([v, cap, len(self._g[v])])
        self._g[v].append([u, 0, len(self._g[u]) - 1])

    def max_flow(self, s, t):
        flow = 0
        n = len(self._g)
        while True:
            level = [-1] * n
            level[s] = 0
            queue = [s]
            for u in queue:
                for v, cap, _rev in self._g[u]:
                    if cap > 0 and level[v] < 0:
                        level[v] = level[u] + 1
                        queue.append(v)
            if level[t] < 0:
                return flow
            it = [0] * n

            def dfs(u, f):
                if u == t:
                    return f
                while it[u] < len(self._g[u]):
                    i = it[u]
                    v, cap, rev = self._g[u][i]
                    if cap > 0 and level[v] == level[u] + 1:
                        d = dfs(v, min(f, cap))
                        if d > 0:
                            self._g[u][i][1] -= d
                            self._g[v][rev][1] += d
                            return d
                    it[u] += 1
                return 0

            while True:
                pushed = dfs(s, 1 << 62)
                if pushed == 0:
                    break
                flow += pushed


def _max_assignment_size(alive_candidates, group_of, award_ids, quotas, edges):
    """当前剩余问题下的最大可分配边数。

    网络：源 -> 候选人(容量1，每人最多一奖)；
    候选人 -> (奖项, 其组别) 槽位(容量1，边存在才连)；
    槽位 -> 汇(容量=该奖项对该组别的剩余配额)。
    """
    index = {}

    def node(name):
        if name not in index:
            index[name] = len(index)
        return index[name]

    src = node(("src",))
    dst = node(("dst",))
    alive = set(alive_candidates)
    arcs = []
    for c in sorted(alive):
        arcs.append((src, node(("cand", c)), 1))
    for a in award_ids:
        for g in sorted(quotas[a]):
            if quotas[a][g] > 0:
                arcs.append((node(("slot", a, g)), dst, quotas[a][g]))
    for c, a in sorted(edges):
        if c in alive and quotas[a].get(group_of[c], 0) > 0:
            arcs.append((node(("cand", c)), node(("slot", a, group_of[c])), 1))
    dinic = _Dinic(len(index))
    for u, v, cap in arcs:
        dinic.add_edge(u, v, cap)
    return dinic.max_flow(src, dst)


def solve(spec):
    """求解并返回可复核结果字典。输入 spec 格式见 README.md。"""
    seed = spec["seed"]
    candidates = spec["candidates"]
    awards = spec["awards"]
    forbidden = {tuple(pair) for pair in spec.get("forbidden", [])}

    group_of = {}
    for c in candidates:
        if c["id"] in group_of:
            raise ValueError(f"候选人 id 重复: {c['id']!r}")
        group_of[c["id"]] = c["group"]
    candidate_ids = sorted(group_of)

    quotas = {}
    for a in awards:
        if a["id"] in quotas:
            raise ValueError(f"奖项 id 重复: {a['id']!r}")
        quotas[a["id"]] = {g: int(q) for g, q in a["quotas"].items()}
    award_ids = sorted(quotas)

    edges = set()
    for c in candidates:
        for a in c["awards"]:
            if a not in quotas:
                raise ValueError(f"候选人 {c['id']!r} 引用了未知奖项 {a!r}")
            if (c["id"], a) in forbidden:
                continue
            if quotas[a].get(c["group"], 0) <= 0:
                continue
            edges.add((c["id"], a))

    order = edge_total_order(edges, seed)
    rank = {edge: i for i, edge in enumerate(order)}

    alive = set(candidate_ids)
    live_quotas = {a: dict(q) for a, q in quotas.items()}
    live_edges = set(edges)

    max_size = _max_assignment_size(alive, group_of, award_ids, live_quotas, live_edges)

    # 在全部最大匹配中选字典序最小者：按全序逐边尝试，
    # 若"强制选入该边后仍存在最大匹配"则选入，否则跳过。
    chosen = []
    for edge in order:
        c, a = edge
        if c not in alive or edge not in live_edges:
            continue
        g = group_of[c]
        if live_quotas[a].get(g, 0) <= 0:
            continue
        trial_alive = alive - {c}
        trial_quotas = {k: dict(v) for k, v in live_quotas.items()}
        trial_quotas[a][g] -= 1
        trial_edges = {e for e in live_edges if e[0] != c}
        rest = _max_assignment_size(
            trial_alive, group_of, award_ids, trial_quotas, trial_edges
        )
        if len(chosen) + 1 + rest == max_size:
            chosen.append(edge)
            alive = trial_alive
            live_quotas = trial_quotas
            live_edges = trial_edges

    return {
        "seed": seed,
        "prng": "splitmix64",
        "edge_order": [[c, a] for c, a in order],
        "matching": [[c, a] for c, a in sorted(chosen, key=lambda e: rank[e])],
        "matching_size": len(chosen),
        "maximum_matching_size": max_size,
        "is_maximum": len(chosen) == max_size,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="lottery",
        description="约束式抽签可复核分配：种子生成候选边全序，"
        "在所有最大匹配中取边序列字典序最小者。",
    )
    parser.add_argument("input", nargs="?", help="输入 JSON 文件；缺省读标准输入")
    parser.add_argument("-o", "--output", help="输出 JSON 文件；缺省写标准输出")
    args = parser.parse_args(argv)

    if args.input:
        with open(args.input, encoding="utf-8") as fh:
            spec = json.load(fh)
    else:
        spec = json.load(sys.stdin)

    result = solve(spec)
    text = json.dumps(result, ensure_ascii=False, indent=2)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    else:
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())

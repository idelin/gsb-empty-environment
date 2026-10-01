"""最短问题链：为每个不可生成符号给出通往"根因"的最短解释链。

根因有两类：
- 无任何产生式的符号；
- 不可生成依赖图中循环 SCC 上的符号（互相依赖、无终结出口）。
链上的每一步 X ⇒ αYβ 由一条真实产生式佐证。
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .cycles import tarjan_scc
from .grammar import Grammar


@dataclass
class ProblemChain:
    symbol: str
    steps: list[tuple[int, str, str]]  # (产生式下标, 当前符号, 下一符号)
    root: str
    root_kind: str                     # "no_rules" | "cycle"
    root_cycle: tuple[str, ...] | None # root_kind == "cycle" 时的 SCC

    def describe(self, grammar: Grammar) -> str:
        lines = []
        for pidx, cur, nxt in self.steps:
            lines.append(f"  {cur} ⇒ …{nxt}…   （经 #{pidx}: {grammar.productions[pidx]}）")
        if self.root_kind == "no_rules":
            lines.append(f"  根因: {self.root!r} 没有任何产生式")
        else:
            cyc = " ⇒ ".join(self.root_cycle + (self.root_cycle[0],))
            lines.append(f"  根因: 循环依赖且无终结出口（环: {cyc}）")
        return "\n".join(lines)


def problem_chains(grammar: Grammar, non_generating: frozenset[str]) -> dict[str, ProblemChain]:
    ng = set(non_generating)
    if not ng:
        return {}
    # 不可生成依赖图：X → Y 当且仅当存在产生式 X→αYβ 且 Y 不可生成
    adj: dict[str, list[tuple[str, int]]] = {s: [] for s in ng}
    has_production: set[str] = set()
    for idx, prod in enumerate(grammar.productions):
        if prod.lhs in ng:
            has_production.add(prod.lhs)
            for s in prod.rhs:
                if s in ng:
                    adj[prod.lhs].append((s, idx))
    # 根因一：无产生式的符号；根因二：循环 SCC 成员
    plain_adj = {s: [t for t, _ in edges] for s, edges in adj.items()}
    cyclic: set[str] = set()
    scc_of: dict[str, tuple[str, ...]] = {}
    for scc in tarjan_scc(sorted(ng), plain_adj):
        is_cyc = len(scc) > 1 or scc[0] in plain_adj.get(scc[0], ())
        if is_cyc:
            key = tuple(sorted(scc))
            for s in scc:
                cyclic.add(s)
                scc_of[s] = key
    roots = {s for s in ng if s not in has_production} | cyclic

    def root_info(s: str) -> tuple[str, str, tuple[str, ...] | None]:
        if s in cyclic:
            return s, "cycle", scc_of[s]
        return s, "no_rules", None

    chains: dict[str, ProblemChain] = {}
    for sym in sorted(ng):
        if sym in roots:
            r, kind, cyc = root_info(sym)
            chains[sym] = ProblemChain(sym, [], r, kind, cyc)
            continue
        # BFS 求到最近根因的最短链
        prev: dict[str, tuple[str, int]] = {}
        queue = deque([sym])
        seen = {sym}
        hit = None
        while queue and hit is None:
            node = queue.popleft()
            for nxt, pidx in adj.get(node, ()):
                if nxt in seen:
                    continue
                prev[nxt] = (node, pidx)
                if nxt in roots:
                    hit = nxt
                    break
                seen.add(nxt)
                queue.append(nxt)
        if hit is None:
            # 有限图中不可生成符号必能到达根因；防御性兜底
            continue
        steps: list[tuple[int, str, str]] = []
        node = hit
        while node != sym:
            pnode, pidx = prev[node]
            steps.append((pidx, pnode, node))
            node = pnode
        steps.reverse()
        r, kind, cyc = root_info(hit)
        chains[sym] = ProblemChain(sym, steps, r, kind, cyc)
    return chains

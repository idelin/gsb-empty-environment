"""环分析：ε-环（可空上下文循环）与增长环分别诊断。

- ε-环：A ⇒+ αAβ 且 αβ ⇒* ε。若 A 有用 ⇒ 同一终结串有无限多推导树
  （无限歧义），但语言未必无限。
- 增长环：A ⇒+ αAβ 且 αβ 的最短终结产出非空。若 A 有用 ⇒ 语言无限
  （泵出无限多不同终结串）。
两者结构相似但结论不同，必须分别诊断。
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .core import YieldInfo, apply_step, nt_positions
from .grammar import Grammar


@dataclass(frozen=True)
class CycleEdge:
    """一步自嵌套：src ⇒ α dst β（经产生式 prod_index）。"""

    src: str
    dst: str
    prod_index: int
    rhs_index: int            # dst 在右部的下标
    context: tuple[str, ...]  # αβ（除 dst 外的全部符号）


def build_edges(grammar: Grammar, generating: frozenset[str]) -> list[CycleEdge]:
    """对每条全可生成产生式的每个非终结符出现位置建边。"""
    edges: list[CycleEdge] = []
    for idx, prod in enumerate(grammar.productions):
        if prod.lhs not in generating:
            continue
        if any(s not in grammar.terminals and s not in generating for s in prod.rhs):
            continue
        for j, s in enumerate(prod.rhs):
            if s in grammar.nonterminals:
                ctx = prod.rhs[:j] + prod.rhs[j + 1:]
                edges.append(CycleEdge(prod.lhs, s, idx, j, ctx))
    return edges


def tarjan_scc(nodes, adj) -> list[list[str]]:
    """迭代式 Tarjan 强连通分量（避免递归深度限制）。adj: 节点 -> 邻接节点列表。"""
    index_of: dict[str, int] = {}
    low: dict[str, int] = {}
    on_stack: set[str] = set()
    stack: list[str] = []
    result: list[list[str]] = []
    counter = 0
    for root in nodes:
        if root in index_of:
            continue
        index_of[root] = low[root] = counter
        counter += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(adj.get(root, ())))]
        while work:
            node, it = work[-1]
            descended = False
            for nxt in it:
                if nxt not in index_of:
                    index_of[nxt] = low[nxt] = counter
                    counter += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(adj.get(nxt, ()))))
                    descended = True
                    break
                elif nxt in on_stack:
                    low[node] = min(low[node], index_of[nxt])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                low[parent] = min(low[parent], low[node])
            if low[node] == index_of[node]:
                scc = []
                while True:
                    s = stack.pop()
                    on_stack.discard(s)
                    scc.append(s)
                    if s == node:
                        break
                result.append(scc)
    return result


def _is_cyclic(scc: list[str], adj) -> bool:
    if len(scc) > 1:
        return True
    return scc[0] in adj.get(scc[0], ())


@dataclass
class CycleWitness:
    """A ⇒+ γ A δ 的具体推导见证。"""

    symbol: str
    steps: list[tuple[int, int]]        # 推导步（产生式下标, 非终结符位置）
    left_context: tuple[str, ...]       # γ
    right_context: tuple[str, ...]      # δ
    scc: tuple[str, ...]

    def describe(self, grammar: Grammar) -> str:
        forms = [self.symbol]
        cur = [self.symbol]
        for step in self.steps:
            cur = apply_step(grammar, cur, step)
            forms.append(" ".join(cur) if cur else "ε")
        return " ⇒ ".join(forms)


def _find_path(adj_edges, start: str, goal: str, allowed: set[str],
               min_len: int) -> list[CycleEdge] | None:
    """BFS 求 start ~> goal 的最短边路径（全部节点 ∈ allowed，边数 ≥ min_len）。"""
    if start == goal and min_len == 0:
        return []
    # prev[节点] = (前驱节点, 边)
    prev: dict[str, tuple[str, CycleEdge]] = {}
    depth: dict[str, int] = {start: 0}
    queue = deque([start])
    while queue:
        node = queue.popleft()
        for e in adj_edges.get(node, ()):
            if e.dst not in allowed:
                continue
            nd = depth[node] + 1
            if e.dst == goal and nd >= min_len:
                path = [e]
                cur = node
                while cur != start:
                    psym, pe = prev[cur]
                    path.append(pe)
                    cur = psym
                path.reverse()
                return path
            if e.dst not in depth:
                depth[e.dst] = nd
                prev[e.dst] = (node, e)
                queue.append(e.dst)
    return None


def _witness_from_path(grammar: Grammar, start: str, path: list[CycleEdge],
                       scc: tuple[str, ...]) -> CycleWitness:
    """把边路径模拟成具体推导步序列（句型从 [start] 出发）。"""
    form = [start]
    steps: list[tuple[int, int]] = []
    dist_nt_pos = 0
    for e in path:
        steps.append((e.prod_index, dist_nt_pos))
        form = apply_step(grammar, form, steps[-1])
        prod = grammar.productions[e.prod_index]
        dist_nt_pos += sum(1 for s in prod.rhs[:e.rhs_index] if s in grammar.nonterminals)
    abs_pos = nt_positions(grammar, form)[dist_nt_pos]
    return CycleWitness(
        symbol=start,
        steps=steps,
        left_context=tuple(form[:abs_pos]),
        right_context=tuple(form[abs_pos + 1:]),
        scc=scc,
    )


@dataclass
class CycleAnalysis:
    epsilon_cycles: list[CycleWitness]   # 上下文可空的环（⇒ 无限歧义，若环上有用）
    growth_cycles: list[CycleWitness]    # 上下文可推出非空终结串的环（⇒ 语言无限，若环上有用）
    infinitely_ambiguous: bool
    language_infinite: bool


def analyze_cycles(grammar: Grammar, generating: frozenset[str],
                   nullable: frozenset[str], useful: frozenset[str],
                   yields: YieldInfo) -> CycleAnalysis:
    edges = build_edges(grammar, generating)

    def adj_of(edge_list) -> dict[str, list[str]]:
        adj: dict[str, list[str]] = {}
        for e in edge_list:
            adj.setdefault(e.src, []).append(e.dst)
        return adj

    def edge_map(edge_list) -> dict[str, list[CycleEdge]]:
        m: dict[str, list[CycleEdge]] = {}
        for e in edge_list:
            m.setdefault(e.src, []).append(e)
        return m

    def ctx_nullable(e: CycleEdge) -> bool:
        # 上下文全部是可空非终结符（终结符不在 nullable 中，自动排除）
        return all(s in nullable for s in e.context)

    def ctx_min_len(e: CycleEdge) -> int:
        total = 0
        for s in e.context:
            total += 1 if s in grammar.terminals else yields.best_len.get(s, 0)
        return total

    useful_edges = [e for e in edges if e.src in useful and e.dst in useful]
    eps_edges = [e for e in useful_edges if ctx_nullable(e)]
    growth_edges = [e for e in useful_edges if ctx_min_len(e) >= 1]

    # ---- ε-环：可空边子图中的循环 SCC ----
    eps_witnesses: list[CycleWitness] = []
    eps_adj = adj_of(eps_edges)
    eps_emap = edge_map(eps_edges)
    nodes = sorted({e.src for e in eps_edges} | {e.dst for e in eps_edges})
    for scc in tarjan_scc(nodes, eps_adj):
        if not _is_cyclic(scc, eps_adj):
            continue
        allowed = set(scc)
        start = sorted(scc)[0]
        path = _find_path(eps_emap, start, start, allowed, min_len=1)
        if path:
            eps_witnesses.append(_witness_from_path(grammar, start, path, tuple(sorted(scc))))

    # ---- 增长环：全边子图中含增长边的循环 SCC ----
    growth_witnesses: list[CycleWitness] = []
    all_adj = adj_of(useful_edges)
    all_emap = edge_map(useful_edges)
    growth_set = {(e.src, e.dst, e.prod_index, e.rhs_index) for e in growth_edges}
    nodes = sorted({e.src for e in useful_edges} | {e.dst for e in useful_edges})
    for scc in tarjan_scc(nodes, all_adj):
        if not _is_cyclic(scc, all_adj):
            continue
        allowed = set(scc)
        inner_growth = [e for e in growth_edges
                        if e.src in allowed and e.dst in allowed]
        if not inner_growth:
            continue
        # 见证：增长边 X→Y 接上 Y ~> X 的 SCC 内路径，拼成 X ⇒+ αXβ
        e0 = inner_growth[0]
        back = _find_path(all_emap, e0.dst, e0.src, allowed, min_len=0)
        if back is None:
            continue
        path = [e0] + back
        growth_witnesses.append(
            _witness_from_path(grammar, e0.src, path, tuple(sorted(scc))))

    return CycleAnalysis(
        epsilon_cycles=eps_witnesses,
        growth_cycles=growth_witnesses,
        infinitely_ambiguous=bool(eps_witnesses),
        language_infinite=bool(growth_witnesses),
    )

"""查询扩展：显式同义组 -> 带权查询图。

关键约束（对应需求）：
- 只沿“显式声明”的同义边扩展，保留原词最高权重（hop=0, weight=1.0）。
- 扩展权重 = 路径上各边权之积；边权 <= 1，跳数越多权重越低。
- 有环图不无限展开：阶段 A 多源 BFS 中每个节点只创建/扩展一次，
  并以 max_word_hops 限制层数；阶段 B 只在有界闭包子图上寻路，
  分支内同样带 path-guard（禁止重复节点）。
- a↔b 循环只展开一次：a 扩展到 b 后，b 回看 a 的回边只计数不入队。
- 多查询根共享的扩展节点只创建一次（shared），不重复扩展；
  “不重复计分”在 search 层通过文档位置归属保证。
- 短语同义与单词同义分离：短语只整条替换，绝不拆成单词边。
- 预算（节点数/边数）耗尽即停止，并显式标记 truncated / truncation。

实现分两阶段：
  阶段 A 全局闭包：多源按层 BFS。多源 BFS 首次发现节点时的层数，
  等于“距任意查询根的最小跳数”，因此恰好得到所有根在跳数限制内的
  可达并集；每个节点只由最先发现它的根扩展一次。
  阶段 B 按根归因：在阶段 A 裁剪好的有界无向闭包子图上，为每个查询
  根求最大权路径，得到“哪个查询位置可以命中该 surface”及对应权重。
"""

from __future__ import annotations

import heapq
from collections import deque
from dataclasses import dataclass, field

from .synonyms import SynonymStore
from .tokenizer import tokenize

SMALL_GRAPH_LIMIT = 64


@dataclass
class SurfaceInfo:
    surface: str
    weight: float                       # 代表（发现树）传播权重
    path: tuple[str, ...]               # 代表路径（发现树）
    hop: int                            # 代表路径跳数
    roots: list[int] = field(default_factory=list)
    paths_by_root: dict[int, tuple[str, ...]] = field(default_factory=dict)
    weights_by_root: dict[int, float] = field(default_factory=dict)
    is_original: bool = False

    def chain(self) -> str:
        return " -> ".join(self.path)


@dataclass
class PhraseAlternative:
    start: int
    end: int
    gid: int
    group_weight: float
    alternatives: list[tuple[str, ...]]  # token 序列，含原短语本身
    surfaces: list[str]                   # 与 alternatives 对齐的可读形式
    is_original: list[bool]


@dataclass
class QueryGraph:
    text: str
    tokens: tuple[str, ...]
    nodes: dict[str, SurfaceInfo]
    phrases: list[PhraseAlternative]
    shared: set[str]
    edges: set[tuple[str, str]]
    back_edges: int
    truncated: bool
    truncation: str | None
    node_budget: int
    edge_budget: int
    max_word_hops: int

    def original_surfaces(self) -> list[str]:
        return [s for s, info in self.nodes.items() if info.is_original]

    def expansion_surfaces(self) -> list[str]:
        return [s for s, info in self.nodes.items() if not info.is_original]


class QueryExpander:
    def __init__(
        self,
        store: SynonymStore,
        max_word_hops: int = 2,
        node_budget: int = 128,
        edge_budget: int = 512,
    ) -> None:
        if max_word_hops < 0 or node_budget < 1 or edge_budget < 0:
            raise ValueError("非法扩展预算")
        self.store = store
        self.max_word_hops = max_word_hops
        self.node_budget = node_budget
        self.edge_budget = edge_budget

    def expand(self, query: str) -> QueryGraph:
        toks = tuple(t.text for t in tokenize(query))

        nodes: dict[str, SurfaceInfo] = {}
        parent: dict[str, str | None] = {}
        discover_root: dict[str, int] = {}
        level: dict[str, int] = {}
        edges: set[tuple[str, str]] = set()
        shared: set[str] = set()
        back_edges = 0
        edges_used = 0
        truncated = False
        truncation: str | None = None

        # ---- 阶段 A：多源按层 BFS 闭包 ----
        queue: deque[str] = deque()
        for pos, tok in enumerate(toks):
            if tok not in nodes:
                nodes[tok] = SurfaceInfo(tok, 1.0, (tok,), 0, is_original=True)
                parent[tok] = None
                discover_root[tok] = pos
                level[tok] = 0
                queue.append(tok)

        while queue and not truncated:
            node = queue.popleft()
            hop = level[node]
            root = discover_root[node]
            if hop >= self.max_word_hops:
                continue
            for nb, ew in sorted(self.store.neighbors(node).items()):
                key = _edge_key(node, nb)
                already = key in edges
                edges_used += 1
                edges.add(key)
                if edges_used > self.edge_budget:
                    truncated, truncation = True, "edge_budget"
                    break
                if nb in nodes:
                    # 无向 BFS：边此前已存在即非发现树边 -> 回边（被剪枝）。
                    # a->b 是发现树边，b 回看 a 的父边反向遍历恰被计 1 次，
                    # 这正是“a↔b 循环只展开一次”的可核验计数。
                    if already:
                        back_edges += 1
                    if discover_root[nb] != root:
                        shared.add(nb)
                    continue
                if len(nodes) >= self.node_budget:
                    truncated, truncation = True, "node_budget"
                    break
                chain = _chain_to(node, parent) + (nb,)
                nodes[nb] = SurfaceInfo(nb, nodes[node].weight * ew, chain, hop + 1)
                parent[nb] = node
                discover_root[nb] = root
                level[nb] = hop + 1
                queue.append(nb)

        # ---- 阶段 B：在有界闭包子图上按根求最大权路径（按根归因）----
        adj = _build_adj(nodes, self.store)
        original = set(toks)
        for pos, tok in enumerate(toks):
            for surface, (w, path, hop) in _best_paths_from(
                adj, tok, self.max_word_hops
            ).items():
                info = nodes[surface]
                if pos not in info.roots:
                    info.roots.append(pos)
                info.paths_by_root[pos] = path
                info.weights_by_root[pos] = w
        for info in nodes.values():
            info.is_original = info.surface in original
            if len(info.roots) >= 2:
                shared.add(info.surface)

        phrases = list(self._build_phrases(toks))

        return QueryGraph(
            text=query,
            tokens=toks,
            nodes=nodes,
            phrases=phrases,
            shared=shared,
            edges=edges,
            back_edges=back_edges,
            truncated=truncated,
            truncation=truncation,
            node_budget=self.node_budget,
            edge_budget=self.edge_budget,
            max_word_hops=self.max_word_hops,
        )

    def _build_phrases(self, toks: tuple[str, ...]):
        for start, end, gid, gw, plist in self.store.iter_phrase_alternatives(toks):
            alts = [tuple(p.tokens) for p in plist]
            surfaces = [p.surface for p in plist]
            orig_span = toks[start:end]
            yield PhraseAlternative(
                start=start,
                end=end,
                gid=gid,
                group_weight=gw,
                alternatives=alts,
                surfaces=surfaces,
                is_original=[a == orig_span for a in alts],
            )


def _edge_key(a: str, b: str) -> tuple[str, str]:
    return (a, b) if a <= b else (b, a)


def _chain_to(node: str, parent: dict[str, str | None]) -> tuple[str, ...]:
    chain = [node]
    cur = parent.get(node)
    while cur is not None:
        chain.append(cur)
        cur = parent.get(cur)
    return tuple(reversed(chain))


def _build_adj(nodes: dict[str, SurfaceInfo], store: SynonymStore):
    """闭包节点集合在同义图上诱导出的无向邻接表（带权）。"""
    adj: dict[str, dict[str, float]] = {s: {} for s in nodes}
    for s in nodes:
        for nb, w in store.neighbors(s).items():
            if nb in nodes:
                adj[s][nb] = w
    return adj


def _best_paths_from(adj, src: str, max_hops: int) -> dict[str, tuple]:
    """有界子图上的单源最大权路径（Dijkstra，目标是最大边权乘积）。

    返回 node -> (weight, path, hop)。分支带 path-guard，
    且图规模受闭包预算上界约束，因此有环也必然终止。
    """
    if src not in adj:
        return {}
    best_w = {src: 1.0}
    best_path = {src: (src,)}
    best_hop = {src: 0}
    heap = [(-1.0, 0, (src,), src)]
    while heap:
        neg_w, hop, path, node = heapq.heappop(heap)
        w = -neg_w
        if w < best_w.get(node, -1.0) - 1e-12:
            continue
        if hop >= max_hops:
            continue
        for nb, ew in sorted(adj.get(node, {}).items()):
            if nb in path:
                continue
            nw = w * ew
            new_path = path + (nb,)
            old = best_w.get(nb)
            better = old is None or nw > old + 1e-12
            tied = (
                old is not None
                and abs(nw - old) <= 1e-12
                and (hop + 1 < best_hop[nb]
                     or (hop + 1 == best_hop[nb] and new_path < best_path[nb]))
            )
            if better or tied:
                best_w[nb] = nw
                best_path[nb] = new_path
                best_hop[nb] = hop + 1
                heapq.heappush(heap, (-nw, hop + 1, new_path, nb))
    return {s: (best_w[s], best_path[s], best_hop[s]) for s in best_w}


def enumerate_simple_paths(
    adj: dict[str, dict[str, float]],
    src: str,
    dst: str,
    max_hops: int,
) -> list[tuple[tuple[str, ...], float]]:
    """小词图上的简单路径枚举（核验用）。

    - 节点数超过 SMALL_GRAPH_LIMIT 直接拒绝，防止指数枚举。
    - 只枚举无重复节点的简单路径：有环图也不会无限展开。
    - 返回 (path, 路径权重乘积)，按权重降序、路径字典序确定排序。
    """
    if len(adj) > SMALL_GRAPH_LIMIT:
        raise ValueError(f"词图过大（>{SMALL_GRAPH_LIMIT} 节点），拒绝枚举")
    results: list[tuple[tuple[str, ...], float]] = []

    def dfs(node: str, path: tuple[str, ...], w: float, hop: int) -> None:
        if node == dst:
            results.append((path, w))
            return
        if hop >= max_hops:
            return
        for nb in sorted(adj.get(node, {})):
            if nb in path:
                continue
            dfs(nb, path + (nb,), w * adj[node][nb], hop + 1)

    if src in adj:
        dfs(src, (src,), 1.0, 0)
    results.sort(key=lambda x: (-x[1], x[0]))
    return results

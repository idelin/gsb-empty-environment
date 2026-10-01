"""有限字母路径合规求解器。

在"图顶点 x DFA 状态"的积图上做最佳优先搜索（Dijkstra 推广），
优先级元组为 (总成本, 边数, 标签字符串, 边ID元组)。

正确性要点：
- 边成本非负 => 成本沿路径扩展单调不减；
- 每扩展一步边数严格 +1；
- 标签/边ID序列只能追加，前缀在字典序上更小，故序列也单调不减。
因此优先级沿任何路径扩展单调不减，每个积状态 (v, q) 第一次从堆中
弹出时即取得该状态的最优优先级，之后同状态的条目可直接跳过。
零成本环只会产生优先级相同但边数更大的条目，在有限个积状态
(|V| * |Q|) 结算完毕后搜索必然终止，不会无限循环。

字典序支配的说明：到达同一积状态且 (成本, 边数) 相同的两条路径，
其标签长度相等，互不为前缀，因此拼接任意公共后缀后字典序保持；
边ID序列同理。故每个积状态只需保留最优条目，剪枝是安全的。
"""

from __future__ import annotations

import heapq
import json
import sys
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Edge:
    eid: int
    src: str
    dst: str
    label: str
    cost: int


@dataclass
class Graph:
    vertices: list[str]
    edges: list[Edge]
    adj: dict[str, list[Edge]] = field(default_factory=dict)

    def __post_init__(self) -> None:
        self.adj = {v: [] for v in self.vertices}
        for e in self.edges:
            self.adj[e.src].append(e)


@dataclass
class DFA:
    states: list[str]
    alphabet: list[str]
    start: str
    accepting: set[str]
    trans: dict[tuple[str, str], str]  # (state, char) -> state；允许部分函数


def _validate(graph: Graph, dfa: DFA, source: str, target: str) -> None:
    vset = set(graph.vertices)
    if len(vset) != len(graph.vertices):
        raise ValueError("图中存在重复顶点")
    if source not in vset or target not in vset:
        raise ValueError("source/target 不在图顶点集中")
    seen_ids: set[int] = set()
    alpha = set(dfa.alphabet)
    for e in graph.edges:
        if e.eid in seen_ids:
            raise ValueError(f"边ID重复: {e.eid}")
        seen_ids.add(e.eid)
        if e.src not in vset or e.dst not in vset:
            raise ValueError(f"边 {e.eid} 的端点不在顶点集中")
        if len(e.label) != 1 or e.label not in alpha:
            raise ValueError(f"边 {e.eid} 的标签 {e.label!r} 不是字母表中的单个字符")
        if e.cost < 0:
            raise ValueError(f"边 {e.eid} 成本为负: {e.cost}")
    if dfa.start not in set(dfa.states):
        raise ValueError("DFA 初始状态不在状态集中")
    if not dfa.accepting <= set(dfa.states):
        raise ValueError("DFA 接受状态不在状态集中")
    for (q, c), q2 in dfa.trans.items():
        if q not in set(dfa.states) or q2 not in set(dfa.states):
            raise ValueError("DFA 转移涉及未知状态")
        if c not in alpha:
            raise ValueError(f"DFA 转移字符 {c!r} 不在字母表中")


def solve(graph: Graph, dfa: DFA, source: str, target: str) -> dict:
    """返回结果字典；未找到合规路径时 found 为 False。"""
    _validate(graph, dfa, source, target)

    # 堆条目: (成本, 边数, 标签串, 边ID元组, 顶点, DFA状态, 顶点路径, 边ID路径, DFA状态路径)
    # 前 4 项为优先级；顶点/状态为字符串可比较，保证堆条目全序。
    pq: list[tuple] = [
        (0, 0, "", (), source, dfa.start, [source], [], [dfa.start])
    ]
    settled: dict[tuple[str, str], tuple] = {}

    while pq:
        cost, ne, lab, eids, v, q, vpath, epath, qpath = heapq.heappop(pq)
        key = (v, q)
        sig = (cost, ne, lab, eids)
        if key in settled:
            continue  # 首次弹出即最优，后续条目必然不更优
        settled[key] = sig
        if v == target and q in dfa.accepting:
            return {
                "found": True,
                "cost": cost,
                "edge_count": ne,
                "label": lab,
                "vertices": vpath,
                "edge_ids": epath,
                "dfa_states": qpath,
            }
        for e in graph.adj[v]:
            q2 = dfa.trans.get((q, e.label))
            if q2 is None:
                continue
            heapq.heappush(
                pq,
                (
                    cost + e.cost,
                    ne + 1,
                    lab + e.label,
                    eids + (e.eid,),
                    e.dst,
                    q2,
                    vpath + [e.dst],
                    epath + [e.eid],
                    qpath + [q2],
                ),
            )
    return {"found": False}


def parse_instance(data: dict) -> tuple[Graph, DFA, str, str]:
    g = data["graph"]
    d = data["dfa"]
    edges = [
        Edge(eid=int(e["id"]), src=e["src"], dst=e["dst"],
             label=e["label"], cost=int(e["cost"]))
        for e in g["edges"]
    ]
    graph = Graph(vertices=list(g["vertices"]), edges=edges)
    trans: dict[tuple[str, str], str] = {}
    for row in d["transitions"]:
        q, c, q2 = row
        if (q, c) in trans:
            raise ValueError(f"DFA 非确定：状态 {q!r} 在字符 {c!r} 上有多个转移")
        trans[(q, c)] = q2
    dfa = DFA(
        states=list(d["states"]),
        alphabet=list(d["alphabet"]),
        start=d["start"],
        accepting=set(d["accepting"]),
        trans=trans,
    )
    return graph, dfa, data["source"], data["target"]


def main(argv: list[str]) -> int:
    if len(argv) > 2:
        print("用法: python3 dfa_path.py [输入.json]  (缺省从 stdin 读取)", file=sys.stderr)
        return 2
    raw = open(argv[1], encoding="utf-8").read() if len(argv) == 2 else sys.stdin.read()
    try:
        graph, dfa, source, target = parse_instance(json.loads(raw))
        result = solve(graph, dfa, source, target)
    except (ValueError, KeyError, TypeError) as exc:
        print(json.dumps({"found": False, "error": str(exc)}, ensure_ascii=False))
        return 1
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))

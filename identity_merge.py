#!/usr/bin/env python3.11
"""可撤回身份合并（Retractable Identity Merge）。

主体别名关系建模为无向多重图：
  - 顶点：主体 ID（字符串）。
  - 边：一对主体之间的别名关系；每条边可携带多条独立证据（evidence ID）。
  - 同一条边只要还剩至少一条证据，就仍然成立。

规范身份：连通分量内字典序最小的主体 ID。

为什么不使用并查集：并查集不支持删除边（撤回证据可能导致分量分裂）。
本实现直接维护邻接多重图，查询时在"当前图"上重算连通分量
（用脏标记缓存，批量查询只算一次），因此撤回语义天然正确。

命令行用法：
    python3.11 identity_merge.py [输入文件]      # 缺省读 stdin

输入格式（每行一条命令，# 开头为注释，空行忽略）：
    ADD <id_a> <id_b> <evidence_id>   添加一条证据，支持 id_a 与 id_b 的别名边
    RETRACT <evidence_id>             撤回一条证据；边无剩余证据时边消失
    QUERY <id>                        查询规范身份，并输出到规范身份的路径证据
    BATCH <id_1> <id_2> ...           离线批量查询（只重算一次连通分量）

输出格式：
    QUERY  ->  QUERY <id> canonical=<c> path=<id> -[e1]-> <mid> -[e2]-> <c>
               （id 即规范身份或未知时 path 仅含自身；未知 id 的规范身份是其自身）
    BATCH  ->  每个 id 一行，格式同 QUERY
    ADD/RETRACT -> OK / 错误信息（错误输出到 stderr 并以非零码退出）
"""

from __future__ import annotations

import sys
from collections import deque


class IdentityMerger:
    """可撤回证据的身份合并器。"""

    def __init__(self) -> None:
        # 规范化边键 (min, max) -> 该边上现存证据 ID 集合
        self._edge_evidence: dict[tuple[str, str], set[str]] = {}
        # 证据 ID -> 边键，用于按证据撤回
        self._evidence_index: dict[str, tuple[str, str]] = {}
        # 连通分量缓存：节点 -> 分量成员集合；None 表示脏
        self._components: dict[str, frozenset[str]] | None = None

    # ---------- 变更操作 ----------

    @staticmethod
    def _edge_key(a: str, b: str) -> tuple[str, str]:
        return (a, b) if a <= b else (b, a)

    def add(self, a: str, b: str, evidence_id: str) -> None:
        """添加证据 evidence_id，支持 a 与 b 之间的别名边。"""
        if evidence_id in self._evidence_index:
            raise ValueError(f"证据 ID 已存在: {evidence_id}")
        if a == b:
            raise ValueError(f"自环证据无意义: {a}")
        key = self._edge_key(a, b)
        self._edge_evidence.setdefault(key, set()).add(evidence_id)
        self._evidence_index[evidence_id] = key
        self._components = None  # 标记脏，下次查询重算

    def retract(self, evidence_id: str) -> None:
        """撤回证据。边失去最后一条证据时被移除，分量可能分裂。"""
        key = self._evidence_index.pop(evidence_id, None)
        if key is None:
            raise ValueError(f"未知证据 ID: {evidence_id}")
        remaining = self._edge_evidence[key]
        remaining.discard(evidence_id)
        if not remaining:
            del self._edge_evidence[key]
        self._components = None  # 标记脏，下次查询重算

    # ---------- 查询 ----------

    def _adjacency(self) -> dict[str, list[str]]:
        adj: dict[str, list[str]] = {}
        for u, v in self._edge_evidence:
            adj.setdefault(u, []).append(v)
            adj.setdefault(v, []).append(u)
        return adj

    def _compute_components(self) -> None:
        """在当前图上重算全部连通分量（BFS）。删除后分裂自然被处理。"""
        adj = self._adjacency()
        components: dict[str, frozenset[str]] = {}
        for start in adj:
            if start in components:
                continue
            members = {start}
            queue = deque([start])
            while queue:
                node = queue.popleft()
                for nxt in adj[node]:
                    if nxt not in members:
                        members.add(nxt)
                        queue.append(nxt)
            frozen = frozenset(members)
            for member in frozen:
                components[member] = frozen
        self._components = components

    def canonical(self, node: str) -> str:
        """返回 node 的规范身份（分量内最小 ID）；未知节点规范身份为自身。"""
        if self._components is None:
            self._compute_components()
        assert self._components is not None
        members = self._components.get(node)
        return min(members) if members else node

    def evidence_path(self, node: str) -> list[tuple[str, str, str]]:
        """返回 node 到其规范身份的路径证据。

        结果为 [(from, evidence_id, to), ...]，从 node 出发到规范身份；
        node 即规范身份（或未知）时为空列表。每条边任选一条现存证据
        （取最小 ID 保证确定性）。
        """
        target = self.canonical(node)
        if node == target:
            return []
        adj: dict[str, list[tuple[str, str]]] = {}
        for (u, v), evidences in self._edge_evidence.items():
            evidence = min(evidences)
            adj.setdefault(u, []).append((v, evidence))
            adj.setdefault(v, []).append((u, evidence))
        # BFS 找 node -> target 最短路径，记录前驱
        prev: dict[str, tuple[str, str]] = {}
        visited = {node}
        queue = deque([node])
        while queue:
            cur = queue.popleft()
            if cur == target:
                break
            for nxt, evidence in adj.get(cur, []):
                if nxt not in visited:
                    visited.add(nxt)
                    prev[nxt] = (cur, evidence)
                    queue.append(nxt)
        if target not in visited:
            return []  # 理论不可达：canonical 保证同分量
        path: list[tuple[str, str, str]] = []
        cur = target
        while cur != node:
            parent, evidence = prev[cur]
            path.append((parent, evidence, cur))
            cur = parent
        path.reverse()
        return path

    def query(self, node: str) -> str:
        """格式化单行查询结果。"""
        canonical = self.canonical(node)
        path = self.evidence_path(node)
        if not path:
            path_str = node
        else:
            parts = [path[0][0]]
            for _, evidence, to in path:
                parts.append(f"-[{evidence}]-> {to}")
            path_str = " ".join(parts)
        return f"QUERY {node} canonical={canonical} path={path_str}"

    def batch_query(self, nodes: list[str]) -> list[str]:
        """离线批量查询：连通分量只重算一次。"""
        if self._components is None:
            self._compute_components()
        return [self.query(node) for node in nodes]


# ---------- 命令行 ----------

def run_commands(lines, out) -> int:
    merger = IdentityMerger()
    for lineno, raw in enumerate(lines, 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        tokens = line.split()
        cmd, args = tokens[0].upper(), tokens[1:]
        try:
            if cmd == "ADD" and len(args) == 3:
                merger.add(args[0], args[1], args[2])
                print("OK", file=out)
            elif cmd == "RETRACT" and len(args) == 1:
                merger.retract(args[0])
                print("OK", file=out)
            elif cmd == "QUERY" and len(args) == 1:
                print(merger.query(args[0]), file=out)
            elif cmd == "BATCH" and args:
                for result in merger.batch_query(args):
                    print(result, file=out)
            else:
                raise ValueError(f"无法解析的命令: {line}")
        except ValueError as exc:
            print(f"第 {lineno} 行错误: {exc}", file=sys.stderr)
            return 1
    return 0


def main(argv: list[str]) -> int:
    if len(argv) > 2:
        print(__doc__, file=sys.stderr)
        return 2
    if len(argv) == 2:
        with open(argv[1], encoding="utf-8") as fh:
            return run_commands(fh, sys.stdout)
    return run_commands(sys.stdin, sys.stdout)


if __name__ == "__main__":
    sys.exit(main(sys.argv))

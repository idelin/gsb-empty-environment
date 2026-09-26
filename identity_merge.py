#!/usr/bin/env python3
"""可撤回身份合并（retractable identity merge）。

主体别名关系建模为无向边，每条边可由多条独立证据支持。
撤回证据时若某边不再有任何证据则该边消失，连通分量整体重算，
规范身份（canonical ID）取分量内最小 ID。因此不使用仅支持合并、
不支持删除的经典并查集，而是基于证据索引 + 全量分量重算。

仅使用 Python 3.11 标准库。所有示例数据均为合成数据。

命令行用法（离线批量模式）：
    python3 identity_merge.py [commands_file]
省略文件参数时从标准输入读取。每行一条命令：

    add <id1> <id2> <evidence_id>   为边 id1-id2 添加一条证据
    retract <evidence_id>           撤回一条证据（可能使边消失、分量分裂）
    query <id1> <id2>               查询两 ID 是否同属一个规范身份，并输出路径证据
    canon <id>                      查询某 ID 当前的规范身份

输出（每行对应一条命令的结果）：
    OK add <id1> <id2> <evidence_id>
    OK retract <evidence_id>
    ERROR retract <evidence_id> : unknown evidence
    SAME canonical=<c> path=<id1> -[<e>]- <id2> -[<e>]- ...
    DIFFERENT canonical_a=<c1> canonical_b=<c2>
    UNKNOWN <id>                    （canon 查询了不存在的 ID）
    ERROR ...                       （格式错误等）

示例（合成数据）：
    add a b e1
    add b c e2
    query a c        -> SAME canonical=a path=a -[e1]- b -[e2]- c
    retract e2
    canon c          -> c
    query a b        -> SAME canonical=a path=a -[e1]- b
"""

from __future__ import annotations

import argparse
import sys
from collections import deque
from typing import Iterable, TextIO


def _edge_key(id1: str, id2: str) -> tuple[str, str]:
    """无向边的规范化键。"""
    return (id1, id2) if id1 <= id2 else (id2, id1)


class IdentityMerger:
    """支持证据撤回的身份合并器。

    数据结构：
      _edge_evidence: 边 -> 支持该边的证据 ID 集合
      _evidence_index: 证据 ID -> 边（保证每条证据只归属一条边）
    每次查询时基于当前边集重算连通分量，保证撤回后结果正确。
    """

    def __init__(self) -> None:
        self._edge_evidence: dict[tuple[str, str], set[str]] = {}
        self._evidence_index: dict[str, tuple[str, str]] = {}
        self._nodes: set[str] = set()  # 出现过的主体；边消失后仍作为独立身份存在

    def add_evidence(self, id1: str, id2: str, evidence: str) -> None:
        if id1 == id2:
            raise ValueError(f"self-loop not allowed: {id1!r}")
        key = _edge_key(id1, id2)
        existing = self._evidence_index.get(evidence)
        if existing is not None:
            if existing == key:
                return  # 幂等：同一证据重复添加同一条边
            raise ValueError(
                f"evidence {evidence!r} already supports edge {existing}"
            )
        self._edge_evidence.setdefault(key, set()).add(evidence)
        self._evidence_index[evidence] = key
        self._nodes.update(key)

    def retract(self, evidence: str) -> bool:
        """撤回证据。返回是否找到该证据。"""
        key = self._evidence_index.pop(evidence, None)
        if key is None:
            return False
        supporters = self._edge_evidence[key]
        supporters.discard(evidence)
        if not supporters:
            del self._edge_evidence[key]  # 无证据支持，边消失
        return True

    def _adjacency(self) -> dict[str, list[str]]:
        adj: dict[str, list[str]] = {node: [] for node in self._nodes}
        for a, b in self._edge_evidence:
            adj.setdefault(a, []).append(b)
            adj.setdefault(b, []).append(a)
        return adj

    def canonical_ids(self) -> dict[str, str]:
        """重算所有连通分量，返回 节点 -> 规范ID（分量内最小ID）。"""
        adj = self._adjacency()
        canonical: dict[str, str] = {}
        for start in adj:
            if start in canonical:
                continue
            component = [start]
            canonical[start] = start
            queue: deque[str] = deque([start])
            while queue:
                node = queue.popleft()
                for nxt in adj[node]:
                    if nxt not in canonical:
                        canonical[nxt] = nxt
                        component.append(nxt)
                        queue.append(nxt)
            root = min(component)
            for node in component:
                canonical[node] = root
        return canonical

    def canonical(self, node: str) -> str | None:
        return self.canonical_ids().get(node)

    def find_path(self, id1: str, id2: str) -> list[tuple[str, str]] | None:
        """BFS 找 id1 到 id2 的一条路径。

        返回 [(节点, 支撑该步的证据ID), ...]，首元素证据为空字符串；
        不连通或节点不存在时返回 None。每条边取字典序最小的证据ID，
        保证输出确定性。
        """
        adj = self._adjacency()
        if id1 not in adj or id2 not in adj:
            return None
        parent: dict[str, tuple[str, str]] = {}  # 节点 -> (前驱, 证据)
        visited = {id1}
        queue: deque[str] = deque([id1])
        while queue:
            node = queue.popleft()
            if node == id2:
                break
            for nxt in adj[node]:
                if nxt not in visited:
                    visited.add(nxt)
                    evidence = min(self._edge_evidence[_edge_key(node, nxt)])
                    parent[nxt] = (node, evidence)
                    queue.append(nxt)
        if id2 not in visited:
            return None
        path: list[tuple[str, str]] = []
        node = id2
        while node != id1:
            prev, evidence = parent[node]
            path.append((node, evidence))
            node = prev
        path.append((id1, ""))
        path.reverse()
        return path


def format_path(path: list[tuple[str, str]]) -> str:
    parts = [path[0][0]]
    for node, evidence in path[1:]:
        parts.append(f"-[{evidence}]-")
        parts.append(node)
    return " ".join(parts)


def run_commands(lines: Iterable[str], out: TextIO, merger: IdentityMerger | None = None) -> IdentityMerger:
    """逐行执行批量命令并写出结果。返回使用的 merger 便于测试。"""
    if merger is None:
        merger = IdentityMerger()
    for lineno, raw in enumerate(lines, 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        tokens = line.split()
        cmd, args = tokens[0], tokens[1:]
        try:
            if cmd == "add" and len(args) == 3:
                merger.add_evidence(args[0], args[1], args[2])
                print(f"OK add {args[0]} {args[1]} {args[2]}", file=out)
            elif cmd == "retract" and len(args) == 1:
                if merger.retract(args[0]):
                    print(f"OK retract {args[0]}", file=out)
                else:
                    print(f"ERROR retract {args[0]} : unknown evidence", file=out)
            elif cmd == "query" and len(args) == 2:
                canonical = merger.canonical_ids()
                ca, cb = canonical.get(args[0]), canonical.get(args[1])
                if ca is not None and ca == cb:
                    path = merger.find_path(args[0], args[1])
                    print(f"SAME canonical={ca} path={format_path(path)}", file=out)
                else:
                    print(
                        f"DIFFERENT canonical_a={ca} canonical_b={cb}", file=out
                    )
            elif cmd == "canon" and len(args) == 1:
                c = merger.canonical(args[0])
                print(c if c is not None else f"UNKNOWN {args[0]}", file=out)
            else:
                print(f"ERROR line {lineno}: unrecognized command: {line}", file=out)
        except ValueError as exc:
            print(f"ERROR line {lineno}: {exc}", file=out)
    return merger


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="可撤回身份合并：离线批量处理 add/retract/query/canon 命令。"
    )
    parser.add_argument(
        "commands_file",
        nargs="?",
        help="命令文件路径；省略时从标准输入读取。",
    )
    args = parser.parse_args(argv)
    if args.commands_file:
        with open(args.commands_file, "r", encoding="utf-8") as fh:
            run_commands(fh, sys.stdout)
    else:
        run_commands(sys.stdin, sys.stdout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

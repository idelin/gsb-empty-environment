"""包图数据模型与条件表达式解析。

JSON 结构（示例见 demo 目录）::

    {
      "features": ["linux", "ssl"],
      "packages": [
        {"id": "root", "version": "1.0", "exports": {"newName": "oldName", "gone": null}},
        {"id": "lib-a", "version": "2.1", "symbols": ["foo"],
         "exports": {"renamed": "foo"}}
      ],
      "edges": [
        {"from": "root", "to": "lib-a", "kind": "public", "when": ["and", "linux", ["not", "ssl"]]}
      ]
    }

条件表达式:
  特性名           -> Bool
  ["not", x]
  ["and", x, y, ...]
  ["or",  x, y, ...]
未给 "when" 的边条件恒真。
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

from .bdd import BDDManager, TRUE

PUBLIC = "public"
PRIVATE = "private"
BUILD = "build"
EDGE_KINDS = (PUBLIC, PRIVATE, BUILD)
ROOT_ID = "root"


@dataclass(frozen=True)
class Source:
    """符号身份：包 ID + 版本 + 原符号名。"""

    package: str
    version: str
    symbol: str

    def as_tuple(self) -> tuple[str, str, str]:
        return (self.package, self.version, self.symbol)


@dataclass
class Package:
    id: str
    version: str
    symbols: set[str] = field(default_factory=set)
    # exposed_name -> original_name；null 表示隐藏
    rename: dict[str, str] = field(default_factory=dict)
    hide: set[str] = field(default_factory=set)


@dataclass
class Edge:
    key: int
    from_: str
    to: str
    kind: str
    condition: int          # BDD 节点
    condition_text: Any
    deleted: bool = False


def parse_condition(expr: Any, bdd: BDDManager) -> int:
    if expr is None or expr is True:
        return TRUE
    if expr is False:
        from .bdd import FALSE
        return FALSE
    if isinstance(expr, str):
        bdd.declare(expr)
        return bdd.literal(expr)
    if isinstance(expr, list) and expr:
        op = expr[0]
        if op == "not":
            if len(expr) != 2:
                raise ValueError("not 只接受一个参数")
            return bdd.neg(parse_condition(expr[1], bdd))
        if op == "and":
            return bdd.and_(*(parse_condition(x, bdd) for x in expr[1:]))
        if op == "or":
            return bdd.or_(*(parse_condition(x, bdd) for x in expr[1:]))
    raise ValueError(f"无法解析条件表达式: {expr!r}")


class PackageGraph:
    def __init__(self, data: dict[str, Any]):
        self.bdd = BDDManager(data.get("features", []))
        self.packages: dict[str, Package] = {}
        self.edges: dict[int, Edge] = {}
        self.out_edges: dict[str, list[int]] = {}
        self.in_edges: dict[str, list[int]] = {}
        self._next_key = 0

        for raw in data.get("packages", []):
            pkg = self._build_package(raw)
            self.packages[pkg.id] = pkg
            self.out_edges.setdefault(pkg.id, [])
            self.in_edges.setdefault(pkg.id, [])
        if ROOT_ID not in self.packages:
            raise ValueError("图中必须包含 root 包")

        for raw in data.get("edges", []):
            self.add_edge(
                raw["from"], raw["to"], raw["kind"], raw.get("when"),
            )

    @classmethod
    def from_file(cls, path: str) -> "PackageGraph":
        with open(path, "r", encoding="utf-8") as handle:
            return cls(json.load(handle))

    def _build_package(self, raw: dict[str, Any]) -> Package:
        pkg = Package(id=raw["id"], version=str(raw.get("version", "")),
                      symbols=set(raw.get("symbols", [])))
        for exposed, original in raw.get("exports", {}).items():
            if original is None:
                pkg.hide.add(exposed)
            else:
                pkg.rename[exposed] = original
        return pkg

    def add_edge(self, from_: str, to: str, kind: str, when: Any = None) -> int:
        if kind not in EDGE_KINDS:
            raise ValueError(f"未知边类型: {kind}")
        if from_ not in self.packages or to not in self.packages:
            raise ValueError(f"边引用了不存在的包: {from_} -> {to}")
        cond = parse_condition(when, self.bdd)
        key = self._next_key
        self._next_key += 1
        self.edges[key] = Edge(key, from_, to, kind, cond, when)
        self.out_edges.setdefault(from_, []).append(key)
        self.in_edges.setdefault(to, []).append(key)
        return key

    def delete_edge(self, key: int) -> Edge:
        edge = self.edges[key]
        edge.deleted = True
        return edge

    def active_edges(self) -> list[Edge]:
        return [edge for edge in self.edges.values() if not edge.deleted]

    def source_of(self, package_id: str, symbol: str) -> Source:
        return Source(package_id, self.packages[package_id].version, symbol)

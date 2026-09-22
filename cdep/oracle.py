"""Independent per-configuration reference traverser.

For every complete assignment of the finite feature set it performs ordinary
Boolean reachability with plain sets (no BDDs, no shared state with the
analyzer), and derives exposed symbols, the build closure and conflicts.
Used by tests to cross-check the symbolic implementation.
"""

from __future__ import annotations

import itertools
from typing import Dict, FrozenSet, List, Optional, Set, Tuple

from .model import GraphSpec, Source


class ReferenceOracle:
    def __init__(self, spec: GraphSpec, edges=None, root=None):
        self.spec = spec
        self._edges = edges  # live edge list (e.g. after incremental edits)
        self._root = root
        self.features = list(spec.bdd.var_order)
        self.configs = [
            dict(zip(self.features, vals))
            for vals in itertools.product((False, True), repeat=len(self.features))
        ]
        # config -> (visible node set, build node set)
        self.visible: Dict[FrozenSet[Tuple[str, bool]], Set[str]] = {}
        self.build_nodes: Dict[FrozenSet[Tuple[str, bool]], Set[str]] = {}
        # config -> name -> set of source tuples
        self.exposed: Dict[FrozenSet, Dict[str, Set[Tuple[str, str, str]]]] = {}
        self.build_exposed: Dict[FrozenSet, Dict[str, Set[Tuple[str, str, str]]]] = {}
        # name -> set of configs (frozen) that conflict
        self.conflict_configs: Dict[str, Set[FrozenSet]] = {}
        self._run()

    @property
    def edges(self):
        return self._edges if self._edges is not None else self.spec.edges

    @property
    def root(self):
        return self._root or self.spec.root

    def _holds(self, edge, config) -> bool:
        return self.spec.bdd.holds(edge.cond, config)

    def _run(self) -> None:
        for config in self.configs:
            key = frozenset(config.items())
            visible, build = self._traverse(config)
            self.visible[key] = visible
            self.build_nodes[key] = build
            exp = self._symbols(visible)
            bexp = self._symbols(build)
            self.exposed[key] = exp
            self.build_exposed[key] = bexp
            for name, srcs in exp.items():
                if len(srcs) > 1:
                    self.conflict_configs.setdefault(name, set()).add(key)

    def _traverse(self, config) -> Tuple[Set[str], Set[str]]:
        root = self.root
        visible: Set[str] = {root}
        stack: List[str] = [root]
        private_leaves: Set[str] = set()
        while stack:
            u = stack.pop()
            for edge in self.edges:
                if edge.src != u or not self._holds(edge, config):
                    continue
                if edge.kind == "public":
                    if edge.dst not in visible:
                        visible.add(edge.dst)
                        stack.append(edge.dst)
                elif edge.kind == "private" and u == root:
                    private_leaves.add(edge.dst)
        visible |= private_leaves

        build: Set[str] = {root}
        stack = [root]
        while stack:
            u = stack.pop()
            for edge in self.edges:
                if edge.src != u or not self._holds(edge, config):
                    continue
                if edge.kind in ("public", "build") and edge.dst not in build:
                    build.add(edge.dst)
                    stack.append(edge.dst)
        return visible, build

    def _symbols(self, nodes: Set[str]) -> Dict[str, Set[Tuple[str, str, str]]]:
        out: Dict[str, Set[Tuple[str, str, str]]] = {}
        for ref in nodes:
            pkg = self.spec.packages[ref]
            hidden = set(pkg.hide)
            for sym in pkg.provides:
                if sym not in hidden:
                    out.setdefault(sym, set()).add(Source(pkg.id, pkg.version, sym).as_tuple())
            for original, alias in pkg.rename.items():
                if original in hidden or original not in pkg.provides:
                    continue
                out.setdefault(alias, set()).add(
                    Source(pkg.id, pkg.version, original).as_tuple()
                )
        return out

"""Conditional dependency exposure analyzer.

Reachability semantics
----------------------
* The root is itself reachable and can see the exports of its direct
  ``public`` and ``private`` dependencies.
* Transitive traversal continues only along ``public`` edges.  A package
  reached at the end of a private edge cannot re-export anything through it.
* ``build`` edges only enter the build closure: from the root they reach a
  separate context that follows public and build edges but never private
  ones, and nothing from that closure is reported as runtime exposure.

Each edge carries a Boolean condition over finite feature variables.
Reachability conditions are ROBDD nodes, and the least fixed point is reached
by work-list delta propagation (``new = old OR incoming``), which terminates
naturally in the presence of dependency cycles without enumerating feature
configurations.

Symbol identity is (package id, version, original symbol).  The same source
reached by several paths merges (conditions are OR-joined); two *different*
sources exporting the same name under an intersecting pair of conditions are
a conflict.

Incremental edge deletion uses delete-and-rederive (DRed): an over-estimate
of facts lost with the edge is subtracted first (breaking cycles correctly,
unlike reference counts), then facts still supported from outside the
affected set are re-derived.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple

from .bdd import ROBDD, TRUE, FALSE
from .model import Edge, GraphSpec, Package, Source, split_node

PUBLIC = "public"
PRIVATE = "private"
BUILD = "build"


@dataclass
class PathStep:
    kind: str
    src: str
    dst: str
    when: str
    edge_id: int


@dataclass
class Witness:
    name: str
    source: Source
    config: Dict[str, bool]
    path: List[PathStep]
    condition: int


@dataclass
class Exposure:
    name: str
    source: Source
    condition: int  # reachability/visibility condition of the exposed name
    witness: Optional[Witness] = None


@dataclass
class Conflict:
    name: str
    sources: Tuple[Source, Source]
    conditions: Tuple[int, int]
    common: int  # conjunction, satisfiable iff real conflict
    config: Optional[Dict[str, bool]]
    witnesses: Tuple[Witness, Witness]


@dataclass
class UpdateReport:
    action: str
    edge_id: int
    touched_nodes: List[str]
    bdd_nodes_total: int
    bdd_nodes_delta: int


class Analyzer:
    def __init__(self, spec: GraphSpec):
        self.spec = spec
        self.bdd: ROBDD = spec.bdd
        self.root = spec.root
        # edge indices
        self.edges: List[Edge] = list(spec.edges)
        self._next_id = len(self.edges)
        self.out: Dict[str, List[int]] = {}
        self.incoming: Dict[str, List[int]] = {}
        self.edge_by_id: Dict[int, Edge] = {}
        for ref in spec.packages:
            self.out[ref] = []
            self.incoming[ref] = []
        for eid, edge in enumerate(self.edges):
            self._index_edge(eid, edge)
        # reachability conditions
        self.rt_reach: Dict[str, int] = {r: FALSE for r in self.out}
        self.private_facts: Dict[str, int] = {r: FALSE for r in self.out}
        self.build_reach: Dict[str, int] = {r: FALSE for r in self.out}
        # results
        self.exposure: Dict[str, Dict[Source, int]] = {}
        self.build_exposure: Dict[str, Dict[Source, int]] = {}
        self._conflicts: Optional[List[Conflict]] = None
        self.recompute()

    # ------------------------------------------------------------------ #
    # indexing
    # ------------------------------------------------------------------ #
    def _index_edge(self, eid: int, edge: Edge) -> None:
        self.edge_by_id[eid] = edge
        self.out.setdefault(edge.src, []).append(eid)
        self.incoming.setdefault(edge.dst, []).append(eid)
        self.out.setdefault(edge.dst, self.out.get(edge.dst, []))
        self.incoming.setdefault(edge.src, self.incoming.get(edge.src, []))

    def _unindex_edge(self, eid: int) -> Edge:
        edge = self.edge_by_id.pop(eid)
        self.out[edge.src] = [x for x in self.out[edge.src] if x != eid]
        self.incoming[edge.dst] = [x for x in self.incoming[edge.dst] if x != eid]
        return edge

    # ------------------------------------------------------------------ #
    # fixed point propagation
    # ------------------------------------------------------------------ #
    def _propagate(
        self,
        seeds: Dict[str, int],
        state: Dict[str, int],
        edge_kinds: Tuple[str, ...],
        limit: Optional[set] = None,
    ) -> set:
        """OR-delta least fixed point.

        ``state`` is updated in place.  Returns the set of nodes whose value
        changed.  When ``limit`` is given, propagation only enters nodes in
        that set (nodes outside keep contributing through their current
        state).
        """
        bdd = self.bdd
        work: List[Tuple[str, int]] = []
        changed: set = set()

        def enqueue(node: str, delta: int, force: bool = False) -> None:
            if delta == FALSE:
                return
            if limit is not None and node not in limit:
                return
            old = state[node]
            new = bdd.or_(old, delta)
            added = bdd.and_(new, bdd.not_(old)) if old != FALSE else new
            if not force and added == FALSE:
                return
            state[node] = new
            work.append((node, added if added != FALSE else delta))
            changed.add(node)

        for node, cond in seeds.items():
            # initial seeds always spread their delta outward (e.g. the root)
            enqueue(node, cond, force=True)
        while work:
            u, delta_u = work.pop()
            for eid in self.out.get(u, ()):
                edge = self.edge_by_id[eid]
                if edge.kind not in edge_kinds:
                    continue
                v = edge.dst
                if limit is not None and v not in limit:
                    continue
                contributed = bdd.and_(delta_u, edge.cond)
                if contributed == FALSE:
                    continue
                new = bdd.or_(state[v], contributed)
                if new != state[v]:
                    state[v] = new
                    work.append((v, contributed))
                    changed.add(v)
        return changed

    # ------------------------------------------------------------------ #
    # full recomputation
    # ------------------------------------------------------------------ #
    def recompute(self) -> None:
        bdd = self.bdd
        for r in self.rt_reach:
            self.rt_reach[r] = FALSE
            self.private_facts[r] = FALSE
            self.build_reach[r] = FALSE
        self.rt_reach[self.root] = TRUE
        self.build_reach[self.root] = TRUE

        # private facts come only from direct private edges out of the root
        priv = FALSE
        for eid in self.out[self.root]:
            edge = self.edge_by_id[eid]
            if edge.kind == PRIVATE:
                priv = bdd.or_(priv, edge.cond)
                self.private_facts[edge.dst] = bdd.or_(
                    self.private_facts[edge.dst], edge.cond
                )

        self._propagate({self.root: TRUE}, self.rt_reach, (PUBLIC,))
        self._propagate({self.root: TRUE}, self.build_reach, (PUBLIC, BUILD))
        self._build_exposures()
        self._conflicts = None

    def _visible_packages(self) -> Dict[str, int]:
        """Package -> condition under which root can see its exports."""
        visible: Dict[str, int] = {}
        for node, cond in self.rt_reach.items():
            if cond != FALSE:
                visible[node] = cond
        for node, cond in self.private_facts.items():
            if cond != FALSE:
                visible[node] = self.bdd.or_(visible.get(node, FALSE), cond)
        return visible

    def _build_exposures(self) -> None:
        self.exposure = {}
        for node, cond in self._visible_packages().items():
            self._add_package_exposure(node, cond, self.exposure)
        self.build_exposure = {}
        for node, cond in self.build_reach.items():
            if cond != FALSE:
                self._add_package_exposure(node, cond, self.build_exposure)

    def _add_package_exposure(
        self, node: str, cond: int, target: Dict[str, Dict[Source, int]]
    ) -> None:
        pkg = self.spec.packages[node]
        pid = pkg.id
        hidden = set(pkg.hide)
        for sym in pkg.provides:
            if sym in hidden:
                continue
            src = Source(pid, pkg.version, sym)
            bucket = target.setdefault(sym, {})
            bucket[src] = self.bdd.or_(bucket.get(src, FALSE), cond)
        for original, alias in pkg.rename.items():
            if original in hidden:
                continue
            if original not in pkg.provides:
                # rename only ever applies to a symbol the package supplies;
                # a dangling rename is harmless and ignored.
                continue
            src = Source(pid, pkg.version, original)
            bucket = target.setdefault(alias, {})
            bucket[src] = self.bdd.or_(bucket.get(src, FALSE), cond)

    # ------------------------------------------------------------------ #
    # queries
    # ------------------------------------------------------------------ #
    def exposed_names(self) -> List[str]:
        return sorted(self.exposure)

    def sources_of(self, name: str) -> Dict[Source, int]:
        return dict(self.exposure.get(name, {}))

    def condition_of(self, name: str) -> int:
        """Condition under which ``name`` is exposed (any source)."""
        acc = FALSE
        for cond in self.exposure.get(name, {}).values():
            acc = self.bdd.or_(acc, cond)
        return acc

    def witness_for(self, name: str, source: Optional[Source] = None) -> Optional[Witness]:
        bucket = self.exposure.get(name)
        if not bucket:
            return None
        if source is None:
            source = next(iter(bucket))
        cond = bucket.get(source)
        if cond is None or cond == FALSE:
            return None
        config = self.bdd.satisfy(cond)
        if config is None:
            return None
        target_node = f"{source.pkg_id}@{source.version}" if source.version else source.pkg_id
        if target_node == self.root and source.symbol == name:
            path: List[PathStep] = []
        else:
            path = self._find_path(self.root, target_node, config, build=False)
        return Witness(name, source, config, path, cond)

    def _find_path(
        self, start: str, goal: str, config: Dict[str, bool], build: bool
    ) -> List[PathStep]:
        """Shortest condition-satisfying step sequence.

        Runtime rule: at most one private edge, and it must be first; build
        edges never occur.  Build rule: only public/build edges.
        """
        bdd = self.bdd

        def ok(edge: Edge) -> bool:
            if build:
                return edge.kind in (PUBLIC, BUILD) and bdd.holds(edge.cond, config)
            if edge.kind == BUILD:
                return False
            if not bdd.holds(edge.cond, config):
                return False
            return True

        queue: List[Tuple[str, List[PathStep], bool]] = [(start, [], False)]
        seen = {(start, False)}
        while queue:
            u, path, used_private = queue.pop(0)
            for eid in self.out.get(u, ()):
                edge = self.edge_by_id[eid]
                if not ok(edge):
                    continue
                if not build and used_private:
                    continue  # nothing transits past a private edge
                priv = used_private or edge.kind == PRIVATE
                state = (edge.dst, priv)
                if state in seen:
                    continue
                seen.add(state)
                step = PathStep(edge.kind, edge.src, edge.dst, edge.cond_text, eid)
                npath = path + [step]
                if edge.dst == goal:
                    return npath
                queue.append((edge.dst, npath, priv))
        return []

    # ------------------------------------------------------------------ #
    # conflicts
    # ------------------------------------------------------------------ #
    def conflicts(self) -> List[Conflict]:
        if self._conflicts is not None:
            return self._conflicts
        out: List[Conflict] = []
        for name in sorted(self.exposure):
            bucket = self.exposure[name]
            sources = list(bucket)
            for i in range(len(sources)):
                for j in range(i + 1, len(sources)):
                    s1, s2 = sources[i], sources[j]
                    c1, c2 = bucket[s1], bucket[s2]
                    common = self.bdd.and_(c1, c2)
                    if common == FALSE:
                        continue  # mutually exclusive: not a conflict
                    config = self.bdd.satisfy(common)
                    w1 = self._make_witness(name, s1, common)
                    w2 = self._make_witness(name, s2, common)
                    out.append(
                        Conflict(
                            name=name,
                            sources=(s1, s2),
                            conditions=(c1, c2),
                            common=common,
                            config=config,
                            witnesses=(w1, w2),
                        )
                    )
        self._conflicts = out
        return out

    def _make_witness(self, name: str, source: Source, must_hold: int) -> Optional[Witness]:
        cond = self.exposure[name][source]
        joint = self.bdd.and_(cond, must_hold)
        config = self.bdd.satisfy(joint)
        if config is None:
            return None
        target_node = f"{source.pkg_id}@{source.version}" if source.version else source.pkg_id
        path: List[PathStep]
        if target_node == self.root:
            path = []
        else:
            path = self._find_path(self.root, target_node, config, build=False)
        return Witness(name, source, config, path, cond)

    # ------------------------------------------------------------------ #
    # build closure
    # ------------------------------------------------------------------ #
    def build_packages(self) -> Dict[str, int]:
        return {n: c for n, c in self.build_reach.items() if c != FALSE}

    def build_names(self) -> List[str]:
        return sorted(self.build_exposure)

    def build_condition_of(self, name: str) -> int:
        acc = FALSE
        for cond in self.build_exposure.get(name, {}).values():
            acc = self.bdd.or_(acc, cond)
        return acc

    # ------------------------------------------------------------------ #
    # incremental updates
    # ------------------------------------------------------------------ #
    def add_edge(self, edge: Edge) -> UpdateReport:
        before = self.bdd.size()
        if edge.src not in self.spec.packages or edge.dst not in self.spec.packages:
            raise ValueError("edge endpoints must be declared packages")
        # same (kind, src, dst) replaces the prior edge
        existing = self._find_edge(edge.kind, edge.src, edge.dst)
        if existing is not None:
            return self.modify_edge(existing, when=edge.cond_text, edge=edge)
        eid = self._next_id
        self._next_id += 1
        self.edges.append(edge)
        self._index_edge(eid, edge)

        touched: set = set()
        if edge.kind == PUBLIC:
            src_cond = self.rt_reach.get(edge.src, FALSE)
            delta = self.bdd.and_(src_cond, edge.cond)
            touched |= self._propagate(
                {edge.dst: delta}, self.rt_reach, (PUBLIC,)
            )
        elif edge.kind == PRIVATE:
            if edge.src == self.root:
                self.private_facts[edge.dst] = self.bdd.or_(
                    self.private_facts[edge.dst], edge.cond
                )
                touched.add(edge.dst)
        if edge.kind in (PUBLIC, BUILD):
            src_cond = self.build_reach.get(edge.src, FALSE)
            delta = self.bdd.and_(src_cond, edge.cond)
            touched |= self._propagate(
                {edge.dst: delta}, self.build_reach, (PUBLIC, BUILD)
            )
        if touched:
            self._build_exposures()
            self._conflicts = None
        return UpdateReport("add", eid, sorted(touched), self.bdd.size(),
                            self.bdd.size() - before)

    def _find_edge(self, kind: str, src: str, dst: str) -> Optional[int]:
        for eid in self.out.get(src, ()):
            edge = self.edge_by_id[eid]
            if edge.kind == kind and edge.dst == dst:
                return eid
        return None

    def remove_edge(self, eid: int) -> UpdateReport:
        before = self.bdd.size()
        edge = self.edge_by_id.get(eid)
        if edge is None:
            raise KeyError(f"unknown edge id {eid}")
        self._unindex_edge(eid)
        self.edges = [e for e in self.edges if e is not edge]

        touched: set = set()
        if edge.kind == PUBLIC:
            touched |= self._dred(edge, self.rt_reach, (PUBLIC,), private_facts=True)
            touched |= self._dred(edge, self.build_reach, (PUBLIC, BUILD))
        elif edge.kind == BUILD:
            touched |= self._dred(edge, self.build_reach, (PUBLIC, BUILD))
        elif edge.kind == PRIVATE:
            # private facts do not propagate, so simple subtraction suffices
            cur = self.private_facts[edge.dst]
            self.private_facts[edge.dst] = self._subtract(cur, edge.cond)
            touched.add(edge.dst)
        self._build_exposures()
        self._conflicts = None
        return UpdateReport("remove", eid, sorted(touched), self.bdd.size(),
                            self.bdd.size() - before)

    def modify_edge(self, eid: int, when: Optional[str] = None,
                    edge: Optional[Edge] = None) -> UpdateReport:
        old = self.edge_by_id[eid]
        kind, src, dst = old.kind, old.src, old.dst
        cond_text = when if when is not None else (edge.cond_text if edge else old.cond_text)
        rem = self.remove_edge(eid)
        new_edge = Edge(
            kind, src, dst,
            self.spec.expr.parse(cond_text), cond_text,
            dict(edge.rename) if edge else dict(old.rename),
            list(edge.hide) if edge else list(old.hide),
        )
        add = self.add_edge(new_edge)
        touched = sorted(set(rem.touched_nodes) | set(add.touched_nodes))
        return UpdateReport(
            "modify", add.edge_id, touched, self.bdd.size(),
            self.bdd.size() - rem.bdd_nodes_total,
        )

    # ------------------------------------------------------------------ #
    # delete-and-rederive
    # ------------------------------------------------------------------ #
    def _dred(
        self,
        removed: Edge,
        state: Dict[str, int],
        kinds: Tuple[str, ...],
        private_facts: bool = False,
    ) -> set:
        """Delete-and-rederive repair for one removed edge.

        1. Over-estimate the possibly-lost set S: everything still
           reachable (via ``kinds`` edges) from the removed edge's target in
           the graph *after* removal.
        2. Subtract: clear reachability on S (this breaks cycle-supported
           residue; reference counting could not).
        3. Re-derive: seed S from the root, direct private facts (runtime
           context only) and every surviving edge crossing into S from
           outside, then take the least fixed point restricted to S.
        """
        bdd = self.bdd
        start = removed.dst
        affected = {start}
        stack = [start]
        while stack:
            u = stack.pop()
            for eid in self.out.get(u, ()):
                edge = self.edge_by_id[eid]
                if edge.kind in kinds and edge.dst not in affected:
                    affected.add(edge.dst)
                    stack.append(edge.dst)

        old_values = {n: state[n] for n in affected}
        for n in affected:
            state[n] = FALSE

        seeds: Dict[str, int] = {}
        if self.root in affected:
            seeds[self.root] = TRUE
        if private_facts:
            for n, cond in self.private_facts.items():
                if n in affected and cond != FALSE:
                    seeds[n] = bdd.or_(seeds.get(n, FALSE), cond)
        # edges crossing the S boundary from outside
        for n in affected:
            for eid in self.incoming.get(n, ()):
                edge = self.edge_by_id[eid]
                if edge.kind not in kinds:
                    continue
                if edge.src in affected:
                    continue
                pred_cond = state.get(edge.src, FALSE)
                contributed = bdd.and_(pred_cond, edge.cond)
                if contributed != FALSE:
                    seeds[n] = bdd.or_(seeds.get(n, FALSE), contributed)

        self._propagate(seeds, state, kinds, limit=affected)

        touched = {n for n in affected if state[n] != old_values[n]}
        return touched

    def _subtract(self, cond: int, removed_disjunct: int) -> int:
        return self.bdd.and_(cond, self.bdd.not_(removed_disjunct))

    # ------------------------------------------------------------------ #
    # misc
    # ------------------------------------------------------------------ #
    def stats(self) -> Dict[str, int]:
        return {
            "packages": len(self.spec.packages),
            "edges": len(self.edges),
            "features": len(self.bdd.var_order),
            "bdd_nodes": self.bdd.size(),
            "exposed_names": len(self.exposure),
            "conflicts": len(self.conflicts()),
        }

    def condition_text(self, node: int) -> str:
        """Render a BDD condition as a parenthesized formula over features."""
        if node == TRUE:
            return "1"
        if node == FALSE:
            return "0"

        def render(n: int) -> str:
            if n == TRUE:
                return "1"
            if n == FALSE:
                return "0"
            v, lo, hi = self.bdd.nodes[n]
            name = self.bdd.var_order[v]
            terms = []
            if hi != FALSE:
                terms.append(name if hi == TRUE else f"({name} & {render(hi)})")
            if lo != FALSE:
                terms.append(f"(!{name} & {render(lo)})" if lo != TRUE else f"!{name}")
            return " | ".join(terms)

        return render(node)

    def reachable_under(self, config: Dict[str, bool], build: bool = False) -> List[str]:
        state = self.build_reach if build else self._visible_packages()
        return sorted(n for n, c in state.items() if self.bdd.holds(c, config))

    def exposed_under(self, config: Dict[str, bool], build: bool = False):
        table = self.build_exposure if build else self.exposure
        out: Dict[str, List[Source]] = {}
        for name, bucket in table.items():
            srcs = [s for s, c in bucket.items() if self.bdd.holds(c, config)]
            if srcs:
                out[name] = srcs
        return out

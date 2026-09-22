"""Staged build planning with co-compiled SCC groups and hard-edge cycle rejection.

Edge convention
---------------
All edges point *from a prerequisite to the package that consumes it*, so a
topological traversal builds prerequisites first.

* soft edges (``depends_on``) may form cycles; an SCC containing only soft
  edges becomes a single co-compilation group whose members have no fake
  internal topological order;
* hard edges (a package ``needs_artifacts`` produced by another package, i.e.
  generator -> generated) must always go between earlier and later stages.
  Any cycle containing a hard edge is a forbidden cross-stage cycle and is
  reported with the concrete edges involved.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .model import Catalog

SOFT = "soft"
HARD = "hard"


@dataclass(frozen=True)
class Edge:
    source: str
    target: str
    kind: str
    label: str


@dataclass
class CycleEvidence:
    nodes: tuple[str, ...]
    edges: tuple[Edge, ...]

    def render(self) -> str:
        parts = [self.edges[0].source]
        for edge in self.edges:
            parts.append(f"--{edge.kind}:{edge.label}-->{edge.target}")
        return "".join(parts)


@dataclass
class BlockedGroup:
    scc_id: str
    members: tuple[str, ...]
    codes: tuple[str, ...]
    reasons: tuple[str, ...]
    cycles: tuple[CycleEvidence, ...]
    blocking_downstream: tuple[str, ...] = ()


@dataclass
class ExecutableGroup:
    scc_id: str
    stage: int
    members: tuple[str, ...]
    internal_edges: tuple[Edge, ...]
    # Evidence of why this group is skipped: an invalid upstream SCC plus a
    # concrete hard/soft chain leading into this SCC.


@dataclass
class PlanReport:
    stages: list[ExecutableGroup]
    blocked: list[BlockedGroup]
    package_errors: dict[str, str]
    warnings: tuple[str, ...]
    in_scope: tuple[str, ...]
    targets: tuple[str, ...]
    total_packages: int

    @property
    def ok(self) -> bool:
        return not self.blocked and not self.package_errors

    def to_dict(self) -> dict:
        return {
            "ok": self.ok,
            "targets": list(self.targets),
            "in_scope": list(self.in_scope),
            "total_packages": self.total_packages,
            "warnings": list(self.warnings),
            "package_errors": self.package_errors,
            "stages": [
                {
                    "stage": group.stage,
                    "group": group.scc_id,
                    "action": "co-compile" if len(group.members) > 1 else "compile",
                    "members": list(group.members),
                    "internal_order": "unspecified (soft cycles within group)",
                    "internal_edges": [_edge_dict(e) for e in group.internal_edges],
                }
                for group in self.stages
            ],
            "blocked": [
                {
                    "group": blocked.scc_id,
                    "members": list(blocked.members),
                    "codes": list(blocked.codes),
                    "reasons": list(blocked.reasons),
                    "cycles": [
                        {"nodes": list(c.nodes), "path": c.render()} for c in blocked.cycles
                    ],
                    "blocking_downstream": list(blocked.blocking_downstream),
                }
                for blocked in self.blocked
            ],
        }


def _edge_dict(edge: Edge) -> dict:
    return {"from": edge.source, "to": edge.target, "kind": edge.kind, "label": edge.label}


def _add_edge(edges: dict[tuple[str, str], Edge], edge: Edge) -> None:
    key = (edge.source, edge.target)
    existing = edges.get(key)
    if existing is None:
        edges[key] = edge
        return
    # Hard requirements win over soft labels when the same pair is related both
    # ways; all labels are preserved for evidence output.
    label = existing.label
    if edge.label not in label.split(", "):
        label = ", ".join(sorted({*label.split(", "), edge.label}))
    kind = HARD if HARD in (existing.kind, edge.kind) else SOFT
    edges[key] = Edge(existing.source, existing.target, kind, label)


def build_edges(catalog: Catalog) -> tuple[list[Edge], dict[str, str]]:
    """Construct prerequisite -> consumer edges.

    Returns the edge list plus per-package blocker reasons found while wiring
    dependencies (unknown packages, failed version constraints, unknown
    artifacts).
    """
    edges: dict[tuple[str, str], Edge] = {}
    blockers: dict[str, str] = {}

    def block(package: str, reason: str) -> None:
        prior = blockers.get(package)
        blockers[package] = reason if prior is None else f"{prior}; {reason}"

    for name, package in sorted(catalog.packages.items()):
        for dep_name, constraint in package.depends_on:
            dependency = catalog.packages.get(dep_name)
            if dependency is None:
                block(name, f"unknown dependency '{dep_name}'")
                continue
            if not constraint.satisfies(dependency.version):
                block(
                    name,
                    f"dependency '{dep_name}' version {dependency.version} "
                    f"does not satisfy {constraint.raw}",
                )
            _add_edge(edges, Edge(dep_name, name, SOFT, f"depends:{dep_name}"))
        for artifact in package.needs_artifacts:
            producer = catalog.producer.get(artifact)
            if producer is None:
                if artifact in catalog.duplicate_artifacts:
                    producers = ", ".join(catalog.duplicate_artifacts[artifact])
                    block(name, f"artifact '{artifact}' has duplicate producers: {producers}")
                else:
                    block(name, f"needs unknown artifact '{artifact}' (no producer)")
                continue
            if producer == name:
                block(name, f"package produces and needs its own artifact '{artifact}'")
            _add_edge(edges, Edge(producer, name, HARD, f"artifact:{artifact}"))

    for artifact, producers in sorted(catalog.duplicate_artifacts.items()):
        for producer in producers:
            package = catalog.packages.get(producer)
            if package is not None and artifact in package.produces:
                block(producer, f"duplicate producer for artifact '{artifact}'")

    return list(edges.values()), blockers


def _adjacency(nodes: set[str], edges: list[Edge]) -> tuple[dict[str, list[str]], dict[str, list[str]]]:
    forward: dict[str, list[str]] = {node: [] for node in nodes}
    reverse: dict[str, list[str]] = {node: [] for node in nodes}
    for edge in edges:
        if edge.source in nodes and edge.target in nodes:
            forward[edge.source].append(edge.target)
            reverse[edge.target].append(edge.source)
    for graph in (forward, reverse):
        for targets in graph.values():
            targets.sort()
    return forward, reverse


def _kosaraju(nodes: set[str], edges: list[Edge]) -> list[list[str]]:
    forward, reverse = _adjacency(nodes, edges)
    visited: set[str] = set()
    order: list[str] = []
    for start in sorted(nodes):
        if start in visited:
            continue
        stack: list[tuple[str, int]] = [(start, 0)]
        visited.add(start)
        while stack:
            node, idx = stack[-1]
            neighbors = forward[node]
            if idx < len(neighbors):
                stack[-1] = (node, idx + 1)
                nxt = neighbors[idx]
                if nxt not in visited:
                    visited.add(nxt)
                    stack.append((nxt, 0))
            else:
                order.append(node)
                stack.pop()

    components: list[list[str]] = []
    assigned: set[str] = set()
    for start in reversed(order):
        if start in assigned:
            continue
        component: list[str] = []
        stack = [start]
        assigned.add(start)
        while stack:
            node = stack.pop()
            component.append(node)
            for nxt in reverse[node]:
                if nxt not in assigned:
                    assigned.add(nxt)
                    stack.append(nxt)
        components.append(sorted(component))
    components.sort(key=lambda component: component[0])
    return components


def _internal_cycle(members: set[str], member_edges: list[Edge]) -> tuple[str, ...] | None:
    """Find one directed cycle within the SCC, preferring one that uses a hard edge."""
    hard = [edge for edge in member_edges if edge.kind == HARD]
    anchors = sorted({(edge.source, edge.target) for edge in hard}) or sorted(
        {(edge.source, edge.target) for edge in member_edges}
    )
    forward: dict[str, list[str]] = {node: [] for node in members}
    for edge in member_edges:
        forward[edge.source].append(edge.target)
    for targets in forward.values():
        targets.sort()
    for source, target in anchors:
        if source == target:
            return (source, target)
        path = _shortest_path(target, source, forward, members)
        if path is not None:
            return (source, *path)
    return None


def _shortest_path(start: str, goal: str, forward: dict[str, list[str]], allowed: set[str]):
    queue = [start]
    parent: dict[str, str | None] = {start: None}
    head = 0
    while head < len(queue):
        node = queue[head]
        head += 1
        if node == goal:
            path: list[str] = []
            cursor: str | None = node
            while cursor is not None:
                path.append(cursor)
                cursor = parent[cursor]
            return tuple(reversed(path))
        for nxt in forward.get(node, []):
            if nxt in allowed and nxt not in parent:
                parent[nxt] = node
                queue.append(nxt)
    return None


def _edges_for_path(path: tuple[str, ...], edge_index: dict[tuple[str, str], Edge]) -> tuple[Edge, ...]:
    result: list[Edge] = []
    for source, target in zip(path, path[1:]):
        result.append(edge_index[(source, target)])
    return tuple(result)


def plan(catalog: Catalog, targets: list[str] | None = None) -> PlanReport:
    edges, edge_blockers = build_edges(catalog)
    nodes = set(catalog.packages)
    components = _kosaraju(nodes, edges)
    width = max(2, len(str(len(components))))
    scc_id = {node: f"G{idx + 1:0{width}d}" for idx, component in enumerate(components) for node in component}

    edge_index = {(edge.source, edge.target): edge for edge in edges}
    member_lookup = {scc_id[component[0]]: tuple(component) for component in components}

    invalid: dict[str, dict] = {}
    for component in components:
        gid = scc_id[component[0]]
        members = set(component)
        internal = [edge for edge in edges if edge.source in members and edge.target in members]
        reasons: list[str] = []
        codes: list[str] = []
        cycles: list[CycleEvidence] = []
        if any(edge.kind == HARD for edge in internal):
            loop = _internal_cycle(members, internal)
            assert loop is not None
            evidence = CycleEvidence(loop, _edges_for_path(loop, edge_index))
            cycles.append(evidence)
            reasons.append(
                "forbidden cross-stage cycle: a generator depends on its own artifact chain"
            )
            codes.append("CROSS_STAGE_CYCLE")
        bad_packages = sorted(p for p in component if p in edge_blockers)
        for package in bad_packages:
            reasons.append(f"{package}: {edge_blockers[package]}")
            codes.append("DEPENDENCY_BLOCKED")
        if reasons:
            invalid[gid] = {
                "members": tuple(component),
                "reasons": tuple(reasons),
                "codes": tuple(dict.fromkeys(codes)),
                "cycles": tuple(cycles),
            }

    # Condensation DAG on SCC ids (edges still point prerequisite -> consumer).
    scc_forward: dict[str, set[str]] = {gid: set() for gid in member_lookup}
    scc_reverse: dict[str, set[str]] = {gid: set() for gid in member_lookup}
    for edge in edges:
        a, b = scc_id[edge.source], scc_id[edge.target]
        if a != b:
            scc_forward[a].add(b)
            scc_reverse[b].add(a)

    target_nodes = set(catalog.packages)
    unknown_targets: list[str] = []
    if targets:
        wanted = set(targets)
        unknown_targets = sorted(wanted - set(catalog.packages))
        # A target's build needs its prerequisite closure only (reverse
        # traversal of prerequisite -> consumer edges).  Downstream consumers
        # are not pulled into the build.
        target_nodes = set()
        stack = sorted(p for p in wanted if p in catalog.packages)
        while stack:
            node = stack.pop()
            if node in target_nodes:
                continue
            target_nodes.add(node)
            stack.extend(edge.source for edge in edges if edge.target == node)

    def reachable(start: str, graph: dict[str, set[str]]) -> set[str]:
        seen = {start}
        stack = [start]
        while stack:
            node = stack.pop()
            for nxt in graph.get(node, ()):  # downstream consumers
                if nxt not in seen:
                    seen.add(nxt)
                    stack.append(nxt)
        return seen

    contaminated: dict[str, dict[str, tuple[Edge, ...]]] = {}
    downstream_of_invalid: set[str] = set()
    for gid in invalid:
        for downstream in reachable(gid, scc_forward) - {gid}:
            downstream_of_invalid.add(downstream)
            chain = _scc_chain(gid, downstream, scc_forward, scc_id, edges, invalid)
            contaminated.setdefault(downstream, {})[gid] = chain

    scope_sccs = {gid for gid, members in member_lookup.items() if any(m in target_nodes for m in members)}
    invalid_in_scope = {gid: data for gid, data in invalid.items() if gid in scope_sccs}
    blocked_set = set(invalid_in_scope) | (downstream_of_invalid & scope_sccs)
    executable_ids = [
        gid for gid in member_lookup
        if gid in scope_sccs and gid not in blocked_set
    ]

    level_cache: dict[str, int] = {}

    def level(gid: str) -> int:
        if gid in level_cache:
            return level_cache[gid]
        prerequisites = [p for p in scc_reverse[gid] if p in executable_ids]
        value = 0 if not prerequisites else 1 + max(level(p) for p in prerequisites)
        level_cache[gid] = value
        return value

    stages: list[ExecutableGroup] = []
    for gid in executable_ids:
        members = set(member_lookup[gid])
        internal = [edge for edge in edges if edge.source in members and edge.target in members]
        internal.sort(key=lambda e: (e.source, e.target, e.kind))
        stages.append(
            ExecutableGroup(
                scc_id=gid,
                stage=level(gid) + 1,
                members=member_lookup[gid],
                internal_edges=tuple(internal),
            )
        )
    stages.sort(key=lambda g: (g.stage, g.scc_id))

    blocked_groups: list[BlockedGroup] = []
    for gid in sorted(blocked_set):
        members = member_lookup[gid]
        if gid in invalid_in_scope:
            data = invalid_in_scope[gid]
            downstream = sorted(
                other for other in contaminated if gid in contaminated[other] and other in scope_sccs
            )
            blocked_groups.append(
                BlockedGroup(
                    scc_id=gid,
                    members=members,
                    codes=data["codes"],
                    reasons=data["reasons"],
                    cycles=data["cycles"],
                    blocking_downstream=tuple(downstream),
                )
            )
        else:
            chains = tuple(
                (root, contaminated[gid][root])
                for root in sorted(contaminated.get(gid, {}))
                if root in invalid_in_scope
            )
            reasons = tuple(
                f"cannot build: upstream group {root} is blocked ("
                + invalid_in_scope[root]["reasons"][0]
                + "); chain: "
                + _render_chain(chain)
                for root, chain in chains
            )
            blocked_groups.append(
                BlockedGroup(
                    scc_id=gid,
                    members=members,
                    codes=("UPSTREAM_BLOCKED",),
                    reasons=reasons,
                    cycles=(),
                )
            )
    blocked_groups.sort(key=lambda b: b.scc_id)

    requested = set(targets or ())
    if requested:
        scoped_errors = {name: catalog.package_errors[name] for name in requested if name in catalog.package_errors}
    else:
        scoped_errors = dict(catalog.package_errors)
    warnings: list[str] = []
    for name in sorted(scoped_errors):
        warnings.append(f"excluded package {name}: {scoped_errors[name]}")
    for target in unknown_targets:
        warnings.append(f"unknown target ignored: {target}")

    in_scope = tuple(sorted(target_nodes & set(catalog.packages)))
    return PlanReport(
        stages=stages,
        blocked=blocked_groups,
        package_errors=scoped_errors,
        warnings=tuple(warnings),
        in_scope=in_scope,
        targets=tuple(targets or ()),
        total_packages=len(catalog.packages),
    )


def _scc_chain(start: str, goal: str, scc_forward, scc_id, edges, invalid) -> tuple[Edge, ...]:
    queue = [(start, ())]
    seen = {start}
    head = 0
    while head < len(queue):
        gid, chain = queue[head]
        head += 1
        if gid == goal:
            return chain
        for nxt in sorted(scc_forward[gid]):
            if nxt in seen:
                continue
            seen.add(nxt)
            bridge = min(
                (e for e in edges if scc_id[e.source] == gid and scc_id[e.target] == nxt),
                key=lambda e: (e.kind != HARD, e.source, e.target),
            )
            queue.append((nxt, (*chain, bridge)))
    return ()


def _render_chain(chain: tuple[Edge, ...]) -> str:
    if not chain:
        return ""
    parts = [chain[0].source]
    for edge in chain:
        parts.append(f" --{edge.kind}:{edge.label}--> {edge.target}")
    return "".join(parts)

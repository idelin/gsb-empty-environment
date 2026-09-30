"""Core model, exact solver, constraint evidence and exhaustive verifier."""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import combinations
from typing import Any


class InstanceError(ValueError):
    """Raised when an input instance is malformed."""


@dataclass(frozen=True)
class Change:
    """A single releasable change."""

    id: str
    benefit: int
    cost: int
    depends_on: tuple[str, ...] = ()


@dataclass(frozen=True)
class Instance:
    """A release-selection problem instance."""

    capacity: int
    changes: tuple[Change, ...]
    mutex_groups: tuple[tuple[str, ...], ...] = ()

    @property
    def change_map(self) -> dict[str, Change]:
        return {c.id: c for c in self.changes}


@dataclass(frozen=True)
class Solution:
    """An optimal selection together with constraint evidence."""

    selected_ids: tuple[str, ...]
    total_benefit: int
    total_cost: int
    evidence: tuple[dict[str, Any], ...]


@dataclass(frozen=True)
class VerificationReport:
    """Result of the independent exhaustive verification."""

    method: str
    subsets_enumerated: int
    feasible_subsets: int
    best_benefit: int
    best_ids: tuple[str, ...]
    matches_solution: bool
    proved_optimal: bool


def _require_int(name: str, value: Any) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise InstanceError(f"{name} must be an integer, got {value!r}")
    return value


def parse_instance(data: dict[str, Any]) -> Instance:
    """Validate and parse a JSON-decoded document into an Instance."""
    if not isinstance(data, dict):
        raise InstanceError("top-level document must be an object")
    if "capacity" not in data:
        raise InstanceError("missing required field 'capacity'")
    capacity = _require_int("capacity", data["capacity"])
    if capacity < 0:
        raise InstanceError("capacity must be >= 0")

    raw_changes = data.get("changes", [])
    if not isinstance(raw_changes, list):
        raise InstanceError("'changes' must be a list")

    changes: list[Change] = []
    seen: set[str] = set()
    for i, raw in enumerate(raw_changes):
        if not isinstance(raw, dict):
            raise InstanceError(f"changes[{i}] must be an object")
        cid = raw.get("id")
        if not isinstance(cid, str) or not cid:
            raise InstanceError(f"changes[{i}].id must be a non-empty string")
        if cid in seen:
            raise InstanceError(f"duplicate change id {cid!r}")
        seen.add(cid)
        benefit = _require_int(f"changes[{i}].benefit", raw.get("benefit"))
        cost = _require_int(f"changes[{i}].cost", raw.get("cost"))
        if cost < 0:
            raise InstanceError(f"changes[{i}].cost must be >= 0")
        deps = raw.get("depends_on", [])
        if not isinstance(deps, list) or not all(isinstance(d, str) for d in deps):
            raise InstanceError(f"changes[{i}].depends_on must be a list of strings")
        if len(set(deps)) != len(deps):
            raise InstanceError(f"changes[{i}].depends_on contains duplicates")
        changes.append(Change(id=cid, benefit=benefit, cost=cost,
                              depends_on=tuple(deps)))

    for c in changes:
        for dep in c.depends_on:
            if dep not in seen:
                raise InstanceError(
                    f"change {c.id!r} depends on unknown change {dep!r}")

    raw_mutex = data.get("mutex_groups", [])
    if not isinstance(raw_mutex, list):
        raise InstanceError("'mutex_groups' must be a list")
    mutex_groups: list[tuple[str, ...]] = []
    for i, group in enumerate(raw_mutex):
        if (not isinstance(group, list) or len(group) < 2
                or not all(isinstance(g, str) for g in group)):
            raise InstanceError(
                f"mutex_groups[{i}] must be a list of >= 2 change ids")
        if len(set(group)) != len(group):
            raise InstanceError(f"mutex_groups[{i}] contains duplicates")
        for gid in group:
            if gid not in seen:
                raise InstanceError(
                    f"mutex_groups[{i}] references unknown change {gid!r}")
        mutex_groups.append(tuple(group))

    return Instance(capacity=capacity, changes=tuple(changes),
                    mutex_groups=tuple(mutex_groups))


def _strongly_connected_components(
        nodes: list[str],
        edges: dict[str, tuple[str, ...]]) -> list[list[str]]:
    """Tarjan's SCC algorithm (iterative). Returns components in reverse
    topological order of the condensation DAG."""
    index_of: dict[str, int] = {}
    lowlink: dict[str, int] = {}
    on_stack: set[str] = set()
    stack: list[str] = []
    components: list[list[str]] = []
    counter = 0

    for root in nodes:
        if root in index_of:
            continue
        work: list[tuple[str, int]] = [(root, 0)]
        while work:
            node, child_idx = work[-1]
            if child_idx == 0:
                index_of[node] = lowlink[node] = counter
                counter += 1
                stack.append(node)
                on_stack.add(node)
            recurse = False
            deps = edges[node]
            i = child_idx
            while i < len(deps):
                succ = deps[i]
                if succ not in index_of:
                    work[-1] = (node, i + 1)
                    work.append((succ, 0))
                    recurse = True
                    break
                if succ in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[succ])
                i += 1
            if recurse:
                continue
            if lowlink[node] == index_of[node]:
                comp: list[str] = []
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    comp.append(w)
                    if w == node:
                        break
                components.append(sorted(comp))
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
    return components


def _objective_key(ids: tuple[str, ...], benefit: int) -> tuple:
    """Ordering key: higher benefit wins, then fewer changes, then
    lexicographically smaller sorted id tuple. Use with min()."""
    return (-benefit, len(ids), ids)


def _is_feasible(selected: frozenset[str], instance: Instance) -> bool:
    cmap = instance.change_map
    if sum(cmap[c].cost for c in selected) > instance.capacity:
        return False
    for cid in selected:
        for dep in cmap[cid].depends_on:
            if dep not in selected:
                return False
    for group in instance.mutex_groups:
        hits = sum(1 for g in group if g in selected)
        if hits > 1:
            return False
    return True


def _build_evidence(selected: frozenset[str], instance: Instance,
                    components: list[list[str]]) -> tuple[dict[str, Any], ...]:
    cmap = instance.change_map
    evidence: list[dict[str, Any]] = []

    used = sum(cmap[c].cost for c in selected)
    evidence.append({
        "constraint": "capacity",
        "used": used,
        "limit": instance.capacity,
        "satisfied": used <= instance.capacity,
    })

    for cid in sorted(selected):
        for dep in cmap[cid].depends_on:
            evidence.append({
                "constraint": "dependency",
                "change": cid,
                "requires": dep,
                "satisfied": dep in selected,
            })

    for comp in components:
        if len(comp) > 1:
            picked = [c for c in comp if c in selected]
            evidence.append({
                "constraint": "dependency_cycle_group",
                "members": comp,
                "selected_members": sorted(picked),
                "satisfied": len(picked) in (0, len(comp)),
            })

    for group in instance.mutex_groups:
        picked = sorted(g for g in group if g in selected)
        evidence.append({
            "constraint": "mutex",
            "group": sorted(group),
            "selected_in_group": picked,
            "satisfied": len(picked) <= 1,
        })

    return tuple(evidence)


def solve(instance: Instance) -> Solution:
    """Exact solver.

    Condenses the dependency graph into strongly connected components
    (cycles become atomic units that are taken as a whole or not at all),
    then exhaustively enumerates subsets of the resulting DAG components,
    keeping only dependency-closed, capacity- and mutex-feasible ones.
    """
    cmap = instance.change_map
    ids = sorted(cmap)
    edges = {cid: cmap[cid].depends_on for cid in ids}
    components = _strongly_connected_components(ids, edges)

    comp_of: dict[str, int] = {}
    for idx, comp in enumerate(components):
        for cid in comp:
            comp_of[cid] = idx

    comp_deps: list[set[int]] = [set() for _ in components]
    for cid in ids:
        for dep in cmap[cid].depends_on:
            a, b = comp_of[cid], comp_of[dep]
            if a != b:
                comp_deps[a].add(b)

    comp_benefit = [sum(cmap[c].benefit for c in comp) for comp in components]
    comp_cost = [sum(cmap[c].cost for c in comp) for comp in components]

    n = len(components)
    best_key: tuple | None = None
    best_ids: tuple[str, ...] = ()
    best_benefit = 0
    best_cost = 0

    for mask in range(1 << n):
        closed = True
        for i in range(n):
            if mask >> i & 1:
                for d in comp_deps[i]:
                    if not (mask >> d & 1):
                        closed = False
                        break
            if not closed:
                break
        if not closed:
            continue
        cost = sum(comp_cost[i] for i in range(n) if mask >> i & 1)
        if cost > instance.capacity:
            continue
        selected = frozenset(
            cid for i in range(n) if mask >> i & 1 for cid in components[i])
        if not _is_feasible(selected, instance):
            continue
        benefit = sum(comp_benefit[i] for i in range(n) if mask >> i & 1)
        ids_tuple = tuple(sorted(selected))
        key = _objective_key(ids_tuple, benefit)
        if best_key is None or key < best_key:
            best_key = key
            best_ids = ids_tuple
            best_benefit = benefit
            best_cost = cost

    selected_set = frozenset(best_ids)
    evidence = _build_evidence(selected_set, instance, components)
    return Solution(selected_ids=best_ids, total_benefit=best_benefit,
                    total_cost=best_cost, evidence=evidence)


def exhaustive_verify(instance: Instance, solution: Solution) -> VerificationReport:
    """Independent exact verifier.

    Enumerates every subset of the *raw* changes (2^n, no SCC
    condensation), checks feasibility directly from the constraints, and
    re-derives the optimum. Confirms the solver's solution matches.
    """
    ids = sorted(instance.change_map)
    n = len(ids)
    enumerated = 0
    feasible = 0
    best_key: tuple | None = None
    best_ids: tuple[str, ...] = ()
    best_benefit = 0

    for r in range(n + 1):
        for combo in combinations(ids, r):
            enumerated += 1
            selected = frozenset(combo)
            if not _is_feasible(selected, instance):
                continue
            feasible += 1
            benefit = sum(instance.change_map[c].benefit for c in combo)
            ids_tuple = tuple(sorted(combo))
            key = _objective_key(ids_tuple, benefit)
            if best_key is None or key < best_key:
                best_key = key
                best_ids = ids_tuple
                best_benefit = benefit

    matches = (best_ids == tuple(solution.selected_ids)
               and best_benefit == solution.total_benefit)
    return VerificationReport(
        method="exhaustive-enumeration-2^n",
        subsets_enumerated=enumerated,
        feasible_subsets=feasible,
        best_benefit=best_benefit,
        best_ids=best_ids,
        matches_solution=matches,
        proved_optimal=matches,
    )

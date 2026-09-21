"""Resolve the minimal actual build set for one target, with evidence.

Rules implemented here:
- Only edges whose condition holds for (target os, target arch, stage)
  contribute requirements; false-condition dependencies never participate
  in version conflicts.
- Tools (kind == "tool", e.g. code generators) run on the host and are
  keyed by host os/arch, so two targets share the same host tool cache.
- Libraries (kind == "library") are compiled for the target and keyed by
  target os/arch.
"""
from __future__ import annotations

import heapq
from collections import defaultdict, deque
from dataclasses import dataclass

from .conditions import evaluate
from .registry import Package


class ConflictError(Exception):
    """Raised when taken edges require incompatible versions of one package."""

    def __init__(self, name: str, requirements):
        self.name = name
        self.requirements = list(requirements)  # [(Constraint, source)]
        detail = ", ".join(f"{c} (required by {s})" for c, s in self.requirements)
        super().__init__(f"version conflict for {name!r}: {detail}")


@dataclass(frozen=True)
class EdgeEvidence:
    package: str      # "name@version" of the dependent
    dependency: str
    constraint: str
    condition: dict
    checks: tuple     # of FieldCheck
    taken: bool


@dataclass(frozen=True)
class BuildNode:
    name: str
    version: str
    kind: str
    partition: str    # "host" | "target"
    cache_key: tuple  # (name, version, os, arch) of the partition it runs on


@dataclass
class Resolution:
    context: dict
    nodes: list       # BuildNode, dependency-first build order
    evidence: list    # EdgeEvidence for every evaluated edge

    @property
    def pruned(self) -> list:
        return [e for e in self.evidence if not e.taken]


def cache_key(pkg: Package, context: dict) -> tuple:
    if pkg.is_tool:
        return (pkg.name, str(pkg.version), context["host_os"], context["host_arch"])
    return (pkg.name, str(pkg.version), context["target_os"], context["target_arch"])


def resolve(registry, roots, context: dict) -> Resolution:
    """roots: iterable of (name, Constraint). context keys: host_os, host_arch,
    target_os, target_arch, stage."""
    # Edge conditions are evaluated against the target, never the host.
    eval_ctx = {
        "os": context["target_os"],
        "arch": context["target_arch"],
        "stage": context["stage"],
    }
    requirements: dict = {}   # name -> [(Constraint, source)]
    selected: dict = {}       # name -> Package
    queue = deque((name, constraint, "<root>") for name, constraint in roots)
    while queue:
        name, constraint, source = queue.popleft()
        requirements.setdefault(name, []).append((constraint, source))
        constraints = [c for c, _ in requirements[name]]
        pkg = registry.best_match(name, constraints)
        if pkg is None:
            raise ConflictError(name, requirements[name])
        if selected.get(name) != pkg:
            selected[name] = pkg
            for dep in pkg.dependencies:
                if evaluate(dep.condition, eval_ctx).taken:
                    queue.append((dep.name, dep.constraint, str(pkg)))

    evidence = []
    for pkg in selected.values():
        for dep in pkg.dependencies:
            result = evaluate(dep.condition, eval_ctx)
            evidence.append(
                EdgeEvidence(
                    package=str(pkg),
                    dependency=dep.name,
                    constraint=str(dep.constraint),
                    condition=dep.condition.as_dict(),
                    checks=result.checks,
                    taken=result.taken,
                )
            )

    order = _topo_order(selected, eval_ctx)
    nodes = [
        BuildNode(
            name=pkg.name,
            version=str(pkg.version),
            kind=pkg.kind,
            partition="host" if pkg.is_tool else "target",
            cache_key=cache_key(pkg, context),
        )
        for pkg in (selected[name] for name in order)
    ]
    return Resolution(context=dict(context), nodes=nodes, evidence=evidence)


def _topo_order(selected: dict, eval_ctx: dict) -> list:
    deps = {name: set() for name in selected}
    dependents = defaultdict(set)
    for pkg in selected.values():
        for dep in pkg.dependencies:
            if evaluate(dep.condition, eval_ctx).taken:
                deps[pkg.name].add(dep.name)
                dependents[dep.name].add(pkg.name)
    ready = [n for n, d in deps.items() if not d]
    heapq.heapify(ready)
    order = []
    while ready:
        name = heapq.heappop(ready)
        order.append(name)
        for other in dependents[name]:
            deps[other].discard(name)
            if not deps[other]:
                heapq.heappush(ready, other)
    if len(order) != len(selected):
        raise ValueError("dependency cycle detected among selected packages")
    return order

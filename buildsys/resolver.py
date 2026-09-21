"""Resolution: evaluate conditional edges, pick versions, detect conflicts,
and emit the minimal build set plus full condition-evaluation evidence."""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass

from .model import PackageIndex, TargetContext
from .version import Constraint, Version


class BuildsysError(Exception):
    """Base class for resolution failures."""


class UnresolvedError(BuildsysError):
    """A required (true-condition) package is absent from the local index."""


class ConflictError(BuildsysError):
    """True-condition requirements on one package cannot be co-satisfied."""


class CycleError(BuildsysError):
    """The included dependency graph contains a cycle."""


@dataclass
class Requirement:
    constraint: Constraint
    source: str  # human-readable origin, e.g. "app-1.0.0" or "<root>"


@dataclass
class EdgeEvidence:
    """One evaluated dependency edge (included or not)."""

    parent_key: tuple  # (domain, name) of the requiring package
    parent_version: Version
    name: str
    constraint: Constraint
    child_domain: str
    condition: object  # Condition
    evaluation: object  # ConditionEvaluation
    included: bool
    reason: str

    @property
    def child_key(self) -> tuple:
        return (self.child_domain, self.name)

    def to_json(self) -> dict:
        parent = f"{self.parent_key[1]}-{self.parent_version}"
        return {
            "parent": parent,
            "parent_domain": self.parent_key[0],
            "package": self.name,
            "constraint": str(self.constraint),
            "domain": self.child_domain,
            "condition": self.condition.to_json(),
            "evaluation": self.evaluation.to_json(),
            "included": self.included,
            "reason": self.reason,
        }


@dataclass
class Resolution:
    context: TargetContext
    selected: dict  # (domain, name) -> Version
    edges: list  # list of EdgeEvidence (final, deduplicated)
    build_order: list  # list of (domain, name), dependencies first

    def selected_json(self) -> list:
        return [
            {"domain": domain, "name": name, "version": str(self.selected[(domain, name)])}
            for domain, name in self.build_order
        ]


def _topo_order(selected: dict, edges: list) -> list:
    children = {key: set() for key in selected}
    for edge in edges:
        if edge.included:
            children[edge.parent_key].add(edge.child_key)
    order, emitted = [], set()
    remaining = dict(children)
    while remaining:
        ready = sorted(key for key, deps in remaining.items() if deps <= emitted)
        if not ready:
            cycle = ", ".join(f"{d}/{n}" for d, n in sorted(remaining))
            raise CycleError(f"dependency cycle among: {cycle}")
        for key in ready:
            order.append(key)
            emitted.add(key)
            del remaining[key]
    return order


def resolve(index: PackageIndex, ctx: TargetContext, roots) -> Resolution:
    """Compute the minimal concrete build set for one target context.

    Only edges whose condition evaluates true contribute requirements, so
    false-condition dependencies never trigger conflicts or lookups.
    """
    requirements: dict[tuple, list] = {}  # (domain, name) -> [Requirement]
    selected: dict[tuple, Version] = {}
    edges: list = []
    queue: deque = deque()

    def add_requirement(domain, name, constraint, source):
        key = (domain, name)
        requirements.setdefault(key, []).append(Requirement(constraint, source))
        queue.append(key)

    for root in roots:
        add_requirement("target", root.name, root.constraint, "<root>")

    while queue:
        domain, name = queue.popleft()
        key = (domain, name)
        reqs = requirements[key]
        known = index.versions(name)
        if known is None:
            sources = ", ".join(r.source for r in reqs)
            raise UnresolvedError(
                f"{domain}/{name} required by {sources} is not in the local package index"
            )
        matching = [v for v in known if all(r.constraint.matches(v) for r in reqs)]
        if not matching:
            lines = [f"conflict for {domain}/{name}: no known version satisfies all requirements:"]
            lines += [f"  {r.constraint} (required by {r.source})" for r in reqs]
            lines.append("  available: " + ", ".join(str(v) for v in known))
            raise ConflictError("\n".join(lines))
        chosen = max(matching)
        if selected.get(key) == chosen:
            continue
        selected[key] = chosen
        package = index.get(name, chosen)
        for dep in package.deps:
            evaluation = dep.condition.evaluate(ctx)
            child_domain = dep.kind or domain  # host tools beget host deps
            if evaluation.matched:
                edges.append(
                    EdgeEvidence(key, chosen, dep.name, dep.constraint, child_domain,
                                 dep.condition, evaluation, True, "condition true")
                )
                add_requirement(child_domain, dep.name, dep.constraint,
                                source=f"{name}-{chosen}")
            else:
                failed = ", ".join(evaluation.failed_dimensions)
                edges.append(
                    EdgeEvidence(key, chosen, dep.name, dep.constraint, child_domain,
                                 dep.condition, evaluation, False,
                                 f"condition false on: {failed}")
                )

    # Drop edges emitted by superseded versions of a re-selected package.
    final_edges = [e for e in edges if selected.get(e.parent_key) == e.parent_version]
    order = _topo_order(selected, final_edges)
    return Resolution(context=ctx, selected=selected, edges=final_edges, build_order=order)

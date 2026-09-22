"""Problem model: packages, build targets, synthetic propagation rules.

Everything is loaded from local JSON - no network access anywhere.

The propagation rules in an input file are SYNTHETIC modeling rules
supplied by the caller; they are not legal conclusions. A rule says:
when a package whose chosen license carries ``trigger.tag`` is reached
through a ``trigger.link`` edge (and the target conditions match), then
every package in ``scope`` must choose licenses whose tags intersect
``require_tag_in`` - unless the triggering atom carries the exception
named by ``unless_exception`` (WITH semantics: the exception lives on
that one atom only).
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field

from .expr import Atom, parse_terms
from .version import Version, VersionConstraint

LINK_TYPES = ("static", "dynamic")
SCOPES = ("static_dependents", "static_closure", "direct_dependents")


class ModelError(ValueError):
    pass


@dataclass(frozen=True)
class Dependency:
    name: str
    constraint: VersionConstraint
    link: str


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    license_text: str
    terms: tuple[tuple[Atom, ...], ...]
    dependencies: tuple[Dependency, ...]

    @property
    def ref(self) -> str:
        return f"{self.name}@{self.version}"


@dataclass(frozen=True)
class Rule:
    id: str
    trigger_tag: str
    trigger_link: str | None  # None = any incoming link
    scope: str
    require_tag_in: frozenset[str]
    unless_exception: str | None
    target_conditions: tuple[tuple[str, str], ...]
    message: str


@dataclass(frozen=True)
class Rules:
    license_tags: dict[str, frozenset[str]]
    disclosure_tags: frozenset[str]
    propagation: tuple[Rule, ...]

    def tags_of(self, license_id: str) -> frozenset[str]:
        return self.license_tags.get(license_id, frozenset())


@dataclass(frozen=True)
class RootSpec:
    name: str
    constraint: VersionConstraint


@dataclass(frozen=True)
class Target:
    name: str
    conditions: tuple[tuple[str, str], ...]
    link: str  # link type from the artifact to its roots
    roots: tuple[RootSpec, ...]


@dataclass
class Graph:
    nodes: dict[str, Package] = field(default_factory=dict)
    # edges: (parent_ref, child_ref, link); parent_ref None means "the target"
    edges: list[tuple[str | None, str, str]] = field(default_factory=list)
    incoming: dict[str, list[tuple[str | None, str]]] = field(default_factory=dict)
    outgoing: dict[str, list[tuple[str, str]]] = field(default_factory=dict)
    node_targets: dict[str, set[str]] = field(default_factory=dict)

    def add_edge(self, parent: str | None, child: str, link: str) -> None:
        edge = (parent, child, link)
        if edge in self.edges:
            return
        self.edges.append(edge)
        self.incoming.setdefault(child, []).append((parent, link))
        if parent is not None:
            self.outgoing.setdefault(parent, []).append((child, link))


@dataclass
class Problem:
    rules: Rules
    targets: tuple[Target, ...]
    index: dict[str, list[Package]]
    graph: Graph

    @property
    def targets_by_name(self) -> dict[str, Target]:
        return {t.name: t for t in self.targets}


@dataclass(frozen=True)
class ConstraintInstance:
    """One rule applied to one trigger package over a fixed scope."""

    rule_id: str
    trigger: str
    scope_nodes: frozenset[str]
    trigger_tag: str
    require_tag_in: frozenset[str]
    unless_exception: str | None
    message: str

    @property
    def involved(self) -> frozenset[str]:
        return self.scope_nodes | {self.trigger}


# ---------------------------------------------------------------- loading

def _parse_rule(raw: dict, pos: int) -> Rule:
    try:
        trigger = raw["trigger"]
        link = trigger.get("link")
        if link is not None and link not in LINK_TYPES:
            raise ModelError(f"rule[{pos}]: bad trigger link {link!r}")
        scope = raw["scope"]
        if scope not in SCOPES:
            raise ModelError(f"rule[{pos}]: bad scope {scope!r}, want one of {SCOPES}")
        require = raw["require_tag_in"]
        if not require:
            raise ModelError(f"rule[{pos}]: require_tag_in must be non-empty")
        return Rule(
            id=raw.get("id", f"rule-{pos}"),
            trigger_tag=trigger["tag"],
            trigger_link=link,
            scope=scope,
            require_tag_in=frozenset(require),
            unless_exception=raw.get("unless_exception"),
            target_conditions=tuple(sorted(raw.get("target_conditions", {}).items())),
            message=raw.get("message", ""),
        )
    except KeyError as exc:
        raise ModelError(f"rule[{pos}]: missing key {exc}") from exc


def _parse_package(raw: dict, pos: int) -> Package:
    try:
        name = raw["name"]
        version = Version.parse(raw["version"])
        license_text = raw["license"]
        terms = parse_terms(license_text)
        deps = []
        for dep in raw.get("dependencies", []):
            link = dep.get("link", "static")
            if link not in LINK_TYPES:
                raise ModelError(
                    f"package[{pos}] {name}: bad dependency link {link!r}"
                )
            deps.append(
                Dependency(
                    name=dep["name"],
                    constraint=VersionConstraint.parse(dep.get("constraint", "*")),
                    link=link,
                )
            )
        return Package(name, version, license_text, terms, tuple(deps))
    except KeyError as exc:
        raise ModelError(f"package[{pos}]: missing key {exc}") from exc


def load_problem(source) -> Problem:
    """Load a problem from a JSON path or an already-parsed dict."""
    if isinstance(source, (str, bytes)):
        with open(source, "r", encoding="utf-8") as fh:
            raw = json.load(fh)
    else:
        raw = source

    raw_rules = raw.get("rules", {})
    rules = Rules(
        license_tags={
            lic: frozenset(tags)
            for lic, tags in raw_rules.get("license_tags", {}).items()
        },
        disclosure_tags=frozenset(raw_rules.get("disclosure_tags", [])),
        propagation=tuple(
            _parse_rule(r, i) for i, r in enumerate(raw_rules.get("propagation", []))
        ),
    )

    index: dict[str, list[Package]] = {}
    for i, pkg_raw in enumerate(raw.get("packages", [])):
        pkg = _parse_package(pkg_raw, i)
        versions = index.setdefault(pkg.name, [])
        if any(p.version == pkg.version for p in versions):
            raise ModelError(f"duplicate package {pkg.ref}")
        versions.append(pkg)
    for versions in index.values():
        versions.sort(key=lambda p: p.version)

    targets = []
    for t in raw.get("targets", []):
        link = t.get("link", "static")
        if link not in LINK_TYPES:
            raise ModelError(f"target {t.get('name')!r}: bad link {link!r}")
        roots = tuple(
            RootSpec(r["name"], VersionConstraint.parse(r.get("constraint", "*")))
            for r in t.get("roots", [])
        )
        if not roots:
            raise ModelError(f"target {t.get('name')!r}: no roots")
        targets.append(
            Target(
                name=t["name"],
                conditions=tuple(sorted(t.get("conditions", {}).items())),
                link=link,
                roots=roots,
            )
        )
    if not targets:
        raise ModelError("no targets defined")

    problem = Problem(rules=rules, targets=tuple(targets), index=index, graph=Graph())
    _build_graph(problem)
    return problem


def resolve(index: dict[str, list[Package]], name: str, constraint: VersionConstraint) -> Package:
    """Pick the highest version satisfying the constraint (local index only)."""
    candidates = [p for p in index.get(name, []) if constraint.allows(p.version)]
    if not candidates:
        raise ModelError(f"no local version of {name!r} satisfies {constraint}")
    return candidates[-1]


def _build_graph(problem: Problem) -> None:
    graph = problem.graph
    queue: list[tuple[str, str]] = []  # (ref, target_name)
    for target in problem.targets:
        for root in target.roots:
            pkg = resolve(problem.index, root.name, root.constraint)
            graph.nodes[pkg.ref] = pkg
            graph.add_edge(None, pkg.ref, target.link)
            queue.append((pkg.ref, target.name))
    while queue:
        ref, target_name = queue.pop(0)
        graph.node_targets.setdefault(ref, set()).add(target_name)
        pkg = graph.nodes[ref]
        for dep in pkg.dependencies:
            child = resolve(problem.index, dep.name, dep.constraint)
            is_new = child.ref not in graph.nodes
            graph.nodes[child.ref] = child
            graph.add_edge(ref, child.ref, dep.link)
            known_targets = graph.node_targets.get(child.ref, set())
            if is_new or target_name not in known_targets:
                # shared dependency: re-enqueue only to record target membership
                queue.append((child.ref, target_name))


# ------------------------------------------------------- constraint instances

def _static_reachable(graph: Graph, start: str, reverse: bool) -> frozenset[str]:
    seen: set[str] = set()
    stack = [start]
    while stack:
        node = stack.pop()
        if reverse:
            neighbours = [p for p, link in graph.incoming.get(node, []) if link == "static" and p is not None]
        else:
            neighbours = [c for c, link in graph.outgoing.get(node, []) if link == "static"]
        for nxt in neighbours:
            if nxt not in seen and nxt != start:
                seen.add(nxt)
                stack.append(nxt)
    return frozenset(seen)


def compute_instances(problem: Problem) -> list[ConstraintInstance]:
    graph = problem.graph
    instances: list[ConstraintInstance] = []
    for rule in problem.rules.propagation:
        for ref in sorted(graph.nodes):
            incoming = graph.incoming.get(ref, [])
            if rule.trigger_link is not None and not any(
                link == rule.trigger_link for _, link in incoming
            ):
                continue
            if rule.target_conditions and not any(
                all(cond in problem.targets_by_name[t].conditions for cond in rule.target_conditions)
                for t in graph.node_targets.get(ref, ())
                if t in problem.targets_by_name
            ):
                continue
            if rule.scope == "static_dependents":
                scope = _static_reachable(graph, ref, reverse=True)
            elif rule.scope == "static_closure":
                scope = _static_reachable(graph, ref, reverse=False)
            else:  # direct_dependents: parents over edges of the trigger's link type
                scope = frozenset(
                    p for p, link in graph.incoming.get(ref, [])
                    if p is not None
                    and (rule.trigger_link is None or link == rule.trigger_link)
                )
            if not scope:
                continue
            instances.append(
                ConstraintInstance(
                    rule_id=rule.id,
                    trigger=ref,
                    scope_nodes=scope,
                    trigger_tag=rule.trigger_tag,
                    require_tag_in=rule.require_tag_in,
                    unless_exception=rule.unless_exception,
                    message=rule.message,
                )
            )
    return instances

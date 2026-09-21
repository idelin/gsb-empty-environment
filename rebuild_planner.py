"""Minimum rebuild cost planner for cached build artifacts.

All package and build metadata is read from a local JSON file.  The planner
never accesses the network.  A node in the dependency DAG can be obtained in
three ways:

* ``local``   -- a valid node already present in the empty/working repository;
* ``restore`` -- restored from a cache entry whose digest matches exactly;
* ``build``   -- rebuilt from its declared dependencies at ``cost``.

A failure artifact listed in ``failed`` is unavailable and must be restored
or rebuilt.  Shared transitive dependencies are traversed once, so their cost
is counted exactly once.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


class PlanError(ValueError):
    """Raised for invalid inputs or plans that cannot satisfy the targets."""


_VERSION_RE = re.compile(
    r"^(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)"
    r"(?:-(?P<pre>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+(?P<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)
_PRE_ID_RE = re.compile(r"^(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)$")
_CONSTRAINT_RE = re.compile(r"^\s*(\^|~|>=|<=|>|<|=)?\s*(.+?)\s*$")


def _pre_key(identifier: str) -> tuple[int, Any]:
    """Sort key for one dot-separated pre-release identifier (SemVer §11)."""
    if identifier.isdigit():
        if len(identifier) > 1 and identifier[0] == "0":
            raise PlanError(f"invalid pre-release identifier: {identifier!r}")
        return (0, int(identifier))
    return (1, identifier)


@dataclass(frozen=True)
class Version:
    """A semantic version major.minor.patch with an optional pre-release."""

    major: int
    minor: int
    patch: int
    prerelease: tuple[str, ...] = ()
    build_metadata: str = ""

    @classmethod
    def parse(cls, text: str) -> "Version":
        if not isinstance(text, str):
            raise PlanError(f"version must be a string, got {type(text).__name__}")
        match = _VERSION_RE.match(text)
        if not match:
            raise PlanError(f"invalid semantic version: {text!r}")
        pre_text = match.group("pre")
        prerelease = tuple(pre_text.split(".")) if pre_text else ()
        for identifier in prerelease:
            if not _PRE_ID_RE.match(identifier):
                raise PlanError(f"invalid pre-release identifier: {identifier!r}")
        return cls(
            int(match.group("major")),
            int(match.group("minor")),
            int(match.group("patch")),
            prerelease,
            match.group("build") or "",
        )

    @property
    def core(self) -> tuple[int, int, int]:
        return (self.major, self.minor, self.patch)

    @property
    def is_prerelease(self) -> bool:
        return bool(self.prerelease)

    def _sort_key(self) -> tuple[Any, ...]:
        # SemVer 11: a release outranks any pre-release of the same core, and
        # pre-release identifiers are compared numerically/lexicographically.
        release_rank = 1 if not self.prerelease else 0
        pre_key = tuple(_pre_key(part) for part in self.prerelease)
        return (*self.core, release_rank, pre_key)

    def __lt__(self, other: "Version") -> bool:
        return self._sort_key() < other._sort_key()

    def __le__(self, other: "Version") -> bool:
        return self._sort_key() <= other._sort_key()

    def __gt__(self, other: "Version") -> bool:
        return self._sort_key() > other._sort_key()

    def __ge__(self, other: "Version") -> bool:
        return self._sort_key() >= other._sort_key()

    def __str__(self) -> str:
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            text += "-" + ".".join(self.prerelease)
        if self.build_metadata:
            text += "+" + self.build_metadata
        return text


@dataclass(frozen=True)
class Constraint:
    """A single constraint such as ``^1.2.0``, ``>=2.0.0`` or ``=1.0.0-rc.1``."""

    operator: str
    version: Version
    include_prerelease: bool = False

    @classmethod
    def parse(cls, text: str) -> "Constraint":
        if not isinstance(text, str) or not text.strip():
            raise PlanError(f"invalid version constraint: {text!r}")
        match = _CONSTRAINT_RE.match(text)
        assert match is not None
        operator = match.group(1) or "="
        version = Version.parse(match.group(2))
        return cls(operator, version, version.is_prerelease)

    def matches(self, candidate: Version) -> bool:
        if candidate.is_prerelease and not self.include_prerelease:
            return False
        operator = self.operator
        base = self.version
        if operator == "=":
            return (
                candidate.core == base.core
                and candidate.prerelease == base.prerelease
            )
        if operator == ">":
            return candidate > base
        if operator == ">=":
            return candidate >= base
        if operator == "<":
            return candidate < base
        if operator == "<=":
            return candidate <= base
        if operator == "^":
            if base.major > 0:
                upper = (base.major + 1, 0, 0)
            elif base.minor > 0:
                upper = (0, base.minor + 1, 0)
            else:
                upper = (0, 0, base.patch + 1)
            return candidate >= base and candidate.core < upper
        if operator == "~":
            upper = (
                (base.major, base.minor + 1, 0)
                if base.major > 0 or base.minor > 0
                else (0, 0, base.patch + 1)
            )
            return candidate >= base and candidate.core < upper
        raise PlanError(f"unsupported constraint operator: {operator}")

    def __str__(self) -> str:
        return f"{self.operator}{self.version}"


@dataclass(frozen=True)
class Node:
    """One versioned build node and its direct dependency edge list."""

    node_id: str
    package: str
    version: Version
    cost: int
    digest: str
    dependencies: tuple[str, ...] = ()
    available: bool = True


@dataclass(frozen=True)
class Target:
    expression: str
    package: str
    constraint: Constraint
    allow_prerelease: bool
    node_id: str = ""

    def matches(self, node: Node) -> bool:
        if self.node_id:
            return node.node_id == self.node_id
        if node.package != self.package:
            return False
        constraint = self.constraint
        if node.version.is_prerelease and not self.allow_prerelease:
            return False
        return constraint.matches(node.version)


@dataclass(frozen=True)
class BuildInput:
    nodes: dict[str, Node]
    cache: dict[str, str]
    failed: frozenset[str]
    targets: tuple[Target, ...]


def _require_object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise PlanError(f"{label} must be a JSON object")
    return value


def _parse_target(value: Any, nodes: dict[str, Node]) -> Target:
    if isinstance(value, str):
        if value in nodes:
            node = nodes[value]
            return Target(
                value,
                node.package,
                Constraint("=", node.version),
                node.version.is_prerelease,
                node_id=value,
            )
        if "@" not in value:
            raise PlanError(f"unknown target node: {value!r}")
        package, spec = value.rsplit("@", 1)
        if not package:
            raise PlanError(f"invalid package target: {value!r}")
        constraint = Constraint.parse(spec)
        allow_pre = constraint.include_prerelease
        return Target(value, package, constraint, allow_pre)

    target = _require_object(value, "target")
    expression = target.get("name") or target.get("package")
    if not expression:
        raise PlanError("target requires 'package'")
    package = str(expression)
    allow_pre = bool(target.get("allow_prerelease", False))
    constraint_text = target.get("constraint", "*")
    if constraint_text == "*":
        constraint = Constraint(">=", Version.parse("0.0.0"), allow_pre)
        return Target(f"{package}@*", package, constraint, allow_pre)
    constraint = Constraint.parse(str(constraint_text))
    allow_pre = allow_pre or constraint.include_prerelease
    return Target(
        f"{package}@{constraint_text}", package, constraint, allow_pre
    )


def load_input(data: Any) -> BuildInput:
    """Validate and decode the local JSON document."""
    root = _require_object(data, "input root")
    raw_nodes = root.get("nodes", [])
    if not isinstance(raw_nodes, list):
        raise PlanError("'nodes' must be a list")

    nodes: dict[str, Node] = {}
    seen_pairs: set[tuple[str, Version]] = set()
    for index, raw_node in enumerate(raw_nodes):
        item = _require_object(raw_node, f"nodes[{index}]")
        node_id = item.get("id")
        package = item.get("package")
        if not isinstance(node_id, str) or not node_id:
            raise PlanError(f"nodes[{index}] requires non-empty string 'id'")
        if not isinstance(package, str) or not package:
            raise PlanError(f"nodes[{index}] requires non-empty string 'package'")
        if node_id in nodes:
            raise PlanError(f"duplicate node id: {node_id!r}")
        version = Version.parse(item.get("version", ""))
        pair = (package, version)
        if pair in seen_pairs:
            raise PlanError(
                f"duplicate package/version pair: {package}@{version}"
            )
        seen_pairs.add(pair)
        cost = item.get("cost", 0)
        if not isinstance(cost, int) or isinstance(cost, bool) or cost < 0:
            raise PlanError(f"node {node_id!r}: cost must be a non-negative integer")
        digest = item.get("digest")
        if not isinstance(digest, str) or not digest:
            raise PlanError(f"node {node_id!r}: digest must be a non-empty string")
        dependencies = tuple(item.get("dependencies", []))
        if not all(isinstance(dep, str) and dep for dep in dependencies):
            raise PlanError(f"node {node_id!r}: dependencies must be string ids")
        if len(set(dependencies)) != len(dependencies):
            raise PlanError(f"node {node_id!r}: duplicate dependency edge")
        available = bool(item.get("available", True))
        nodes[node_id] = Node(
            node_id, package, version, cost, digest, dependencies, available
        )

    for node in nodes.values():
        for dependency in node.dependencies:
            if dependency not in nodes:
                raise PlanError(
                    f"node {node.node_id!r} depends on unknown node {dependency!r}"
                )
            if dependency == node.node_id:
                raise PlanError(f"node {node.node_id!r} depends on itself")

    raw_cache = root.get("cache", {})
    if not isinstance(raw_cache, dict):
        raise PlanError("'cache' must be an object of node id -> digest")
    cache: dict[str, str] = {}
    for node_id, cached_digest in raw_cache.items():
        if not isinstance(cached_digest, str) or not cached_digest:
            raise PlanError(f"cache entry for {node_id!r} must be a digest string")
        if node_id not in nodes:
            raise PlanError(f"cache entry references unknown node {node_id!r}")
        cache[node_id] = cached_digest

    failed_values = root.get("failed", [])
    if not isinstance(failed_values, list):
        raise PlanError("'failed' must be a list of node ids")
    failed: set[str] = set()
    for node_id in failed_values:
        if not isinstance(node_id, str) or node_id not in nodes:
            raise PlanError(f"failed entry references unknown node {node_id!r}")
        failed.add(node_id)

    target_values = root.get("targets", [])
    if not isinstance(target_values, list) or not target_values:
        raise PlanError("'targets' must be a non-empty list")
    targets = tuple(_parse_target(value, nodes) for value in target_values)

    return BuildInput(nodes, cache, frozenset(failed), targets)


def load_input_file(path: str | Path) -> BuildInput:
    with Path(path).open("r", encoding="utf-8") as stream:
        return load_input(json.load(stream))


@dataclass(frozen=True)
class Step:
    node_id: str
    package: str
    version: str
    action: str  # "local" | "restore" | "build"
    cost: int
    digest: str
    dependencies: tuple[str, ...]
    reason: str


@dataclass(frozen=True)
class Plan:
    steps: tuple[Step, ...]
    targets: tuple[str, ...]
    total_cost: int
    restored: tuple[str, ...]
    built: tuple[str, ...]
    local: tuple[str, ...]

    def to_dict(self) -> dict[str, Any]:
        return {
            "targets": list(self.targets),
            "total_cost": self.total_cost,
            "cost_currency": "build-cost units (shared node counted once)",
            "restored": list(self.restored),
            "built": list(self.built),
            "local": list(self.local),
            "steps": [
                {
                    "order": index + 1,
                    "node_id": step.node_id,
                    "package": step.package,
                    "version": step.version,
                    "action": step.action,
                    "cost": step.cost,
                    "digest": step.digest,
                    "dependencies": list(step.dependencies),
                    "reason": step.reason,
                }
                for index, step in enumerate(self.steps)
            ],
        }


def _resolve_targets(build_input: BuildInput) -> list[Node]:
    resolved: list[Node] = []
    used_ids: set[str] = set()
    for target in build_input.targets:
        if target.node_id:
            node = build_input.nodes[target.node_id]
        else:
            candidates = [
                node for node in build_input.nodes.values() if target.matches(node)
            ]
            if not candidates:
                raise PlanError(f"no node satisfies target {target.expression!r}")
            node = max(candidates, key=lambda item: item.version)
        if node.node_id not in used_ids:
            resolved.append(node)
            used_ids.add(node.node_id)
    return resolved


def _closure(
    roots: list[Node], nodes: dict[str, Node]
) -> tuple[set[str], dict[str, int]]:
    needed: set[str] = set()
    depth: dict[str, int] = {}

    def visit(node_id: str, stack: list[str]) -> None:
        if node_id in depth:
            return
        if node_id in stack:
            cycle = stack[stack.index(node_id):] + [node_id]
            raise PlanError("dependency cycle: " + " -> ".join(cycle))
        stack.append(node_id)
        for dependency in nodes[node_id].dependencies:
            visit(dependency, stack)
        stack.pop()
        needed.add(node_id)
        depth[node_id] = len(depth)

    for root in roots:
        visit(root.node_id, [])
    return needed, depth


def _action_for(
    node: Node, build_input: BuildInput
) -> tuple[str, int, str]:
    if node.node_id not in build_input.failed and node.available:
        return "local", 0, "valid artifact already present"
    cached_digest = build_input.cache.get(node.node_id)
    if cached_digest is None:
        return "build", node.cost, "failed/missing and no cache entry"
    if cached_digest != node.digest:
        return (
            "build",
            node.cost,
            f"cache digest mismatch (cache={cached_digest[:12]}...)",
        )
    return "restore", 0, "cache digest matches"


def _topological_order(
    needed: set[str], nodes: dict[str, Node]
) -> list[str]:
    indegree = {node_id: 0 for node_id in needed}
    dependents: dict[str, list[str]] = {node_id: [] for node_id in needed}
    for node_id in needed:
        for dependency in nodes[node_id].dependencies:
            indegree[node_id] += 1
            dependents[dependency].append(node_id)
    ready = sorted(node_id for node_id, degree in indegree.items() if degree == 0)
    ordered: list[str] = []
    while ready:
        node_id = ready.pop(0)
        ordered.append(node_id)
        for dependent in sorted(dependents[node_id]):
            indegree[dependent] -= 1
            if indegree[dependent] == 0:
                ready.append(dependent)
        ready.sort()
    if len(ordered) != len(needed):
        remaining = sorted(set(needed) - set(ordered))
        raise PlanError("dependency cycle among: " + ", ".join(remaining))
    return ordered


def make_plan(build_input: BuildInput) -> Plan:
    roots = _resolve_targets(build_input)
    needed, _ = _closure(roots, build_input.nodes)
    ordered_ids = _topological_order(needed, build_input.nodes)

    steps: list[Step] = []
    total_cost = 0
    groups: dict[str, list[str]] = {
        "local": [],
        "restore": [],
        "build": [],
    }
    for node_id in ordered_ids:
        node = build_input.nodes[node_id]
        action, cost, reason = _action_for(node, build_input)
        total_cost += cost
        groups[action].append(node_id)
        steps.append(
            Step(
                node_id,
                node.package,
                str(node.version),
                action,
                cost,
                node.digest,
                node.dependencies,
                reason,
            )
        )

    return Plan(
        tuple(steps),
        tuple(node.node_id for node in roots),
        total_cost,
        tuple(groups["restore"]),
        tuple(groups["build"]),
        tuple(groups["local"]),
    )


@dataclass(frozen=True)
class Verification:
    node_id: str
    action: str
    satisfied_dependencies: tuple[str, ...]
    digest_check: str
    ok: bool
    detail: str


def verify_plan(plan: Plan, build_input: BuildInput) -> list[Verification]:
    """Independently verify the plan node by node.

    Raises :class:`PlanError` on any invalid ordering, dependency, digest,
    target coverage, or cost-optimality condition.
    """
    nodes = build_input.nodes
    completed: set[str] = set()
    seen_steps: set[str] = set()
    verifications: list[Verification] = []
    recomputed_cost = 0

    for step in plan.steps:
        if step.node_id not in nodes:
            raise PlanError(f"plan contains unknown node {step.node_id!r}")
        if step.node_id in seen_steps:
            raise PlanError(f"plan repeats node {step.node_id!r}")
        seen_steps.add(step.node_id)
        node = nodes[step.node_id]

        missing = [
            dependency
            for dependency in node.dependencies
            if dependency not in completed
        ]
        if missing:
            raise PlanError(
                f"node {step.node_id!r} executed before dependencies: {missing}"
            )

        expected_action, expected_cost, reason = _action_for(node, build_input)
        if step.action != expected_action:
            raise PlanError(
                f"node {step.node_id!r}: plan uses {step.action!r} but minimum "
                f"action is {expected_action!r} ({reason})"
            )
        if step.cost != expected_cost:
            raise PlanError(
                f"node {step.node_id!r}: cost {step.cost} != {expected_cost}"
            )
        recomputed_cost += expected_cost

        digest_check = "not required"
        if step.action == "restore":
            cached_digest = build_input.cache[node.node_id]
            if cached_digest != node.digest:
                raise PlanError(
                    f"node {step.node_id!r}: cannot restore, digest mismatch"
                )
            digest_check = f"cache {cached_digest} == node {node.digest}"
        elif step.action == "build":
            if node.node_id not in build_input.failed and node.available:
                raise PlanError(
                    f"node {step.node_id!r}: needless rebuild of valid artifact"
                )
            cached_digest = build_input.cache.get(node.node_id)
            if cached_digest == node.digest:
                raise PlanError(
                    f"node {step.node_id!r}: matching cache makes a rebuild wasteful"
                )
            digest_check = "no matching cache; rebuild required"
        else:
            digest_check = "existing digest accepted as valid"

        completed.add(step.node_id)
        verifications.append(
            Verification(
                step.node_id,
                step.action,
                node.dependencies,
                digest_check,
                True,
                reason,
            )
        )

    if recomputed_cost != plan.total_cost:
        raise PlanError(
            f"total cost {plan.total_cost} != independently summed {recomputed_cost}"
        )

    roots = _resolve_targets(build_input)
    missing_targets = [node.node_id for node in roots if node.node_id not in completed]
    if missing_targets:
        raise PlanError(f"plan does not satisfy targets: {missing_targets}")

    expected_needed, _ = _closure(roots, nodes)
    if seen_steps != expected_needed:
        extra = sorted(seen_steps - expected_needed)
        absent = sorted(expected_needed - seen_steps)
        raise PlanError(f"closure mismatch: extra={extra}, absent={absent}")

    return verifications


def build_report(build_input: BuildInput) -> dict[str, Any]:
    plan = make_plan(build_input)
    verifications = verify_plan(plan, build_input)
    report = plan.to_dict()
    report["verification"] = [
        {
            "node_id": item.node_id,
            "action": item.action,
            "dependencies_available": list(item.satisfied_dependencies),
            "digest_check": item.digest_check,
            "ok": item.ok,
            "detail": item.detail,
        }
        for item in verifications
    ]
    return report


def _print_human(report: dict[str, Any]) -> None:
    print("Targets:     " + ", ".join(report["targets"]))
    print(f"Total cost:  {report['total_cost']}")
    print("Restored:    " + (", ".join(report["restored"]) or "-"))
    print("Rebuilt:     " + (", ".join(report["built"]) or "-"))
    print("Local valid: " + (", ".join(report["local"]) or "-"))
    print("\nExecution order (each dependency is verified first):")
    for step in report["steps"]:
        deps = ", ".join(step["dependencies"]) or "-"
        print(
            f"  {step['order']:>2}. {step['action']:<7} {step['node_id']:<10} "
            f"cost={step['cost']:<4} deps=[{deps}]  # {step['reason']}"
        )
    print("\nVerification:")
    for item in report["verification"]:
        print(f"  {'OK' if item['ok'] else 'FAIL'}  {item['node_id']}: {item['digest_check']}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Compute the minimum shared-cost rebuild plan from local JSON."
    )
    parser.add_argument("input", help="path to the local build input JSON")
    parser.add_argument(
        "--json", action="store_true", help="emit the complete plan/verification as JSON"
    )
    args = parser.parse_args(argv)
    try:
        build_input = load_input_file(args.input)
        report = build_report(build_input)
    except (OSError, json.JSONDecodeError, PlanError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 2
    if args.json:
        json.dump(report, sys.stdout, indent=2, ensure_ascii=False)
        sys.stdout.write("\n")
    else:
        _print_human(report)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

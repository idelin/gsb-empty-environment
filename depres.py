#!/usr/bin/env python3
"""depres: lock-file-aware minimal-change dependency resolver.

Inputs are local JSON files only (package index, build requirements and an
existing lock file); the resolver never accesses the network.

Resolution objectives, in strict order:
  1. minimize the number of packages whose pinned version differs from the
     existing lock file (changed / added / removed packages);
  2. among solutions with the same minimal change count, prefer newer
     versions.

Statuses reported to the caller:
  ok    - an optimal solution was found within the search budget;
  unsat - the complete finite search space was exhausted with no solution;
  limit - the state budget ran out, so unsatisfiability cannot be proven.
"""

from __future__ import annotations

import argparse
import functools
import json
import re
import sys
from collections import defaultdict
from dataclasses import dataclass


class InputError(Exception):
    """Invalid input (bad JSON, malformed versions, missing locked version)."""


class SearchLimitExceeded(Exception):
    """The backtracking search explored more states than the configured limit."""


_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$"
)


@functools.total_ordering
@dataclass(frozen=True)
class Version:
    """major.minor.patch with an optional pre-release tag (semver ordering)."""

    major: int
    minor: int
    patch: int
    pre: tuple = ()

    @classmethod
    def parse(cls, text):
        if not isinstance(text, str):
            raise InputError(f"version must be a string, got {text!r}")
        match = _VERSION_RE.match(text.strip())
        if not match:
            raise InputError(
                f"invalid version {text!r}: expected major.minor.patch[-prerelease]"
            )
        pre = ()
        if match.group(4) is not None:
            pre = tuple(
                int(part) if part.isdigit() else part
                for part in match.group(4).split(".")
            )
        return cls(int(match.group(1)), int(match.group(2)), int(match.group(3)), pre)

    def __str__(self):
        text = f"{self.major}.{self.minor}.{self.patch}"
        if self.pre:
            text += "-" + ".".join(str(part) for part in self.pre)
        return text

    def __lt__(self, other):
        if not isinstance(other, Version):
            return NotImplemented
        core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if core != other_core:
            return core < other_core
        return _pre_less(self.pre, other.pre)


def _pre_less(a, b):
    """Pre-release ordering per semver; a release (empty tuple) is greatest."""
    if a == b:
        return False
    if not a:
        return False
    if not b:
        return True
    for left, right in zip(a, b):
        if left == right:
            continue
        left_numeric = isinstance(left, int)
        right_numeric = isinstance(right, int)
        if left_numeric and right_numeric:
            return left < right
        if left_numeric:
            return True
        if right_numeric:
            return False
        return left < right
    return len(a) < len(b)


_COMPARATOR_RE = re.compile(r"^(>=|<=|==|!=|>|<)?\s*(\S+)$")


class Constraint:
    """Comma-separated comparators, e.g. ">=1.0.0,<2.0.0" or "==1.2.3-rc.1"."""

    _OPS = {
        ">=": lambda value, bound: value >= bound,
        "<=": lambda value, bound: value <= bound,
        ">": lambda value, bound: value > bound,
        "<": lambda value, bound: value < bound,
        "==": lambda value, bound: value == bound,
        "!=": lambda value, bound: value != bound,
    }

    def __init__(self, raw):
        if not isinstance(raw, str):
            raise InputError(f"constraint must be a string, got {raw!r}")
        self.raw = raw
        self.comparators = []
        text = raw.strip()
        if text in ("", "*"):
            return
        for part in text.split(","):
            part = part.strip()
            match = _COMPARATOR_RE.match(part)
            if not match:
                raise InputError(f"invalid constraint comparator {part!r}")
            operator = match.group(1) or "=="
            self.comparators.append((operator, Version.parse(match.group(2))))

    def matches(self, version):
        return all(
            self._OPS[operator](version, bound)
            for operator, bound in self.comparators
        )


@dataclass(frozen=True)
class PackageVersion:
    name: str
    version: Version
    deps: tuple  # sorted tuple of (dependency name, Constraint)


class Index:
    """Local package index: {package: {version: {"deps": {dep: constraint}}}}."""

    def __init__(self, data):
        if not isinstance(data, dict):
            raise InputError("package index must be a JSON object")
        self.packages = {}
        for name, versions in data.items():
            if not isinstance(versions, dict):
                raise InputError(f"index entry for {name!r} must be an object")
            parsed = {}
            for version_text, metadata in versions.items():
                version = Version.parse(version_text)
                metadata = metadata or {}
                raw_deps = metadata.get("deps", {})
                if not isinstance(raw_deps, dict):
                    raise InputError(f"deps of {name}@{version_text} must be an object")
                deps = tuple(sorted((dep, Constraint(spec)) for dep, spec in raw_deps.items()))
                parsed[version] = PackageVersion(name, version, deps)
            self.packages[name] = parsed

    def __contains__(self, name):
        return name in self.packages

    def versions(self, name):
        return sorted(self.packages.get(name, ()))

    def get(self, name, version):
        return self.packages[name][version]


@dataclass(frozen=True)
class Change:
    name: str
    old: object          # Version pinned in the old lock, or None (newly added)
    new: object          # Version in the solution, or None (pruned)
    reasons: tuple       # tuple of (requirer, raw constraint)


@dataclass
class Result:
    status: str          # "ok" | "unsat" | "limit"
    solution: object     # dict name -> Version, or None
    changes: tuple
    states: int
    max_states: int
    cost: int = None     # number of assignments differing from the old lock


class Resolver:
    """Branch-and-bound solver with an explicit (changes, versions) objective."""

    def __init__(self, index, requirements, lock=None, max_states=100_000):
        self.index = index
        self.requirements = {
            name: Constraint(spec) for name, spec in requirements.items()
        }
        if not isinstance(lock or {}, dict):
            raise InputError("lock file must be a JSON object mapping name to version")
        self.lock = {}
        for name, version_text in (lock or {}).items():
            version = Version.parse(version_text)
            if name not in index or version not in index.packages[name]:
                raise InputError(
                    f"lock file pins {name}@{version_text}, "
                    "but that version does not exist in the package index"
                )
            self.lock[name] = version
        self.max_states = max_states
        self.states = 0
        self.best = None
        self.best_key = None
        self._imposed = defaultdict(list)  # name -> [(requirer, Constraint)]
        self._assignment = {}              # name -> Version
        self._cost = 0

    def solve(self):
        for name, constraint in self.requirements.items():
            self._imposed[name].append(("root", constraint))
        try:
            self._search()
        except SearchLimitExceeded:
            return Result("limit", None, (), self.states, self.max_states)
        if self.best is None:
            return Result("unsat", None, (), self.states, self.max_states)
        changes = self._explain(self.best)
        return Result("ok", dict(self.best), tuple(changes), self.states,
                      self.max_states, self.best_key[0])

    def _search(self):
        self.states += 1
        if self.states > self.max_states:
            raise SearchLimitExceeded
        name = self._pick()
        if name is None:
            key = (
                self._cost,
                tuple(version for _, version in sorted(self._assignment.items())),
            )
            if self.best_key is None or self._better(key, self.best_key):
                self.best_key = key
                self.best = dict(self._assignment)
            return
        for version in self._candidates(name):
            delta = 0 if self.lock.get(name) == version else 1
            if self.best_key is not None and self._cost + delta > self.best_key[0]:
                continue
            dependencies = self.index.get(name, version).deps
            if any(
                dep_name in self._assignment
                and not constraint.matches(self._assignment[dep_name])
                for dep_name, constraint in dependencies
            ):
                continue
            self._assignment[name] = version
            self._cost += delta
            requirer = f"{name}@{version}"
            for dep_name, constraint in dependencies:
                self._imposed[dep_name].append((requirer, constraint))
            self._search()
            for dep_name, _ in dependencies:
                self._imposed[dep_name].pop()
            self._cost -= delta
            del self._assignment[name]

    @staticmethod
    def _better(candidate, current):
        """Fewer changes wins; on a tie, the lexicographically newer vector wins."""
        if candidate[0] != current[0]:
            return candidate[0] < current[0]
        return candidate[1] > current[1]

    def _pick(self):
        """Unassigned package with the fewest feasible versions (fail fast)."""
        chosen = None
        chosen_count = None
        for name in self._imposed:
            if name in self._assignment:
                continue
            count = len(self._candidates(name))
            if chosen is None or count < chosen_count:
                chosen = name
                chosen_count = count
                if count == 0:
                    break
        return chosen

    def _candidates(self, name):
        if name not in self.index:
            return []
        feasible = [
            version
            for version in self.index.versions(name)
            if all(constraint.matches(version) for _, constraint in self._imposed[name])
        ]
        feasible.sort(reverse=True)
        locked = self.lock.get(name)
        if locked in feasible:
            feasible.remove(locked)
            feasible.insert(0, locked)
        return feasible

    def _explain(self, solution):
        """Rebuild the final constraint graph and derive one Change per diff."""
        imposed = defaultdict(list)
        for name, constraint in self.requirements.items():
            imposed[name].append(("root", constraint))
        for name, version in solution.items():
            for dep_name, constraint in self.index.get(name, version).deps:
                imposed[dep_name].append((f"{name}@{version}", constraint))
        changes = []
        for name in sorted(set(solution) | set(self.lock)):
            old = self.lock.get(name)
            new = solution.get(name)
            if old == new:
                continue
            reasons = []
            if new is not None:
                for requirer, constraint in imposed.get(name, ()):
                    if old is not None and constraint.matches(old):
                        continue
                    reasons.append((requirer, _requirement_text(name, constraint)))
            changes.append(Change(name, old, new, tuple(reasons)))
        return changes


def _requirement_text(name, constraint):
    raw = constraint.raw.strip()
    if raw in ("", "*"):
        return name
    if raw[:1].isdigit():
        return f"{name}=={raw}"
    return f"{name}{raw}"


RETENTION_POLICY = (
    "retention policy: after a root requirement is removed, every package no "
    "longer reachable from the remaining root requirements is pruned"
)


def render_report(result):
    lines = [
        f"status: {result.status}",
        f"states explored: {result.states} (limit {result.max_states})",
    ]
    if result.status != "ok":
        return "\n".join(lines)
    changed = [change for change in result.changes if change.new is not None]
    removed = [change for change in result.changes if change.new is None]
    lines.append(f"changed packages: {len(changed) + len(removed)} "
                 f"({len(changed)} changed/added, {len(removed)} pruned)")
    if changed:
        lines.append("")
        lines.append("causal chains (root requirement -> affected package):")
        by_name = {change.name: change for change in changed}
        for change in changed:
            _render_chain(lines, change, by_name, 1, set())
    if removed:
        lines.append("")
        lines.append("pruned packages (unreachable from root requirements):")
        for change in removed:
            lines.append(f"  {change.name}: removed (was {change.old})")
            lines.append("    because no remaining package requires it")
            lines.append(f"    ({RETENTION_POLICY})")
    unchanged = [
        f"{name}@{version}"
        for name, version in sorted(result.solution.items())
        if name not in {change.name for change in result.changes}
    ]
    if unchanged:
        lines.append("")
        lines.append("unchanged: " + ", ".join(unchanged))
    lines.append("")
    lines.append("new lock:")
    lock = {name: str(version) for name, version in sorted(result.solution.items())}
    lines.append(json.dumps(lock, indent=2, ensure_ascii=False))
    return "\n".join(lines)


def _render_chain(lines, change, by_name, depth, seen):
    indent = "  " * depth
    if change.old is None:
        lines.append(f"{indent}{change.name}: added at {change.new}")
    else:
        lines.append(f"{indent}{change.name}: {change.old} -> {change.new}")
    for requirer, requirement in change.reasons:
        note = "" if change.old is None else f"; locked {change.old} does not satisfy"
        lines.append(f"{indent}  because {requirer} requires \"{requirement}\"{note}")
        if requirer == "root":
            continue
        parent_name = requirer.rsplit("@", 1)[0]
        parent = by_name.get(parent_name)
        if parent is not None and parent_name not in seen:
            _render_chain(lines, parent, by_name, depth + 1, seen | {parent_name})


def _load_json(path):
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise InputError(f"cannot read {path}: {exc}") from exc


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="depres", description="Minimal-change dependency resolver over local JSON."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    resolve = subparsers.add_parser("resolve", help="resolve requirements")
    resolve.add_argument("--packages", required=True, help="package index JSON")
    resolve.add_argument("--build", required=True, help="build requirements JSON")
    resolve.add_argument("--lock", help="existing lock JSON (optional)")
    resolve.add_argument("--out", help="write the new lock here (default: stdout)")
    resolve.add_argument(
        "--max-states", type=int, default=100_000,
        help="search-state budget; exceeding it yields status 'limit'",
    )
    args = parser.parse_args(argv)

    try:
        index = Index(_load_json(args.packages))
        build = _load_json(args.build)
        requirements = build.get("deps", {})
        if not isinstance(requirements, dict):
            raise InputError("build file must contain a \"deps\" object")
        lock = _load_json(args.lock) if args.lock else {}
        resolver = Resolver(index, requirements, lock, max_states=args.max_states)
    except InputError as exc:
        print(f"input error: {exc}", file=sys.stderr)
        return 3

    result = resolver.solve()
    print(render_report(result))
    if result.status == "ok":
        lock_text = json.dumps(
            {name: str(version) for name, version in sorted(result.solution.items())},
            indent=2, ensure_ascii=False,
        ) + "\n"
        if args.out:
            with open(args.out, "w", encoding="utf-8") as handle:
                handle.write(lock_text)
        return 0
    if result.status == "unsat":
        print("result: unsatisfiable - search space exhausted with no solution",
              file=sys.stderr)
        return 1
    print(
        f"result: search limit exceeded after {result.states} states; "
        "unsatisfiability cannot be proven",
        file=sys.stderr,
    )
    return 2


if __name__ == "__main__":
    raise SystemExit(main())

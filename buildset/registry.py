"""Local package registry loaded from JSON. Never touches the network."""
from __future__ import annotations

import json
from dataclasses import dataclass

from .conditions import Condition
from .version import Constraint, Version

KIND_LIBRARY = "library"
KIND_TOOL = "tool"
_KINDS = (KIND_LIBRARY, KIND_TOOL)


@dataclass(frozen=True)
class Dependency:
    name: str
    constraint: Constraint
    condition: Condition


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    kind: str
    dependencies: tuple = ()

    @property
    def is_tool(self) -> bool:
        return self.kind == KIND_TOOL

    def __str__(self) -> str:
        return f"{self.name}@{self.version}"


def _parse_package(data: dict) -> Package:
    for key in ("name", "version", "kind"):
        if key not in data:
            raise ValueError(f"package entry missing required key {key!r}: {data!r}")
    kind = data["kind"]
    if kind not in _KINDS:
        raise ValueError(f"unknown package kind {kind!r}: expected one of {_KINDS}")
    deps = []
    for raw in data.get("dependencies", []):
        deps.append(
            Dependency(
                name=raw["name"],
                constraint=Constraint.parse(raw.get("constraint", "")),
                condition=Condition.from_json(raw.get("when")),
            )
        )
    return Package(
        name=data["name"],
        version=Version.parse(data["version"]),
        kind=kind,
        dependencies=tuple(deps),
    )


class Registry:
    """All known package versions, indexed by name, sorted ascending."""

    def __init__(self, packages=()):
        self._by_name: dict = {}
        for pkg in packages:
            self._by_name.setdefault(pkg.name, []).append(pkg)
        for versions in self._by_name.values():
            versions.sort(key=lambda p: p.version)

    @classmethod
    def from_json(cls, data: dict) -> "Registry":
        return cls([_parse_package(entry) for entry in data.get("packages", [])])

    @classmethod
    def load(cls, path) -> "Registry":
        with open(path, encoding="utf-8") as fh:
            return cls.from_json(json.load(fh))

    def versions(self, name: str) -> list:
        return list(self._by_name.get(name, ()))

    def best_match(self, name: str, constraints) -> "Package | None":
        """Highest version satisfying every accumulated constraint, or None."""
        best = None
        for pkg in self._by_name.get(name, ()):
            if all(c.allows(pkg.version) for c in constraints):
                if best is None or pkg.version > best.version:
                    best = pkg
        return best

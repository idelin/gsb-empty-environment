"""Local-only package index and build inputs loaded from JSON.

Package index schema::

    {
      "packages": {
        "name": {
          "1.2.3": {"dependencies": {"dep": "<spec>", ...}},
          "1.2.3-alpha.1": {"dependencies": {...}}
        }
      }
    }

A dependency value is either a specification string, or an object
``{"spec": "...", "when": "key=value,key2=value2"}`` gating it on the build
context, or a conditional object with a ``branches`` list.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path

from .target import ConditionalVersionSet
from .version import Version


@dataclass(frozen=True)
class Dependency:
    name: str
    requirement: ConditionalVersionSet
    when: str | None = None


@dataclass(frozen=True)
class Release:
    package: str
    version: Version
    dependencies: tuple[Dependency, ...]


@dataclass(frozen=True)
class PackageIndex:
    releases: dict[str, tuple[Release, ...]]

    @classmethod
    def load(cls, path: str | Path) -> "PackageIndex":
        with open(path, "r", encoding="utf-8") as handle:
            return cls.from_dict(json.load(handle))

    @classmethod
    def from_dict(cls, data: dict) -> "PackageIndex":
        releases: dict[str, list[Release]] = {}
        for package, versions in data.get("packages", {}).items():
            for version_text, meta in versions.items():
                version = Version.parse(version_text)
                deps = tuple(
                    _parse_dependency(dep_name, dep_value)
                    for dep_name, dep_value in (meta or {}).get("dependencies", {}).items()
                )
                releases.setdefault(package, []).append(
                    Release(package, version, deps)
                )
        ordered = {
            name: tuple(sorted(bucket, key=lambda r: r.version, reverse=True))
            for name, bucket in releases.items()
        }
        return cls(ordered)

    def package_names(self) -> frozenset[str]:
        return frozenset(self.releases)

    def versions(self, package: str) -> tuple[Version, ...]:
        return tuple(r.version for r in self.releases.get(package, ()))

    def release(self, package: str, version: Version) -> Release:
        for candidate in self.releases.get(package, ()):
            if candidate.version == version:
                return candidate
        raise KeyError(f"{package}@{version}")


def _parse_dependency(name: str, value) -> Dependency:
    if isinstance(value, str):
        return Dependency(name, ConditionalVersionSet.parse(value))
    if isinstance(value, dict) and "branches" in value:
        return Dependency(name, ConditionalVersionSet.parse(value))
    if isinstance(value, dict) and "spec" in value:
        return Dependency(
            name,
            ConditionalVersionSet.parse(value["spec"]),
            value.get("when"),
        )
    raise ValueError(f"unsupported dependency declaration for {name}: {value!r}")


@dataclass(frozen=True)
class BuildInput:
    roots: tuple[Dependency, ...]
    context: dict[str, str] = field(default_factory=dict)
    locked: dict[str, str] = field(default_factory=dict)

    @classmethod
    def load(cls, path: str | Path) -> "BuildInput":
        with open(path, "r", encoding="utf-8") as handle:
            return cls.from_dict(json.load(handle))

    @classmethod
    def from_dict(cls, data: dict) -> "BuildInput":
        roots = tuple(
            _parse_dependency(name, value)
            for name, value in data.get("roots", {}).items()
        )
        locked = {name: Version.parse(text) for name, text in data.get("locked", {}).items()}
        return cls(roots, dict(data.get("context", {})), locked)

"""Package repository loaded from a local JSON file (no network access).

JSON shape::

    {
      "packages": {
        "lib": [
          {"version": "1.2.0",
           "deps": [{"name": "util", "range": "[0.5.0,1.0.0)",
                      "channels": ["stable", "b"],
                      "targets": ["linux"]}]}
        ]
      }
    }

``channels`` defaults to ``["stable"]`` (prereleases are only admitted
when their channel is explicitly listed); ``targets`` defaults to all
targets.  A build input file has the shape::

    {"target": "linux",
     "requirements": {"app": "[1.0.0,2.0.0)"},
     "lock": {"lib": "1.2.0"}}
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field

from .labels import LabelSet
from .sets import VersionSet
from .version import Version


@dataclass(frozen=True)
class Dependency:
    name: str
    spec: VersionSet

    def applies_to(self, target: str | None) -> bool:
        """Whether this dependency edge is active under a build target."""
        for comp in self.spec.components:
            if target is None:
                if comp.targets.is_all():
                    return True
            elif comp.targets.contains(target):
                return True
        return False


@dataclass(frozen=True)
class PackageVersion:
    name: str
    version: Version
    deps: tuple[Dependency, ...] = ()


class Repo:
    def __init__(self, packages: dict[str, list[PackageVersion]]):
        self._packages = packages

    @classmethod
    def from_dict(cls, data: dict) -> "Repo":
        packages: dict[str, list[PackageVersion]] = {}
        for name, entries in data.get("packages", {}).items():
            versions = []
            for entry in entries:
                deps = []
                for dep in entry.get("deps", []):
                    channels = (LabelSet.of(dep["channels"])
                                if "channels" in dep else None)
                    targets = (LabelSet.of(dep["targets"])
                               if "targets" in dep else None)
                    spec = VersionSet.parse(dep["range"], channels=channels,
                                            targets=targets)
                    deps.append(Dependency(dep["name"], spec))
                versions.append(PackageVersion(
                    name, Version.parse(entry["version"]), tuple(deps)))
            versions.sort(key=lambda pv: pv.version)
            packages[name] = versions
        return cls(packages)

    @classmethod
    def from_json_file(cls, path: str) -> "Repo":
        with open(path, "r", encoding="utf-8") as fh:
            return cls.from_dict(json.load(fh))

    def names(self) -> list[str]:
        return sorted(self._packages)

    def versions(self, name: str) -> list[PackageVersion]:
        return self._packages.get(name, [])

    def get(self, name: str, version: Version) -> PackageVersion | None:
        for pv in self._packages.get(name, []):
            if pv.version == version:
                return pv
        return None


@dataclass(frozen=True)
class BuildInput:
    target: str | None
    requirements: dict[str, VersionSet]
    lock: dict[str, Version] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: dict) -> "BuildInput":
        reqs = {name: VersionSet.parse(spec)
                for name, spec in data.get("requirements", {}).items()}
        lock = {name: Version.parse(v)
                for name, v in data.get("lock", {}).items()}
        return cls(data.get("target"), reqs, lock)

    @classmethod
    def from_json_file(cls, path: str) -> "BuildInput":
        with open(path, "r", encoding="utf-8") as fh:
            return cls.from_dict(json.load(fh))

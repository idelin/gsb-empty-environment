"""Local JSON inputs: package registry (build inputs) and lock files.

Everything is read from local files; nothing is fetched from the network.

Registry format::

    {
      "packages": {
        "web": {
          "2.4.0": {"dependencies": {"util": "^1.2.0"}},
          "3.0.0": {"dependencies": {"util": "^2.0.0"}}
        }
      }
    }

Lock file format::

    {
      "roots": {"app": "1.0.0"},
      "build_inputs": {"python": ">=3.11,<4"},
      "packages": {"app": {"version": "1.0.0", "integrity": "sha256:..."}}
    }
"""

from __future__ import annotations

import json
from pathlib import Path

from .version import Version


class RegistryError(KeyError):
    """A package/version required for verification is absent locally."""


class PackageVersion:
    __slots__ = ("name", "version", "dependencies")

    def __init__(self, name: str, version: str, dependencies: dict[str, str]) -> None:
        self.name = name
        self.version = version
        self.dependencies = dict(dependencies)


class Registry:
    def __init__(self, data: dict) -> None:
        self._packages: dict[str, dict[str, PackageVersion]] = {}
        for name, versions in (data.get("packages") or {}).items():
            bucket = {}
            for ver, meta in versions.items():
                bucket[ver] = PackageVersion(name, ver, (meta or {}).get("dependencies", {}))
            self._packages[name] = bucket

    def get(self, name: str, version: str) -> PackageVersion:
        bucket = self._packages.get(name)
        if bucket is None or version not in bucket:
            raise RegistryError(f"{name}@{version} is not present in the local registry")
        return bucket[version]

    def has(self, name: str, version: str) -> bool:
        try:
            self.get(name, version)
        except RegistryError:
            return False
        return True

    def dependencies_of(self, name: str, version: str) -> dict[str, str]:
        try:
            return self.get(name, version).dependencies
        except RegistryError:
            return {}


class Lockfile:
    def __init__(self, data: dict) -> None:
        self.roots: dict[str, str] = dict(data.get("roots") or {})
        self.build_inputs: dict[str, str] = dict(data.get("build_inputs") or {})
        self.packages: dict[str, dict] = {}
        for name, entry in (data.get("packages") or {}).items():
            self.packages[name] = {
                "version": str(entry["version"]),
                "integrity": entry.get("integrity"),
            }

    def to_dict(self) -> dict:
        return {
            "roots": dict(sorted(self.roots.items())),
            "build_inputs": dict(sorted(self.build_inputs.items())),
            "packages": {
                name: {k: v for k, v in (("version", e["version"]),
                                        ("integrity", e["integrity"])) if v is not None}
                for name, e in sorted(self.packages.items())
            },
        }

    def entry_version(self, name: str) -> Version | None:
        entry = self.packages.get(name)
        return Version.parse(entry["version"]) if entry else None


def load_json(path: str | Path) -> dict:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def load_lockfile(path: str | Path) -> Lockfile:
    return Lockfile(load_json(path))


def load_registry(path: str | Path) -> Registry:
    return Registry(load_json(path))

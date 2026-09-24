"""Lock-file model: exact pinned versions plus recorded dependency edges."""

from __future__ import annotations

import json

from .version import Constraint, Version


class LockedPackage:
    __slots__ = ("version", "dependencies")

    def __init__(self, version, dependencies=None):
        self.version = version
        self.dependencies = dict(dependencies or {})

    def __eq__(self, other):
        if not isinstance(other, LockedPackage):
            return NotImplemented
        return self.version == other.version and self.dependencies == other.dependencies

    def to_json(self):
        return {
            "version": str(self.version),
            "dependencies": {
                name: str(spec) for name, spec in sorted(self.dependencies.items())
            },
        }


class LockFile:
    def __init__(self, packages=None):
        self.packages = dict(packages or {})

    @classmethod
    def from_json(cls, data):
        packages = {}
        for name, meta in data.get("packages", {}).items():
            version = Version.parse(meta["version"])
            deps = {
                dep: Constraint(spec)
                for dep, spec in meta.get("dependencies", {}).items()
            }
            packages[name] = LockedPackage(version, deps)
        return cls(packages)

    @classmethod
    def load(cls, path):
        with open(path, "r", encoding="utf-8") as handle:
            return cls.from_json(json.load(handle))

    def to_json(self):
        return {
            "packages": {
                name: self.packages[name].to_json() for name in sorted(self.packages)
            }
        }

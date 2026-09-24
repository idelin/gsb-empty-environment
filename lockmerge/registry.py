"""A local, JSON-backed package registry. No network access is ever performed."""

from __future__ import annotations

import json

from .version import Constraint, Version


class Registry:
    """Available package versions and their dependency constraints, from local JSON."""

    def __init__(self, data):
        self._packages = {}
        for name, versions in data.get("packages", {}).items():
            entries = {}
            for version_text, meta in versions.items():
                version = Version.parse(version_text)
                deps = {
                    dep: Constraint(spec)
                    for dep, spec in (meta or {}).get("dependencies", {}).items()
                }
                entries[version] = deps
            self._packages[name] = entries

    @classmethod
    def load(cls, path):
        with open(path, "r", encoding="utf-8") as handle:
            return cls(json.load(handle))

    def __contains__(self, name):
        return name in self._packages

    def has_version(self, name, version):
        return version in self._packages.get(name, {})

    def versions(self, name):
        return sorted(self._packages.get(name, {}))

    def dependencies(self, name, version):
        return dict(self._packages[name][version])

    def resolve(self, name, constraints):
        """Highest available version of *name* satisfying every constraint, or None."""
        best = None
        for version in self.versions(name):
            if all(constraint.allows(version) for constraint in constraints):
                if best is None or version > best:
                    best = version
        return best

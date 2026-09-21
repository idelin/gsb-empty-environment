"""Local JSON package registry — packages are never looked up online."""
from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

from .version import Version


class PackageError(Exception):
    """A package/version is missing from the local manifest."""


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    inputs: dict


class PackageRegistry:
    """Resolves packages exclusively from a local JSON manifest."""

    def __init__(self, packages):
        self._by_name: dict[str, list[Package]] = {}
        for package in packages:
            self._by_name.setdefault(package.name, []).append(package)
        for versions in self._by_name.values():
            versions.sort(key=lambda pkg: pkg.version)

    @classmethod
    def from_file(cls, path) -> "PackageRegistry":
        data = json.loads(Path(path).read_text("utf-8"))
        packages = []
        for item in data.get("packages", []):
            packages.append(Package(
                name=item["name"],
                version=Version.parse(str(item["version"])),
                inputs=item.get("inputs", {}),
            ))
        return cls(packages)

    def resolve(self, name: str, version: str | Version | None = None) -> Package:
        candidates = self._by_name.get(name)
        if not candidates:
            raise PackageError(
                f"package {name!r} not found in local manifest (offline mode)")
        if version is None:
            return candidates[-1]
        wanted = Version.parse(version) if isinstance(version, str) else version
        for package in candidates:
            if package.version == wanted:
                return package
        raise PackageError(
            f"package {name!r} version {wanted} not in local manifest")

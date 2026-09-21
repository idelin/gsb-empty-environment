"""Offline package registry read from a local JSON file."""

from __future__ import annotations

import json
from pathlib import Path

from .versions import Version, parse_version


class RegistryError(ValueError):
    """Base class for registry data problems."""


class UnknownPackageError(RegistryError):
    def __init__(self, package: str):
        super().__init__(f"unknown package: {package!r}")
        self.package = package


class UnknownVersionError(RegistryError):
    def __init__(self, package: str, version: str):
        super().__init__(f"package {package!r} has no version {version}")
        self.package = package
        self.version = version


class PackageRegistry:
    """Maps ``package -> {version_string: Version}`` using local JSON only.

    Expected JSON shape::

        {
          "packages": {
            "libcore": ["1.0.0", "1.1.0", "2.0.0-rc.1"]
          }
        }
    """

    def __init__(self, packages: dict[str, list[Version]]):
        self._packages = {name: sorted(versions) for name, versions in packages.items()}

    @classmethod
    def from_file(cls, path: str | Path) -> "PackageRegistry":
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        if not isinstance(data, dict) or not isinstance(data.get("packages"), dict):
            raise RegistryError("registry JSON must be an object with a 'packages' object")
        packages: dict[str, list[Version]] = {}
        for name, versions in data["packages"].items():
            if not isinstance(name, str) or not name:
                raise RegistryError("package names must be non-empty strings")
            if not isinstance(versions, list) or not versions:
                raise RegistryError(f"package {name!r} must list at least one version")
            parsed = [parse_version(v) for v in versions]
            if len({str(v) for v in parsed}) != len(parsed):
                raise RegistryError(f"duplicate versions for package {name!r}")
            packages[name] = parsed
        return cls(packages)

    def has(self, package: str, version: Version) -> bool:
        available = self._packages.get(package)
        return available is not None and version in available

    def require(self, package: str, version: Version) -> None:
        if package not in self._packages:
            raise UnknownPackageError(package)
        if version not in self._packages[package]:
            raise UnknownVersionError(package, str(version))

    def resolve(self, package: str, spec: str, *, allow_prerelease: bool = False) -> Version:
        """Resolve a local build-input requirement.

        ``spec`` is either an exact version (``"1.2.3-rc.1"``) or the keyword
        ``"latest"``, which selects the highest available version (pre-releases
        included only when ``allow_prerelease`` is true).
        """
        if package not in self._packages:
            raise UnknownPackageError(package)
        candidates = self._packages[package]
        if spec == "latest":
            pool = [v for v in candidates if allow_prerelease or not v.is_prerelease]
            if not pool:
                raise UnknownVersionError(package, "latest(stable)")
            return pool[-1]
        version = parse_version(spec)
        if not allow_prerelease and version.is_prerelease:
            raise RegistryError(
                f"prerelease {version} of {package!r} requires include_prerelease"
            )
        self.require(package, version)
        return version

    def packages(self) -> list[str]:
        return sorted(self._packages)

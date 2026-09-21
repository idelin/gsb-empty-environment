"""Package registry loaded from a local JSON document."""

from __future__ import annotations

from dataclasses import dataclass

from .constraints import Constraint
from .version import Version


@dataclass(frozen=True)
class PackageRelease:
    name: str
    version: Version
    dependencies: tuple[tuple[str, Constraint], ...] = ()


class Registry:
    def __init__(self) -> None:
        self._packages: dict[str, dict[Version, PackageRelease]] = {}

    def add(self, release: PackageRelease) -> None:
        versions = self._packages.setdefault(release.name, {})
        if release.version in versions:
            raise ValueError(
                f"duplicate release: {release.name}@{release.version}"
            )
        versions[release.version] = release

    def names(self) -> list[str]:
        return sorted(self._packages)

    def versions(self, name: str) -> list[PackageRelease]:
        """Releases of `name`, newest first."""
        return [
            self._packages[name][version]
            for version in sorted(self._packages.get(name, {}), reverse=True)
        ]

    def get(self, name: str, version: Version) -> PackageRelease | None:
        return self._packages.get(name, {}).get(version)

    @classmethod
    def from_dict(cls, data: dict) -> "Registry":
        """Build from the "packages" section of the input JSON:
        {name: {version: {"dependencies": {dep: constraint}}}}"""
        registry = cls()
        for name, releases in data.items():
            for version_text, info in releases.items():
                deps = tuple(
                    sorted(
                        (dep, Constraint.parse(spec))
                        for dep, spec in (info or {}).get("dependencies", {}).items()
                    )
                )
                registry.add(
                    PackageRelease(name, Version.parse(version_text), deps)
                )
        return registry

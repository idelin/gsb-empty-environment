"""Load and validate the local JSON inputs: repository, lockfile, requirements."""

from __future__ import annotations

import json

from .constraints import Constraint
from .version import Version


class InputError(ValueError):
    """Raised when an input JSON document is malformed."""


def _load_json(path: str):
    with open(path, "r", encoding="utf-8") as handle:
        try:
            return json.load(handle)
        except json.JSONDecodeError as exc:
            raise InputError(f"{path}: invalid JSON: {exc}") from exc


class Repository:
    """Available packages: name -> {Version: {dep_name: Constraint}}."""

    def __init__(self, packages: dict):
        self._packages = packages
        self._sorted = {
            name: tuple(sorted(versions)) for name, versions in packages.items()
        }

    @classmethod
    def from_json(cls, data) -> "Repository":
        if not isinstance(data, dict) or not isinstance(data.get("packages"), dict):
            raise InputError('repository JSON must be an object with a "packages" object')
        packages = {}
        for name, versions in data["packages"].items():
            if not isinstance(versions, dict) or not versions:
                raise InputError(f"package {name!r}: expected a non-empty version map")
            parsed = {}
            for version_text, meta in versions.items():
                version = Version.parse(version_text)
                if str(version) != version_text:
                    raise InputError(
                        f"package {name!r}: version key {version_text!r} is not canonical"
                    )
                meta = meta or {}
                deps = meta.get("dependencies", {})
                if not isinstance(deps, dict):
                    raise InputError(
                        f"package {name!r} {version_text}: dependencies must be an object"
                    )
                parsed[version] = {
                    dep: Constraint.parse(expr) for dep, expr in deps.items()
                }
            packages[name] = parsed
        return cls(packages)

    @classmethod
    def load(cls, path: str) -> "Repository":
        return cls.from_json(_load_json(path))

    def has_package(self, name: str) -> bool:
        return name in self._packages

    def versions(self, name: str):
        """Ascending tuple of available versions, or None if unknown package."""
        return self._sorted.get(name)

    def dependencies(self, name: str, version: Version) -> dict:
        return self._packages[name][version]


class Lock:
    """Currently locked versions: name -> Version."""

    def __init__(self, pinned: dict):
        self.pinned = pinned

    @classmethod
    def from_json(cls, data) -> "Lock":
        if not isinstance(data, dict) or not isinstance(data.get("locked"), dict):
            raise InputError('lock JSON must be an object with a "locked" object')
        return cls({name: Version.parse(v) for name, v in data["locked"].items()})

    @classmethod
    def load(cls, path: str) -> "Lock":
        return cls.from_json(_load_json(path))

    def to_json(self) -> dict:
        return {
            "locked": {
                name: str(self.pinned[name]) for name in sorted(self.pinned)
            }
        }


class Requirements:
    """Root (build input) constraints: name -> Constraint."""

    def __init__(self, constraints: dict):
        self.constraints = constraints

    @classmethod
    def from_json(cls, data) -> "Requirements":
        if not isinstance(data, dict) or not isinstance(data.get("requirements"), dict):
            raise InputError(
                'requirements JSON must be an object with a "requirements" object'
            )
        return cls(
            {name: Constraint.parse(expr) for name, expr in data["requirements"].items()}
        )

    @classmethod
    def load(cls, path: str) -> "Requirements":
        return cls.from_json(_load_json(path))

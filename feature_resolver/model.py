"""Local JSON documents: the package registry and the build (root) request."""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

from . import expr
from .errors import ModelError, VersionError
from .version import Requirement, Version


def _load_json(path: str | Path) -> object:
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    except json.JSONDecodeError as exc:
        raise ModelError(f"{path}: invalid JSON: {exc}") from exc


def _str_tuple(value: object, what: str) -> tuple[str, ...]:
    if value is None:
        return ()
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        raise ModelError(f"{what} must be a list of strings")
    return tuple(dict.fromkeys(value))


def _parse_requirement(value: object, what: str) -> Requirement:
    try:
        return Requirement.parse(value if value is not None else "*")
    except VersionError as exc:
        raise ModelError(f"{what}: {exc}") from exc


@dataclass(frozen=True)
class Dependency:
    name: str
    req: Requirement
    optional: bool
    features: tuple[str, ...]
    disable: tuple[str, ...]
    default_features: bool
    when: expr.Node | None
    when_text: str | None

    @classmethod
    def from_dict(cls, data: object, owner: str) -> "Dependency":
        what = f"package {owner!r}: dependency"
        if not isinstance(data, dict):
            raise ModelError(f"{what} must be an object")
        name = data.get("name")
        if not isinstance(name, str) or not name:
            raise ModelError(f"{what} needs a non-empty 'name'")
        what = f"package {owner!r}: dependency {name!r}"
        when_text = data.get("when")
        when = None
        if when_text is not None:
            if not isinstance(when_text, str):
                raise ModelError(f"{what}: 'when' must be a string")
            try:
                when = expr.parse(when_text)
            except Exception as exc:
                raise ModelError(f"{what}: invalid 'when' expression: {exc}") from exc
        default_features = data.get("default-features", data.get("default_features", True))
        return cls(
            name=name,
            req=_parse_requirement(data.get("req"), f"{what} 'req'"),
            optional=bool(data.get("optional", False)),
            features=_str_tuple(data.get("features"), f"{what} 'features'"),
            disable=_str_tuple(data.get("disable"), f"{what} 'disable'"),
            default_features=bool(default_features),
            when=when,
            when_text=when_text,
        )

    def trigger_features(self) -> tuple[str, ...]:
        """Features of the owning package that may switch this dependency on."""
        if not self.optional:
            return ()
        if self.when is None:
            return (self.name,)
        return tuple(sorted(expr.names(self.when)))


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    features: dict[str, tuple[str, ...]]
    dependencies: tuple[Dependency, ...]

    @classmethod
    def from_dict(cls, data: object) -> "Package":
        if not isinstance(data, dict):
            raise ModelError("each package entry must be an object")
        name = data.get("name")
        if not isinstance(name, str) or not name:
            raise ModelError("each package needs a non-empty 'name'")
        what = f"package {name!r}"
        try:
            version = Version.parse(data.get("version"))
        except VersionError as exc:
            raise ModelError(f"{what}: {exc}") from exc
        raw_features = data.get("features", {})
        if not isinstance(raw_features, dict):
            raise ModelError(f"{what}: 'features' must be an object")
        features: dict[str, tuple[str, ...]] = {}
        for feat, implied in raw_features.items():
            if not isinstance(feat, str):
                raise ModelError(f"{what}: feature names must be strings")
            features[feat] = _str_tuple(implied, f"{what}: feature {feat!r}")
        raw_deps = data.get("dependencies", [])
        if not isinstance(raw_deps, list):
            raise ModelError(f"{what}: 'dependencies' must be a list")
        dependencies = tuple(Dependency.from_dict(item, name) for item in raw_deps)
        for dep in dependencies:
            if dep.optional and dep.when is None and dep.name not in features:
                features[dep.name] = ()
        for feat, implied in features.items():
            for target in implied:
                if target not in features:
                    raise ModelError(
                        f"{what}: feature {feat!r} implies unknown feature {target!r}"
                    )
        for dep in dependencies:
            if dep.when is None:
                continue
            for ref in sorted(expr.names(dep.when)):
                if ref not in features:
                    raise ModelError(
                        f"{what}: dependency {dep.name!r} 'when' references"
                        f" unknown feature {ref!r}"
                    )
        return cls(name, version, features, dependencies)


@dataclass(frozen=True)
class Registry:
    packages: dict[str, tuple[Package, ...]]

    @classmethod
    def from_dict(cls, data: object) -> "Registry":
        if not isinstance(data, dict) or not isinstance(data.get("packages"), list):
            raise ModelError("registry must be an object with a 'packages' list")
        by_name: dict[str, dict[Version, Package]] = {}
        for entry in data["packages"]:
            package = Package.from_dict(entry)
            versions = by_name.setdefault(package.name, {})
            if package.version in versions:
                raise ModelError(
                    f"duplicate package {package.name!r} version {package.version}"
                )
            versions[package.version] = package
        return cls(
            {
                name: tuple(sorted(versions.values(), key=lambda pkg: pkg.version))
                for name, versions in by_name.items()
            }
        )

    @classmethod
    def load(cls, path: str | Path) -> "Registry":
        return cls.from_dict(_load_json(path))

    def known(self, name: str) -> bool:
        return name in self.packages

    def find(self, name: str, reqs: list[Requirement]) -> Package | None:
        """Highest version satisfying every accumulated requirement, if any."""
        for package in reversed(self.packages.get(name, ())):
            if all(req.matches(package.version) for req in reqs):
                return package
        return None


@dataclass(frozen=True)
class RootRequest:
    """The build input: which package to seed, with which feature states."""

    package: str
    req: Requirement
    features: tuple[str, ...]
    disable: tuple[str, ...]
    default_features: bool

    @classmethod
    def from_dict(cls, data: object) -> "RootRequest":
        if not isinstance(data, dict):
            raise ModelError("request must be an object")
        package = data.get("root", data.get("package"))
        if not isinstance(package, str) or not package:
            raise ModelError("request needs a non-empty 'root' package name")
        default_features = data.get("default_features", data.get("default-features", True))
        return cls(
            package=package,
            req=_parse_requirement(
                data.get("req", data.get("version")), "request 'req'"
            ),
            features=_str_tuple(data.get("features"), "request 'features'"),
            disable=_str_tuple(data.get("disable"), "request 'disable'"),
            default_features=bool(default_features),
        )

    @classmethod
    def load(cls, path: str | Path) -> "RootRequest":
        return cls.from_dict(_load_json(path))

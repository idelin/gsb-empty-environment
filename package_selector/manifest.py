from __future__ import annotations

from dataclasses import dataclass
import json
from pathlib import Path
import re
from typing import Any

from .versions import Constraint, Version, parse_constraints


class ManifestError(ValueError):
    pass


@dataclass(frozen=True, slots=True)
class Requirement:
    capability: str
    constraints: tuple[Constraint, ...] = (Constraint("*"),)
    alternatives: tuple[tuple["Requirement", ...], ...] = ()

    @property
    def is_alternative_group(self) -> bool:
        return bool(self.alternatives)


@dataclass(frozen=True, slots=True)
class Provider:
    package: str
    package_version: Version
    capability: str
    version: Version
    targets: frozenset[str]


@dataclass(frozen=True, slots=True)
class ConflictRule:
    package: str = ""
    capability: str = ""
    constraints: tuple[Constraint, ...] = (Constraint("*"),)


@dataclass(frozen=True, slots=True)
class Package:
    name: str
    version: Version
    providers: tuple[Provider, ...]
    requires: tuple[Requirement, ...]
    conflicts: tuple[ConflictRule, ...]
    targets: frozenset[str]


@dataclass(frozen=True, slots=True)
class Build:
    name: str
    requirements: tuple[Requirement, ...]
    target: str = ""
    allow_prereleases: frozenset[str] = frozenset()


@dataclass(frozen=True, slots=True)
class Manifest:
    packages: tuple[Package, ...]
    builds: tuple[Build, ...]


_REQUIREMENT_RE = re.compile(r"^\s*([A-Za-z0-9_.:-]+)\s*(.*?)\s*$")


def _require_mapping(value: Any, context: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ManifestError(f"{context} must be an object")
    return value


def _normalize_targets(value: Any, context: str) -> frozenset[str]:
    if value is None:
        return frozenset()
    if isinstance(value, str):
        return frozenset({value})
    if isinstance(value, (list, tuple)) and all(isinstance(item, str) for item in value):
        return frozenset(value)
    raise ManifestError(f"{context} must be a string or a list of strings")


def normalize_requirement(value: Any, context: str = "requirement") -> Requirement:
    if isinstance(value, str):
        match = _REQUIREMENT_RE.fullmatch(value)
        if not match or not match.group(1):
            raise ManifestError(f"invalid {context}: {value!r}")
        capability = match.group(1)
        constraint_text = match.group(2)
        constraints = parse_constraints(constraint_text if constraint_text else None)
        return Requirement(capability, constraints)

    data = _require_mapping(value, context)
    alternative_key = next((key for key in ("any_of", "alternatives") if key in data), "")
    if alternative_key:
        unknown = set(data) - {alternative_key}
        if unknown:
            raise ManifestError(f"{context} alternative group cannot contain {sorted(unknown)!r}")
        raw_options = data[alternative_key]
        if not isinstance(raw_options, list) or not raw_options:
            raise ManifestError(f"{context}.{alternative_key} must be a non-empty list")
        options = tuple(
            tuple(
                (
                    normalized
                    for normalized in (
                        normalize_requirement(
                            item,
                            f"{context}.{alternative_key}[{index}][{item_index}]",
                        )
                        for item_index, item in enumerate(
                            option if isinstance(option, list) else [option]
                        )
                    )
                    if not normalized.is_alternative_group
                )
            )
            for index, option in enumerate(raw_options)
        )
        if any(not option for option in options):
            raise ManifestError(f"{context}.{alternative_key} cannot contain nested alternative groups")
        return Requirement("", alternatives=options)

    capability = data.get("capability") or data.get("name")
    if not isinstance(capability, str) or not capability:
        raise ManifestError(f"{context} requires a non-empty capability string")
    if "constraint" in data and "constraints" in data:
        raise ManifestError(f"{context} cannot specify both constraint and constraints")
    raw_constraints = data.get("constraints", data.get("constraint", None))
    constraints = parse_constraints(raw_constraints)
    if "version" in data:
        if raw_constraints is not None:
            raise ManifestError(f"{context} cannot specify both version and constraints")
        constraints = parse_constraints(f"=={data['version']}")
    return Requirement(capability, constraints)


def _normalize_requirements(value: Any, context: str) -> tuple[Requirement, ...]:
    if value is None:
        return ()
    if not isinstance(value, list):
        raise ManifestError(f"{context} must be a list")
    return tuple(normalize_requirement(item, f"{context}[{index}]")
                 for index, item in enumerate(value))


def _parse_constraint_field(data: dict[str, Any], context: str) -> tuple[Constraint, ...]:
    raw_constraints = data.get("constraints", data.get("constraint", None))
    if "version" in data:
        if raw_constraints is not None:
            raise ManifestError(f"{context} cannot specify both version and constraints")
        return parse_constraints(f"=={data['version']}")
    return parse_constraints(raw_constraints)


def _normalize_conflicts(values: Any, package_name: str) -> tuple[ConflictRule, ...]:
    if values is None:
        return ()
    if not isinstance(values, list):
        raise ManifestError(f"package {package_name!r}: conflicts must be a list")
    rules: list[ConflictRule] = []
    for index, raw in enumerate(values):
        context = f"package {package_name!r}: conflicts[{index}]"
        if isinstance(raw, str):
            rules.append(ConflictRule(package=raw))
            continue
        data = _require_mapping(raw, context)
        conflict_package = data.get("package", "")
        capability = data.get("capability", "")
        if bool(conflict_package) == bool(capability):
            raise ManifestError(f"{context} must specify exactly one of package or capability")
        if not isinstance(conflict_package, str) or not isinstance(capability, str):
            raise ManifestError(f"{context} package and capability must be strings")
        rules.append(ConflictRule(conflict_package, capability,
                                  _parse_constraint_field(data, context)))
    return tuple(rules)


def _normalize_package(raw: Any, index: int) -> Package:
    context = f"packages[{index}]"
    data = _require_mapping(raw, context)
    name = data.get("name")
    if not isinstance(name, str) or not name:
        raise ManifestError(f"{context}.name must be a non-empty string")
    version = Version.parse(data.get("version"))
    package_targets = _normalize_targets(data.get("targets", data.get("target")),
                                        f"{context}.targets")

    raw_provides = data.get("provides", [])
    if not isinstance(raw_provides, list) or not raw_provides:
        raise ManifestError(f"package {name!r}: provides must be a non-empty list")
    providers: list[Provider] = []
    seen_capabilities: set[str] = set()
    for provider_index, raw_provider in enumerate(raw_provides):
        provider_context = f"package {name!r}: provides[{provider_index}]"
        if isinstance(raw_provider, str):
            capability = raw_provider
            provider_version = version
            provider_targets = package_targets
        else:
            provider_data = _require_mapping(raw_provider, provider_context)
            capability = provider_data.get("capability", provider_data.get("name", ""))
            if not isinstance(capability, str) or not capability:
                raise ManifestError(f"{provider_context}.capability must be a non-empty string")
            provider_version = Version.parse(provider_data.get("version", version))
            provider_targets = _normalize_targets(
                provider_data.get("targets", provider_data.get("target")),
                f"{provider_context}.targets",
            ) or package_targets
        if capability in seen_capabilities:
            raise ManifestError(f"package {name!r} provides capability {capability!r} more than once")
        seen_capabilities.add(capability)
        providers.append(Provider(name, version, capability, provider_version, provider_targets))

    return Package(
        name=name,
        version=version,
        providers=tuple(providers),
        requires=_normalize_requirements(data.get("requires"), f"package {name!r}.requires"),
        conflicts=_normalize_conflicts(data.get("conflicts"), name),
        targets=package_targets,
    )


def _normalize_build(raw: Any, index: int) -> Build:
    context = f"builds[{index}]"
    data = _require_mapping(raw, context)
    name = data.get("name", f"build-{index}")
    if not isinstance(name, str) or not name:
        raise ManifestError(f"{context}.name must be a non-empty string")
    raw_target = data.get("target", data.get("targets", ""))
    if isinstance(raw_target, list):
        if len(raw_target) != 1 or not isinstance(raw_target[0], str):
            raise ManifestError(f"{context}.target must contain exactly one target")
        target = raw_target[0]
    elif isinstance(raw_target, str):
        target = raw_target
    else:
        raise ManifestError(f"{context}.target must be a string")
    allow_prereleases = data.get("allow_prereleases", data.get("allow_prerelease", []))
    if isinstance(allow_prereleases, bool):
        allowed = frozenset() if not allow_prereleases else frozenset({"*"})
    elif isinstance(allow_prereleases, str):
        allowed = frozenset({allow_prereleases})
    elif (isinstance(allow_prereleases, list)
          and all(isinstance(item, str) for item in allow_prereleases)):
        allowed = frozenset(allow_prereleases)
    else:
        raise ManifestError(
            f"{context}.allow_prereleases must be a boolean, string, or string list"
        )
    return Build(
        name=name,
        requirements=_normalize_requirements(data.get("requirements", []),
                                             f"{context}.requirements"),
        target=target,
        allow_prereleases=allowed,
    )


def load_manifest(value: dict[str, Any]) -> Manifest:
    raw_packages = value.get("packages", value.get("implementations"))
    if raw_packages is None:
        raise ManifestError("manifest must contain a packages list")
    if not isinstance(raw_packages, list) or not raw_packages:
        raise ManifestError("packages must be a non-empty list")
    packages = tuple(_normalize_package(item, index)
                     for index, item in enumerate(raw_packages))

    package_keys = [(package.name, package.version) for package in packages]
    if len(package_keys) != len(set(package_keys)):
        duplicate = sorted({
            f"{name}@{version}"
            for name, version in package_keys
            if package_keys.count((name, version)) > 1
        })
        raise ManifestError(f"duplicate package name/version declarations: {duplicate}")

    raw_builds = value.get("builds", [])
    if not isinstance(raw_builds, list):
        raise ManifestError("builds must be a list")
    builds = tuple(_normalize_build(item, index)
                   for index, item in enumerate(raw_builds))
    return Manifest(packages=packages, builds=builds)


def load_manifest_file(path: str | Path) -> Manifest:
    with Path(path).open("r", encoding="utf-8") as handle:
        data = json.load(handle)
    if not isinstance(data, dict):
        raise ManifestError("manifest root must be a JSON object")
    return load_manifest(data)

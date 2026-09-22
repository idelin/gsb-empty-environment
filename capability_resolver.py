"""Resolve abstract capabilities to concrete package implementations."""

from __future__ import annotations

import argparse
import functools
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


NAME_PATTERN = re.compile(r"[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?(?::[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)*")


class ResolverError(Exception):
    """Base class for resolver failures."""


class InvalidInput(ResolverError):
    """The local package or build document is malformed."""


class ResolutionImpossible(ResolverError):
    """No set of packages satisfies all constraints."""


class ConflictError(ResolutionImpossible):
    """A selected package or capability violates exclusivity."""


@dataclass(frozen=True, eq=False, order=False)
class Version:
    major: int
    minor: int
    patch: int
    prerelease: tuple[Any, ...] = field(default_factory=tuple, compare=False)

    @classmethod
    def parse(cls, value: str) -> "Version":
        text = str(value).strip()
        parts = text.split(".")
        if len(parts) != 3:
            raise InvalidInput(f"invalid version {value!r}: expected major.minor.patch")
        numbers = []
        for part in parts:
            if not part.isdigit() or (len(part) > 1 and part.startswith("0")):
                raise InvalidInput(f"invalid numeric version component in {value!r}")
            numbers.append(int(part))
        return cls(numbers[0], numbers[1], numbers[2])

    def __str__(self) -> str:
        value = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            labels = []
            for kind, number, text in self.prerelease:
                labels.append(str(number) if kind == 0 else text)
            value = value + "-" + ".".join(labels)
        return value

    def _prerelease_key(self) -> tuple[Any, ...]:
        return self.prerelease

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Version):
            return NotImplemented
        return (
            (self.major, self.minor, self.patch, self._prerelease_key())
            == (other.major, other.minor, other.patch, other._prerelease_key())
        )

    def __hash__(self) -> int:
        return hash((self.major, self.minor, self.patch, self.prerelease))

    def __lt__(self, other: "Version") -> bool:
        core = (self.major, self.minor, self.patch)
        other_core = (other.major, other.minor, other.patch)
        if core != other_core:
            return core < other_core
        if not self.prerelease and not other.prerelease:
            return False
        if not self.prerelease:
            return False
        if not other.prerelease:
            return True
        left = self._prerelease_key()
        right = other._prerelease_key()
        if left == right:
            return False
        min_length = min(len(left), len(right))
        for index in range(min_length):
            if left[index] != right[index]:
                left_kind, left_number, left_text = left[index]
                right_kind, right_number, right_text = right[index]
                if left_kind != right_kind:
                    return left_kind < right_kind
                if left_kind == 0:
                    return left_number < right_number
                return left_text < right_text
        return len(left) < len(right)

    def __le__(self, other: "Version") -> bool:
        return self == other or self < other

    def __gt__(self, other: "Version") -> bool:
        return other < self

    def __ge__(self, other: "Version") -> bool:
        return self == other or other < self


def _identifier_key(value: str) -> Any:
    if not re.fullmatch(r"[0-9A-Za-z-]+", value):
        raise InvalidInput(f"invalid prerelease identifier: {value}")
    if value.isdigit():
        if len(value) > 1 and value.startswith("0"):
            raise InvalidInput(f"numeric prerelease identifier has leading zero: {value}")
        return (0, int(value), "")
    return (1, 0, value)


def _parse_prerelease(value: str) -> tuple[Any, ...]:
    if not value:
        raise InvalidInput("empty prerelease identifier")
    return tuple(_identifier_key(part) for part in value.split("."))


def parse_version(value: str) -> Version:
    if not isinstance(value, str):
        raise InvalidInput("version must be a string")
    text = value.strip()
    before_build, build_sep, _build_metadata = text.partition("+")
    if build_sep and not _build_metadata:
        raise InvalidInput(f"invalid version {value!r}")
    core, sep, suffix = before_build.partition("-")
    version = Version.parse(core)
    if sep:
        object.__setattr__(version, "prerelease", _parse_prerelease(suffix))
    return version


@dataclass(frozen=True)
class Constraint:
    operator: str
    version: Version

    @classmethod
    def parse(cls, expression: str) -> "Constraint":
        text = expression.strip()
        if not text:
            raise InvalidInput("empty version constraint")
        for operator in (">=", "<=", "==", ">", "<", "="):
            if text.startswith(operator):
                if not text[len(operator):].strip():
                    raise InvalidInput(f"version constraint {expression!r} has no version")
                normalized = "==" if operator == "=" else operator
                return cls(normalized, parse_version(text[len(operator):].strip()))
        return cls("==", parse_version(text))

    def allows(self, candidate: Version) -> bool:
        if self.operator == "==":
            return candidate == self.version
        if self.operator == ">=":
            return candidate >= self.version
        if self.operator == "<=":
            return candidate <= self.version
        if self.operator == ">":
            return candidate > self.version
        return candidate < self.version

    @property
    def allows_prerelease(self) -> bool:
        return bool(self.version.prerelease)

    def __str__(self) -> str:
        return f"{self.operator}{self.version}"


@dataclass(frozen=True)
class Requirement:
    capability: str
    constraints: tuple[Constraint, ...]
    allow_prerelease: bool | None = None

    @classmethod
    def parse(cls, value: str | dict[str, Any], default_prerelease: bool = False) -> "Requirement":
        if isinstance(value, str):
            capability, expression = split_requirement(value)
            constraints = tuple(Constraint.parse(part) for part in expression.split(",") if part.strip())
            return cls(capability, constraints)
        if not isinstance(value, dict):
            raise InvalidInput("requirement must be a string or object")
        capability = value.get("capability")
        if not valid_name(capability):
            raise InvalidInput("requirement requires a non-empty string capability")
        expressions = value.get("constraints", [])
        if isinstance(expressions, str):
            expressions = [expressions] if expressions.strip() else []
        if not isinstance(expressions, list):
            raise InvalidInput("constraints must be a list or string")
        if not all(isinstance(expression, str) for expression in expressions):
            raise InvalidInput("each constraint must be a string")
        constraints = tuple(Constraint.parse(expression) for expression in expressions)
        if "allow_prerelease" in value:
            prerelease = value["allow_prerelease"]
        else:
            prerelease = None
        if prerelease is not None and not isinstance(prerelease, bool):
            raise InvalidInput("allow_prerelease must be true or false")
        return cls(capability, constraints, prerelease)

    def prerelease_allowed(self, build_default: bool) -> bool:
        if self.allow_prerelease is not None:
            return self.allow_prerelease
        return build_default or any(constraint.allows_prerelease for constraint in self.constraints)

    def satisfied_by(self, version: Version, build_default: bool) -> bool:
        if version.prerelease and not self.prerelease_allowed(build_default):
            return False
        return all(constraint.allows(version) for constraint in self.constraints)

    def merged_with(self, other: "Requirement") -> "Requirement":
        if self.capability != other.capability:
            raise ValueError("cannot merge requirements for different capabilities")
        prerelease_values = {value for value in (self.allow_prerelease, other.allow_prerelease) if value is not None}
        prerelease = False if False in prerelease_values else (True if True in prerelease_values else None)
        return Requirement(self.capability, self.constraints + other.constraints, prerelease)

    def __str__(self) -> str:
        if not self.constraints:
            return self.capability
        return f"{self.capability} {','.join(str(item) for item in self.constraints)}"


def valid_name(value: Any) -> bool:
    return (
        isinstance(value, str)
        and NAME_PATTERN.fullmatch(value) is not None
    )


def split_requirement(value: str) -> tuple[str, str]:
    text = value.strip()
    for index, char in enumerate(text):
        if char.isspace() or char in ("=", "<", ">"):
            capability = text[:index].strip()
            if valid_name(capability):
                return capability, text[index:].strip()
            break
    if valid_name(text):
        return text, ""
    raise InvalidInput(f"invalid requirement {value!r}")


def as_string_list(value: Any, field_name: str) -> list[str]:
    if value is None:
        return []
    if not isinstance(value, list) or not all(isinstance(item, str) and item for item in value):
        raise InvalidInput(f"{field_name} must be a list of non-empty strings")
    return list(value)


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    provides: frozenset[str]
    requires: tuple[Requirement, ...]
    conflicts: frozenset[str]
    target: dict[str, str]

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "Package":
        if not isinstance(data, dict):
            raise InvalidInput("each package must be an object")
        name = data.get("name")
        if not valid_name(name):
            raise InvalidInput("package requires a valid name")
        version = parse_version(data.get("version"))
        provides = frozenset(as_string_list(data.get("provides", []), "provides"))
        if not all(valid_name(item) for item in provides):
            raise InvalidInput(f"invalid provided capability in package {name}")
        requires_data = data.get("requires", [])
        if not isinstance(requires_data, list):
            raise InvalidInput(f"requires must be a list in package {name}")
        requires = tuple(Requirement.parse(item) for item in requires_data)
        conflicts = frozenset(as_string_list(data.get("conflicts", []), "conflicts"))
        if not all(valid_name(item) for item in conflicts):
            raise InvalidInput(f"invalid conflict name in package {name}")
        target_data = data.get("target", {})
        if target_data is None:
            target_data = {}
        if not isinstance(target_data, dict) or not all(
            isinstance(key, str) and isinstance(value, str) for key, value in target_data.items()
        ):
            raise InvalidInput(f"target must be an object with string values in package {name}")
        return cls(name, version, provides, requires, conflicts, target_data)

    @property
    def implementation(self) -> str:
        return f"{self.name}=={self.version}"


class Repository:
    def __init__(self, packages: list[Package]):
        self.packages: dict[str, list[Package]] = {}
        self.providers: dict[str, list[Package]] = {}
        seen = set()
        for package in packages:
            identity = (package.name, package.version)
            if identity in seen:
                raise InvalidInput(f"duplicate package {package.implementation}")
            seen.add(identity)
            self.packages.setdefault(package.name, []).append(package)
            capabilities = {package.name, *package.provides}
            for capability in capabilities:
                self.providers.setdefault(capability, []).append(package)
        for candidates in self.packages.values():
            candidates.sort(key=functools.cmp_to_key(compare_packages))
        for candidates in self.providers.values():
            candidates.sort(key=functools.cmp_to_key(compare_candidates))

    def candidates_for(self, requirement: Requirement, target: dict[str, str], allow_prerelease: bool) -> list[Package]:
        candidates = []
        for package in self.providers.get(requirement.capability, []):
            if not requirement.satisfied_by(package.version, allow_prerelease):
                continue
            if not target_matches(package.target, target):
                continue
            candidates.append(package)
        return candidates


def compare_packages(left: Package, right: Package) -> int:
    if left.version > right.version:
        return -1
    if left.version < right.version:
        return 1
    if left.name < right.name:
        return -1
    if left.name > right.name:
        return 1
    return 0


def compare_candidates(left: Package, right: Package) -> int:
    result = compare_packages(left, right)
    if result != 0:
        return result
    return 0


def target_matches(package_target: dict[str, str], build_target: dict[str, str]) -> bool:
    return all(build_target.get(key) == value for key, value in package_target.items())


@dataclass
class Build:
    requirements: tuple[Requirement, ...]
    target: dict[str, str]
    allow_prerelease: bool

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "Build":
        if not isinstance(data, dict):
            raise InvalidInput("build must be an object")
        raw_requirements = data.get("requirements", [])
        if not isinstance(raw_requirements, list):
            raise InvalidInput("build requirements must be a list")
        target = data.get("target", {})
        if target is None:
            target = {}
        if not isinstance(target, dict) or not all(
            isinstance(key, str) and isinstance(value, str) for key, value in target.items()
        ):
            raise InvalidInput("build target must be an object with string values")
        prerelease = data.get("allow_prerelease", False)
        if not isinstance(prerelease, bool):
            raise InvalidInput("build allow_prerelease must be true or false")
        requirements = tuple(
            Requirement.parse(item, prerelease) for item in raw_requirements
        )
        return cls(requirements, dict(target), prerelease)


@dataclass
class State:
    selected: dict[str, Package]
    owners: dict[str, str]
    requirements: dict[str, Requirement]
    order: list[str]

    @classmethod
    def empty(cls) -> "State":
        return cls({}, {}, {}, [])

    def snapshot(self) -> tuple[dict[str, Package], dict[str, str], dict[str, Requirement], list[str]]:
        return (dict(self.selected), dict(self.owners), dict(self.requirements), list(self.order))

    def restore(
        self, snapshot: tuple[dict[str, Package], dict[str, str], dict[str, Requirement], list[str]]
    ) -> None:
        self.selected, self.owners, self.requirements, self.order = (
            dict(snapshot[0]),
            dict(snapshot[1]),
            dict(snapshot[2]),
            list(snapshot[3]),
        )

    def ensure_capability_owner(self, package: Package, capability: str) -> None:
        existing = self.owners.get(capability)
        if existing is not None and existing != package.name:
            raise ConflictError(
                f"capability {capability!r} has a single-provider requirement: "
                f"{existing!r} and {package.name!r} both provide it"
            )

    def check_conflict(self, package: Package) -> None:
        name = package.name
        if name in package.conflicts:
            raise ConflictError(f"package {name} declares a conflict with itself")
        if name in self.selected:
            return
        for other_name, other in self.selected.items():
            if name in other.conflicts or other_name in package.conflicts:
                raise ConflictError(f"package {name} conflicts with selected package {other_name}")

    def add(self, package: Package, requirements: list[Requirement], build: Build) -> None:
        if package.name in self.selected:
            selected = self.selected[package.name]
            if selected.version != package.version:
                raise ConflictError(f"package {package.name} selected at two different versions")
            for requirement in requirements:
                if not requirement.satisfied_by(selected.version, build.allow_prerelease):
                    raise ResolutionImpossible(
                        f"selected {selected.implementation} does not satisfy {requirement}"
                    )
                self._add_requirement_to_state(requirement)
            return
        self.check_conflict(package)
        capabilities = package.provides
        for capability in capabilities:
            self.ensure_capability_owner(package, capability)
        for requirement in requirements:
            if not requirement.satisfied_by(package.version, build.allow_prerelease):
                raise ResolutionImpossible(
                    f"selected {package.implementation} does not satisfy {requirement}"
                )
            existing = self.requirements.get(requirement.capability)
            self.requirements[requirement.capability] = (
                existing.merged_with(requirement) if existing else requirement
            )
            owner = self.owners.get(requirement.capability)
            if owner is not None and owner != package.name:
                raise ConflictError(
                    f"capability {requirement.capability!r} is already provided by {owner!r}"
                )
        self.selected[package.name] = package
        for capability in capabilities:
            self.owners[capability] = package.name
        self.order.append(package.name)

    def _add_requirement_to_state(self, requirement: Requirement) -> None:
        existing = self.requirements.get(requirement.capability)
        self.requirements[requirement.capability] = (
            existing.merged_with(requirement) if existing else requirement
        )


class Resolver:
    def __init__(self, repository: Repository, build: Build):
        self.repository = repository
        self.build = build
        self.state = State.empty()

    def resolve(self) -> dict[str, Any]:
        grouped: dict[str, Requirement] = {}
        for requirement in self.build.requirements:
            existing = grouped.get(requirement.capability)
            grouped[requirement.capability] = (
                existing.merged_with(requirement) if existing else requirement
            )
        ordered = sorted(grouped.values(), key=lambda item: item.capability)
        self._resolve_requirements(ordered, frozenset(), frozenset(), top_level=True)
        return self.result()

    def _resolve_requirements(
        self,
        requirements: list[Requirement],
        active_requirements: frozenset[str],
        active_packages: frozenset[str],
        top_level: bool = False,
    ) -> None:
        if not requirements:
            return
        requirement = requirements[0]
        remaining = requirements[1:]
        if requirement.capability in active_requirements:
            raise ResolutionImpossible(
                f"dependency cycle detected while resolving {requirement.capability!r}"
            )
        owner = self.state.owners.get(requirement.capability)
        if owner is not None:
            package = self.state.selected[owner]
            if not requirement.satisfied_by(package.version, self.build.allow_prerelease):
                raise ResolutionImpossible(
                    f"selected {package.implementation} does not satisfy {requirement}"
                )
            snapshot = self.state.snapshot()
            try:
                self._add_requirement_to_state(requirement)
                self._resolve_requirements(remaining, active_requirements, active_packages, top_level)
            except ResolverError:
                self.state.restore(snapshot)
                raise
            return
        candidates = self.repository.candidates_for(
            requirement, self.build.target, self.build.allow_prerelease
        )
        if not candidates:
            raise ResolutionImpossible(f"no implementation provides {requirement} for requested target")
        failures = []
        next_active_requirements = active_requirements | {requirement.capability}
        for package in candidates:
            snapshot = self.state.snapshot()
            try:
                if package.name in active_packages:
                    raise ResolutionImpossible(
                        f"dependency cycle detected through {package.implementation}"
                    )
                self.state.add(package, [requirement], self.build)
                sorted_dependencies = sorted(
                    package.requires, key=lambda item: (item.capability, str(item.constraints))
                )
                self._resolve_requirements(
                    sorted_dependencies,
                    next_active_requirements,
                    active_packages | {package.name},
                )
                self._resolve_requirements(remaining, active_requirements, active_packages, top_level)
                return
            except ResolverError as error:
                failures.append(f"{package.implementation}: {error}")
                self.state.restore(snapshot)
        raise ResolutionImpossible(
            f"cannot satisfy {requirement}; tried: " + "; ".join(failures)
        )

    def _add_requirement_to_state(self, requirement: Requirement) -> None:
        existing = self.state.requirements.get(requirement.capability)
        self.state.requirements[requirement.capability] = (
            existing.merged_with(requirement) if existing else requirement
        )

    def result(self) -> dict[str, Any]:
        mapping = {package.name: package.implementation for package in self.state.selected.values()}
        mapping.update(
            {
                capability: self.state.selected[pkg].implementation
                for capability, pkg in sorted(self.state.owners.items())
            }
        )
        mapping = dict(sorted(mapping.items()))
        selected = [
            {
                "name": package.name,
                "version": str(package.version),
                "provides": sorted(package.provides | {package.name}),
            }
            for package in sorted(self.state.selected.values(), key=lambda item: item.name)
        ]
        return {
            "capability_to_implementation": mapping,
            "selected": selected,
            "selection_order": list(self.state.order),
            "tie_break_rules": tie_break_rules(),
        }


def tie_break_rules() -> list[str]:
    return [
        "Filter candidates by all version constraints, prerelease policy, and build target constraints.",
        "Reject a candidate when it conflicts with a selected package and continue with the next implementation.",
        "Allow exactly one selected provider for every capability.",
        "Prefer the highest semantic version; a prerelease is lower than its final release.",
        "If versions remain equal, choose the lexicographically smallest package name.",
        "Resolve requirements and dependencies by capability name, and versions from high to low, for stable output.",
        "Reject a resolution path that revisits an active capability or package, preventing cyclic alternatives from recursing forever.",
    ]


def load_documents(packages_path: str | Path, build_path: str | Path) -> tuple[Repository, Build]:
    with open(packages_path, "r", encoding="utf-8") as package_file:
        package_document = json.load(package_file)
    with open(build_path, "r", encoding="utf-8") as build_file:
        build_document = json.load(build_file)
    raw_packages = package_document.get("packages", package_document) if isinstance(package_document, dict) else package_document
    if not isinstance(raw_packages, list):
        raise InvalidInput("package input must contain a packages list")
    repository = Repository([Package.from_dict(item) for item in raw_packages])
    build = Build.from_dict(build_document)
    return repository, build


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Resolve capabilities to local package implementations.")
    parser.add_argument("packages", help="Path to local package catalog JSON")
    parser.add_argument("build", help="Path to local build request JSON")
    parser.add_argument("-o", "--output", help="Write resolution JSON to this path")
    args = parser.parse_args(argv)
    try:
        repository, build = load_documents(args.packages, args.build)
        result = Resolver(repository, build).resolve()
    except (OSError, json.JSONDecodeError, ResolverError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    rendered = json.dumps(result, indent=2, ensure_ascii=False, sort_keys=False)
    if args.output:
        Path(args.output).write_text(rendered + "\n", encoding="utf-8")
    print(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

"""Load and validate local JSON package catalogs and build requests."""
from __future__ import annotations

import json
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Any, List, Mapping, Sequence, Tuple, Union

from .errors import BuildRequestError, CatalogError
from .models import Catalog, Package, Requirement, Target
from .version import Version

PathLike = Union[str, Path]


def _read_text(source: Union[PathLike, Mapping[str, Any]], error_type):
    if isinstance(source, Mapping):
        return source
    path = Path(source)
    try:
        with path.open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except OSError as exc:
        raise error_type(f"cannot read {path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise error_type(f"invalid JSON in {path}: {exc}") from exc


def _as_str_list(raw: Any, field_name: str, package_id: str) -> Tuple[str, ...]:
    if raw is None:
        return ()
    if not isinstance(raw, (list, tuple)) or not all(isinstance(v, str) for v in raw):
        raise CatalogError(f"{package_id}: '{field_name}' must be a list of strings")
    return tuple(raw)


def _as_targets(raw: Any, field_name: str, package_id: str) -> Tuple[Target, ...]:
    if raw is None:
        return ()
    if not isinstance(raw, (list, tuple)):
        raise CatalogError(f"{package_id}: '{field_name}' must be a list of objects")
    targets: List[Target] = []
    for item in raw:
        if not isinstance(item, Mapping) or not all(
            isinstance(k, str) and isinstance(v, str) for k, v in item.items()
        ):
            raise CatalogError(
                f"{package_id}: every '{field_name}' entry must be a string->string object"
            )
        targets.append(Target.from_mapping(item))
    return tuple(targets)


def _parse_package(entry: Mapping[str, Any]) -> Package:
    if not isinstance(entry, Mapping):
        raise CatalogError("every package entry must be an object")
    name = entry.get("name")
    version_text = entry.get("version")
    if not isinstance(name, str) or not name:
        raise CatalogError("package is missing a non-empty 'name'")
    if not isinstance(version_text, str) or not version_text:
        raise CatalogError(f"package {name!r} is missing a string 'version'")
    try:
        version = Version(version_text)
    except Exception as exc:
        raise CatalogError(f"package {name!r}: {exc}") from exc

    package_id = f"{name}@{version_text}"
    provides = _as_str_list(entry.get("provides"), "provides", package_id)

    conflicts = frozenset(
        _as_str_list(entry.get("conflicts_with"), "conflicts_with", package_id)
    )
    requires = _as_targets(entry.get("requires_target"), "requires_target", package_id)
    excludes = _as_targets(entry.get("excludes_target"), "excludes_target", package_id)

    replaced_by = entry.get("replaced_by")
    if replaced_by is not None and not isinstance(replaced_by, str):
        raise CatalogError(f"{package_id}: 'replaced_by' must be a package name string")
    if replaced_by == name:
        raise CatalogError(f"{package_id}: 'replaced_by' cannot point to itself")

    unknown = set(entry) - {
        "name", "version", "provides", "conflicts_with",
        "requires_target", "excludes_target", "replaced_by",
    }
    if unknown:
        raise CatalogError(f"{package_id}: unknown field(s) {sorted(unknown)}")

    return Package(
        name=name,
        version=version,
        provides=frozenset(provides),
        conflicts_with=conflicts,
        requires=requires,
        excludes=excludes,
        replaced_by=replaced_by,
    )


def _version_rank(version: Version) -> tuple:
    pre = version.prerelease
    flag = 1 if pre is None else 0
    prekey = tuple((0, int(p)) if p.isdigit() else (1, p) for p in (pre or ()))
    return (version.major, version.minor, version.patch, flag, prekey)


def _package_version_rank(package: Package) -> tuple:
    return _version_rank(package.version)


def _order_token(version: Version) -> int:
    # Monotonic ordering token for primary index sorting only; the resolver
    # compares Version objects directly for authoritative precedence.
    pre = version.prerelease
    base = (version.major << 48) | (version.minor << 24) | version.patch
    if pre is None:
        return base + 1
    return base


def load_catalog(source: Union[PathLike, Mapping[str, Any]]) -> Catalog:
    """Load a package catalog from a JSON file path or an in-memory mapping."""
    data = _read_text(source, CatalogError)
    if not isinstance(data, Mapping):
        raise CatalogError("catalog root must be a JSON object")
    raw_packages = data.get("packages")
    if not isinstance(raw_packages, (list, tuple)):
        raise CatalogError("catalog needs a 'packages' list")

    packages: List[Package] = []
    seen = set()
    for entry in raw_packages:
        package = _parse_package(entry)
        if package.id in seen:
            raise CatalogError(f"duplicate package version {package.id}")
        seen.add(package.id)
        packages.append(package)

    by_name: defaultdict[str, List[Package]] = defaultdict(list)
    capability_index: defaultdict[str, List[Package]] = defaultdict(list)
    alternatives: dict[str, str] = {}
    for package in packages:
        by_name[package.name].append(package)
        for capability in package.provides:
            capability_index[capability].append(package)
        if package.replaced_by is not None:
            previous = alternatives.get(package.name)
            if previous is not None and previous != package.replaced_by:
                raise CatalogError(
                    f"package {package.name!r} declares inconsistent 'replaced_by' "
                    f"targets: {previous!r} vs {package.replaced_by!r}"
                )
            alternatives[package.name] = package.replaced_by

    known_names = set(by_name)
    for name, target in alternatives.items():
        if target not in known_names:
            raise CatalogError(
                f"package {name!r} is replaced_by unknown package {target!r}"
            )

    replacement_targets = set(alternatives.values())
    for package in packages:
        if not package.provides and package.name not in replacement_targets:
            raise CatalogError(
                f"package {package.id} provides no capability and is not a "
                f"replaced_by target"
            )

    ordered_by_name = {
        name: tuple(sorted(group, key=_package_version_rank, reverse=True))
        for name, group in by_name.items()
    }
    # Provider name ascending; the resolver re-sorts providers by descending
    # Version before choosing, but a deterministic base order keeps iteration
    # stable and reproducible.
    ordered_caps = {
        cap: tuple(sorted(group, key=lambda p: (p.name, -_order_token(p.version))))
        for cap, group in capability_index.items()
    }

    return Catalog(
        packages=tuple(packages),
        by_name=ordered_by_name,
        capability_index=ordered_caps,
    )


@dataclass(frozen=True)
class BuildRequestModel:
    requirements: Tuple[Requirement, ...]
    target: Target


def load_build_request(source: Union[PathLike, Mapping[str, Any]]) -> BuildRequestModel:
    """Load a build request from a JSON file path or an in-memory mapping."""
    data = _read_text(source, BuildRequestError)
    if not isinstance(data, Mapping):
        raise BuildRequestError("build request root must be a JSON object")
    raw_requirements = data.get("requirements")
    if not isinstance(raw_requirements, (list, tuple)) or not raw_requirements:
        raise BuildRequestError("build request needs a non-empty 'requirements' list")
    requirements = tuple(Requirement.parse(item) for item in raw_requirements)
    target = Target.from_mapping(data.get("target"))
    return BuildRequestModel(requirements=requirements, target=target)

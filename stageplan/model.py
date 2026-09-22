"""Loading package and build input from a local JSON document."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from .version import Constraint, Version, VersionError


class CatalogError(ValueError):
    """Fatal structural error in the catalog JSON document."""


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    depends_on: tuple[tuple[str, Constraint], ...]
    produces: tuple[str, ...]
    needs_artifacts: tuple[str, ...]


@dataclass
class Catalog:
    packages: dict[str, Package]
    """Package name -> definition.  Only successfully parsed packages appear."""

    producer: dict[str, str]
    """Artifact name -> unique producing package name."""

    package_errors: dict[str, str]
    """Package name -> reason the package definition itself was unusable."""

    duplicate_artifacts: dict[str, tuple[str, ...]]
    """Artifact produced by more than one package -> the producer names."""


def load_catalog(path: str | Path) -> Catalog:
    source = Path(path)
    try:
        data = json.loads(source.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise CatalogError(f"catalog file not found: {source}") from exc
    except json.JSONDecodeError as exc:
        raise CatalogError(f"invalid JSON in {source}: {exc}") from exc
    return catalog_from_data(data)


def catalog_from_data(data: Any) -> Catalog:
    if not isinstance(data, dict):
        raise CatalogError("catalog root must be a JSON object")
    raw_packages = data.get("packages")
    if not isinstance(raw_packages, list):
        raise CatalogError("catalog field 'packages' must be a list")

    packages: dict[str, Package] = {}
    package_errors: dict[str, str] = {}
    seen_names: set[str] = set()

    for index, entry in enumerate(raw_packages):
        try:
            package = _parse_package(entry, index)
        except CatalogError as exc:
            label = _entry_label(entry, index)
            package_errors[label] = str(exc)
            continue
        if package.name in seen_names:
            package_errors[package.name] = "duplicate package definition"
            # Keep the first valid definition out of the usable set to make the
            # conflict unambiguous.
            packages.pop(package.name, None)
            continue
        seen_names.add(package.name)
        packages[package.name] = package

    producer: dict[str, str] = {}
    duplicate_artifacts: dict[str, tuple[str, ...]] = {}
    for package in packages.values():
        for artifact in package.produces:
            if artifact in producer:
                prior = producer[artifact]
                producers = tuple(sorted({prior, package.name}))
                duplicate_artifacts[artifact] = producers
                producer.pop(artifact, None)
            elif artifact in duplicate_artifacts:
                producers = tuple(sorted({*duplicate_artifacts[artifact], package.name}))
                duplicate_artifacts[artifact] = producers
            else:
                producer[artifact] = package.name
    producer = {a: p for a, p in producer.items() if a not in duplicate_artifacts}

    return Catalog(packages, producer, package_errors, duplicate_artifacts)


def _entry_label(entry: Any, index: int) -> str:
    if isinstance(entry, dict) and isinstance(entry.get("name"), str):
        return entry["name"]
    return f"<entry #{index}>"


def _parse_package(entry: Any, index: int) -> Package:
    if not isinstance(entry, dict):
        raise CatalogError(f"package entry #{index} must be an object")
    name = entry.get("name")
    if not isinstance(name, str) or not name:
        raise CatalogError(f"package entry #{index} is missing a string 'name'")

    raw_version = entry.get("version", "0.0.0")
    try:
        version = Version.parse(raw_version) if isinstance(raw_version, str) else None
    except VersionError as exc:
        raise CatalogError(str(exc)) from exc
    if version is None:
        raise CatalogError(f"package {name!r} field 'version' must be a string")

    depends_on = _parse_dependencies(name, entry.get("depends_on", []))
    produces = _parse_string_list(name, entry, "produces")
    needs = _parse_string_list(name, entry, "needs_artifacts")
    return Package(name, version, tuple(depends_on), tuple(produces), tuple(needs))


def _parse_dependencies(package_name: str, raw: Any) -> list[tuple[str, Constraint]]:
    if not isinstance(raw, list):
        raise CatalogError(f"package {package_name!r} field 'depends_on' must be a list")
    result: list[tuple[str, Constraint]] = []
    for item in raw:
        if isinstance(item, str):
            dep_name = item.strip()
            if not dep_name:
                raise CatalogError(f"package {package_name!r} has an empty dependency name")
            result.append((dep_name, Constraint.parse(">=0.0.0")))
            continue
        if isinstance(item, dict):
            dep_name = item.get("name")
            if not isinstance(dep_name, str) or not dep_name:
                raise CatalogError(f"package {package_name!r} has a dependency without a string 'name'")
            spec = item.get("version", ">=0.0.0")
            try:
                constraint = Constraint.parse(spec)
            except VersionError as exc:
                raise CatalogError(
                    f"package {package_name!r} dependency {dep_name!r}: {exc}"
                ) from exc
            result.append((dep_name, constraint))
            continue
        raise CatalogError(f"package {package_name!r} has an invalid dependency entry")
    return result


def _parse_string_list(package_name: str, entry: dict, field_name: str) -> list[str]:
    raw = entry.get(field_name, [])
    if not isinstance(raw, list) or not all(isinstance(x, str) and x for x in raw):
        raise CatalogError(f"package {package_name!r} field {field_name!r} must be a list of strings")
    return list(raw)

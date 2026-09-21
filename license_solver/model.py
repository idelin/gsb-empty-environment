"""Local JSON inputs: a package repository and a build/target description.

No network access is ever performed; everything comes from local files.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

from . import expression
from .version import Version, VersionRange


@dataclass(frozen=True)
class LicenseInfo:
    obligations: frozenset
    tags: frozenset


@dataclass(frozen=True)
class ExceptionInfo:
    condition: str  # target attribute that must be truthy
    removes_obligations: frozenset
    removes_tags: frozenset


@dataclass(frozen=True)
class DepSpec:
    range: VersionRange
    link: str | None = None  # per-edge override of the target link type


@dataclass(frozen=True)
class PackageVersion:
    version: Version
    license_text: str
    expr: object
    deps: dict


@dataclass
class Repository:
    licenses: dict
    exceptions: dict
    packages: dict  # name -> [PackageVersion], sorted ascending

    def find(self, name: str, version: str) -> PackageVersion | None:
        for pv in self.packages.get(name, []):
            if str(pv.version) == version:
                return pv
        return None


@dataclass(frozen=True)
class PropagationRule:
    tag: str
    links: frozenset  # link types through which the tag propagates
    obligations: frozenset  # obligations imposed on the dependent package


@dataclass(frozen=True)
class ExceptionRequiredRule:
    tag: str
    when: tuple  # tuple of (attribute, expected value) pairs

    def matches(self, attributes: dict) -> bool:
        return all(attributes.get(key) == value for key, value in self.when)


@dataclass(frozen=True)
class Rules:
    propagation: tuple = ()
    incompatible: tuple = ()  # tuples of license-name sets
    exception_required: tuple = ()


@dataclass
class Build:
    target: str
    link: str  # "static" | "dynamic"
    attributes: dict
    roots: dict  # package name -> VersionRange
    rules: Rules


def _parse_deps(raw) -> dict:
    deps = {}
    for name, spec in (raw or {}).items():
        if isinstance(spec, str):
            deps[name] = DepSpec(VersionRange.parse(spec))
        else:
            deps[name] = DepSpec(
                VersionRange.parse(spec.get("range", "*")), spec.get("link")
            )
    return deps


def repository_from_dict(data: dict) -> Repository:
    licenses = {
        name: LicenseInfo(
            frozenset(info.get("obligations", ())), frozenset(info.get("tags", ()))
        )
        for name, info in data.get("licenses", {}).items()
    }
    exceptions = {
        name: ExceptionInfo(
            info["condition"],
            frozenset(info.get("removes_obligations", ())),
            frozenset(info.get("removes_tags", ())),
        )
        for name, info in data.get("exceptions", {}).items()
    }
    packages = {}
    for name, pinfo in data.get("packages", {}).items():
        versions = []
        for entry in pinfo.get("versions", []):
            versions.append(
                PackageVersion(
                    version=Version.parse(entry["version"]),
                    license_text=entry["license"],
                    expr=expression.parse(entry["license"]),
                    deps=_parse_deps(entry.get("deps")),
                )
            )
        versions.sort(key=lambda pv: pv.version)
        packages[name] = versions
    return Repository(licenses, exceptions, packages)


def rules_from_dict(data: dict) -> Rules:
    return Rules(
        propagation=tuple(
            PropagationRule(
                r["tag"], frozenset(r["links"]), frozenset(r["obligations"])
            )
            for r in data.get("propagation", [])
        ),
        incompatible=tuple(frozenset(pair) for pair in data.get("incompatible", [])),
        exception_required=tuple(
            ExceptionRequiredRule(r["tag"], tuple(sorted(r.get("when", {}).items())))
            for r in data.get("exception_required", [])
        ),
    )


def build_from_dict(data: dict) -> Build:
    return Build(
        target=data.get("target", "target"),
        link=data.get("link", "static"),
        attributes=dict(data.get("attributes", {})),
        roots={name: VersionRange.parse(r) for name, r in data.get("roots", {}).items()},
        rules=rules_from_dict(data.get("rules", {})),
    )


def load_repository(path) -> Repository:
    return repository_from_dict(json.loads(Path(path).read_text(encoding="utf-8")))


def load_build(path) -> Build:
    return build_from_dict(json.loads(Path(path).read_text(encoding="utf-8")))

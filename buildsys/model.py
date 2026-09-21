"""Data model: target context, edge conditions, packages, local JSON index."""

from __future__ import annotations

import json
from dataclasses import dataclass, field

from .version import Constraint, Version

VALID_KINDS = ("host", "target")


@dataclass(frozen=True)
class TargetContext:
    """The dimensions conditions are evaluated against."""

    os: str
    arch: str
    stage: str


@dataclass(frozen=True)
class DimensionEvidence:
    dimension: str
    required: tuple | None  # None means "no constraint on this dimension"
    actual: str
    matched: bool

    def to_json(self) -> dict:
        return {
            "dimension": self.dimension,
            "required": list(self.required) if self.required is not None else None,
            "actual": self.actual,
            "matched": self.matched,
        }


@dataclass(frozen=True)
class ConditionEvaluation:
    dimensions: tuple  # tuple of DimensionEvidence

    @property
    def matched(self) -> bool:
        return all(d.matched for d in self.dimensions)

    @property
    def failed_dimensions(self) -> tuple:
        return tuple(d.dimension for d in self.dimensions if not d.matched)

    def to_json(self) -> list:
        return [d.to_json() for d in self.dimensions]


@dataclass(frozen=True)
class Condition:
    """Optional per-dimension allow-lists. A missing dimension always matches."""

    os: tuple | None = None
    arch: tuple | None = None
    stage: tuple | None = None

    @classmethod
    def from_json(cls, data: dict | None) -> "Condition":
        if not data:
            return cls()

        def opt_tuple(key):
            value = data.get(key)
            if value is None:
                return None
            if isinstance(value, str):
                return (value,)
            return tuple(value)

        return cls(os=opt_tuple("os"), arch=opt_tuple("arch"), stage=opt_tuple("stage"))

    def evaluate(self, ctx: TargetContext) -> ConditionEvaluation:
        dims = []
        for dimension, required, actual in (
            ("os", self.os, ctx.os),
            ("arch", self.arch, ctx.arch),
            ("stage", self.stage, ctx.stage),
        ):
            matched = required is None or actual in required
            dims.append(DimensionEvidence(dimension, required, actual, matched))
        return ConditionEvaluation(tuple(dims))

    def to_json(self) -> dict:
        out = {}
        for dimension in ("os", "arch", "stage"):
            value = getattr(self, dimension)
            if value is not None:
                out[dimension] = list(value)
        return out


@dataclass(frozen=True)
class Dependency:
    """A conditional dependency edge.

    kind: "host" (runs on the build host, e.g. a code generator) or
    "target" (compiled for the target). None inherits the parent's domain.
    """

    name: str
    constraint: Constraint = field(default_factory=Constraint.any)
    kind: str | None = None
    condition: Condition = field(default_factory=Condition)

    @classmethod
    def from_json(cls, data: dict) -> "Dependency":
        kind = data.get("kind")
        if kind is not None and kind not in VALID_KINDS:
            raise ValueError(f"invalid dependency kind {kind!r}: want one of {VALID_KINDS}")
        return cls(
            name=data["name"],
            constraint=Constraint.parse(data.get("constraint", "*")),
            kind=kind,
            condition=Condition.from_json(data.get("when")),
        )


@dataclass(frozen=True)
class PackageVersion:
    name: str
    version: Version
    deps: tuple = ()  # tuple of Dependency


class PackageIndex:
    """All known packages, loaded from a local JSON file (never the network)."""

    def __init__(self):
        self._packages: dict[str, dict[Version, PackageVersion]] = {}

    @classmethod
    def from_data(cls, data: dict) -> "PackageIndex":
        index = cls()
        for entry in data.get("packages", []):
            name = entry["name"]
            version = Version.parse(entry["version"])
            deps = tuple(Dependency.from_json(d) for d in entry.get("deps", []))
            bucket = index._packages.setdefault(name, {})
            if version in bucket:
                raise ValueError(f"duplicate package entry: {name} {version}")
            bucket[version] = PackageVersion(name, version, deps)
        return index

    @classmethod
    def from_json(cls, path) -> "PackageIndex":
        with open(path, "r", encoding="utf-8") as handle:
            return cls.from_data(json.load(handle))

    def versions(self, name: str):
        """Sorted (ascending) known versions, or None if the package is unknown."""
        bucket = self._packages.get(name)
        if bucket is None:
            return None
        return sorted(bucket)

    def get(self, name: str, version: Version) -> PackageVersion:
        return self._packages[name][version]


@dataclass(frozen=True)
class RootRequirement:
    name: str
    constraint: Constraint = field(default_factory=Constraint.any)


@dataclass(frozen=True)
class BuildInput:
    """A single build request: target context plus root packages."""

    context: TargetContext
    roots: tuple  # tuple of RootRequirement
    host_os: str | None = None
    host_arch: str | None = None

    @classmethod
    def from_json(cls, path) -> "BuildInput":
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        target = data["target"]
        host = data.get("host", {})
        roots = tuple(
            RootRequirement(r["name"], Constraint.parse(r.get("constraint", "*")))
            for r in data.get("roots", [])
        )
        if not roots:
            raise ValueError(f"{path}: build input needs at least one root package")
        return cls(
            context=TargetContext(os=target["os"], arch=target["arch"], stage=data["stage"]),
            roots=roots,
            host_os=host.get("os"),
            host_arch=host.get("arch"),
        )

    @property
    def target_key(self) -> str:
        return f"target-{self.context.os}-{self.context.arch}"

    @property
    def host_key(self) -> str:
        if self.host_os and self.host_arch:
            return f"host-{self.host_os}-{self.host_arch}"
        return "host"

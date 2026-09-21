"""Domain models: build targets, requirements, packages, and the catalog."""
from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import FrozenSet, Mapping, Optional, Tuple

from .errors import BuildRequestError
from .version import Constraint, Version, parse_constraint

_REQ_RE = re.compile(r"^\s*([A-Za-z0-9_.\-]+)\s*(.*)$")


@dataclass(frozen=True)
class Target:
    """An immutable set of target dimensions, e.g. os=linux, arch=amd64."""

    values: Tuple[Tuple[str, str], ...] = ()

    @classmethod
    def from_mapping(cls, data: Optional[Mapping[str, str]]) -> "Target":
        if data is None:
            return cls(())
        if not isinstance(data, Mapping):
            raise BuildRequestError("target must be an object of string key/values")
        pairs: list[Tuple[str, str]] = []
        for key, value in data.items():
            if not isinstance(key, str) or not isinstance(value, str):
                raise BuildRequestError("target keys and values must be strings")
            pairs.append((key, value))
        return cls(tuple(sorted(pairs)))

    def get(self, dimension: str) -> Optional[str]:
        for key, value in self.values:
            if key == dimension:
                return value
        return None

    def to_dict(self) -> dict:
        return {key: value for key, value in self.values}

    def __str__(self) -> str:
        return ",".join(f"{key}={value}" for key, value in self.values) or "<any>"


@dataclass(frozen=True)
class Requirement:
    """A capability request with zero or more version constraints."""

    capability: str
    constraints: Tuple[Constraint, ...] = ()

    @classmethod
    def parse(cls, spec: object) -> "Requirement":
        if isinstance(spec, str):
            return cls._parse_text(spec)
        if isinstance(spec, Mapping):
            capability = spec.get("capability")
            if not isinstance(capability, str) or not capability:
                raise BuildRequestError("requirement needs a non-empty 'capability'")
            raw_constraints = spec.get("constraints", [])
            if isinstance(raw_constraints, str):
                raw_constraints = [raw_constraints]
            if not isinstance(raw_constraints, (list, tuple)):
                raise BuildRequestError(
                    f"constraints for {capability!r} must be a string or list"
                )
            constraints = tuple(parse_constraint(str(item)) for item in raw_constraints)
            return cls(capability=capability, constraints=constraints)
        raise BuildRequestError(
            f"requirement must be a string or object, got {type(spec).__name__}"
        )

    @staticmethod
    def _parse_text(text: str) -> "Requirement":
        match = _REQ_RE.match(text)
        if match is None or not match.group(1):
            raise BuildRequestError(f"invalid requirement: {text!r}")
        capability = match.group(1)
        remainder = match.group(2).strip()
        constraints = (parse_constraint(remainder),) if remainder else ()
        return Requirement(capability=capability, constraints=constraints)

    def spec(self) -> str:
        if not self.constraints:
            return self.capability
        return ",".join(str(c) for c in self.constraints)


@dataclass(frozen=True)
class Package:
    """A single package version declaring capabilities and constraints."""

    name: str
    version: Version
    provides: FrozenSet[str]
    conflicts_with: FrozenSet[str] = field(default_factory=frozenset)
    requires: Tuple[Target, ...] = ()
    excludes: Tuple[Target, ...] = ()
    replaced_by: Optional[str] = None

    @property
    def id(self) -> str:
        return f"{self.name}@{self.version}"

    def matches_target(self, target: Target) -> bool:
        """All `requires` dimensions match and no `excludes` target matches."""
        for required in self.requires:
            for dimension, wanted in required.values:
                if target.get(dimension) != wanted:
                    return False
        for excluded in self.excludes:
            if all(target.get(dim) == wanted for dim, wanted in excluded.values):
                return False
        return True


@dataclass(frozen=True)
class Catalog:
    """All known package versions indexed for resolution."""

    packages: Tuple[Package, ...]
    by_name: Mapping[str, Tuple[Package, ...]]
    capability_index: Mapping[str, Tuple[Package, ...]]

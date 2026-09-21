"""Loading and structural validation of lock files stored as local JSON.

Lock file schema (all inputs are local JSON files; nothing is fetched):

    {
      "version": 1,
      "roots":        {"<pkg>": "<constraint>", ...},
      "packages":     {"<pkg>": {"version": "x.y.z[-pre]",
                                 "dependencies": {"<pkg>": "<constraint>", ...}}, ...},
      "build_inputs": {"<key>": <scalar>, ...}
    }
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field

from .versions import Constraint, Version, VersionError

LOCK_FORMAT_VERSION = 1


class LockfileError(ValueError):
    """Raised when a lock file is structurally invalid."""


@dataclass
class Lockfile:
    roots: dict = field(default_factory=dict)          # name -> constraint string
    packages: dict = field(default_factory=dict)       # name -> {"version": str, "dependencies": {...}}
    build_inputs: dict = field(default_factory=dict)   # key -> JSON scalar

    def to_dict(self) -> dict:
        return {
            "version": LOCK_FORMAT_VERSION,
            "roots": dict(sorted(self.roots.items())),
            "packages": {
                name: {
                    "version": rec["version"],
                    "dependencies": dict(sorted(rec.get("dependencies", {}).items())),
                }
                for name, rec in sorted(self.packages.items())
            },
            "build_inputs": dict(sorted(self.build_inputs.items())),
        }


def _require_mapping(value, what: str) -> dict:
    if not isinstance(value, dict):
        raise LockfileError(f"{what} must be an object, got {type(value).__name__}")
    return value


def validate_lockfile(data: dict, source: str = "<input>") -> Lockfile:
    """Validate raw parsed JSON and return a normalized Lockfile.

    Raises LockfileError on structural problems and VersionError on bad
    version/constraint strings.
    """
    _require_mapping(data, f"{source}: top level")
    fmt = data.get("version", LOCK_FORMAT_VERSION)
    if fmt != LOCK_FORMAT_VERSION:
        raise LockfileError(f"{source}: unsupported lock format version {fmt!r}")

    roots = _require_mapping(data.get("roots", {}), f"{source}: roots")
    packages = _require_mapping(data.get("packages", {}), f"{source}: packages")
    build_inputs = _require_mapping(data.get("build_inputs", {}), f"{source}: build_inputs")

    for name, constraint in roots.items():
        if not isinstance(name, str) or not name:
            raise LockfileError(f"{source}: root name must be a non-empty string")
        Constraint(constraint)  # raises VersionError if malformed

    norm_packages = {}
    for name, rec in packages.items():
        if not isinstance(name, str) or not name:
            raise LockfileError(f"{source}: package name must be a non-empty string")
        _require_mapping(rec, f"{source}: packages[{name!r}]")
        version_text = rec.get("version")
        if version_text is None:
            raise LockfileError(f"{source}: packages[{name!r}] is missing 'version'")
        Version.parse(version_text)  # raises VersionError if malformed
        deps = _require_mapping(rec.get("dependencies", {}), f"{source}: packages[{name!r}].dependencies")
        for dep_name, dep_constraint in deps.items():
            if not isinstance(dep_name, str) or not dep_name:
                raise LockfileError(f"{source}: dependency name in {name!r} must be a non-empty string")
            Constraint(dep_constraint)
        norm_packages[name] = {"version": version_text, "dependencies": dict(deps)}

    for key, value in build_inputs.items():
        if not isinstance(key, str) or not key:
            raise LockfileError(f"{source}: build input key must be a non-empty string")
        if isinstance(value, (dict, list)):
            raise LockfileError(f"{source}: build input {key!r} must be a scalar value")

    return Lockfile(roots=dict(roots), packages=norm_packages, build_inputs=dict(build_inputs))


def load_lockfile(path: str) -> Lockfile:
    with open(path, "r", encoding="utf-8") as fh:
        try:
            data = json.load(fh)
        except json.JSONDecodeError as exc:
            raise LockfileError(f"{path}: invalid JSON: {exc}") from exc
    return validate_lockfile(data, source=path)

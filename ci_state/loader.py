"""Loaders for local JSON inputs.

Everything (package catalog, build definition, event log) is read from local
JSON files. Package versions are resolved against the local catalog only -
there is deliberately no network access anywhere in this package.

Local file formats
------------------
packages.json::

    {
      "packages": {
        "libcore": {"versions": ["1.2.0", "1.3.0-rc.1", "1.3.0"]}
      }
    }

build_input.json::

    {
      "nodes": [
        {
          "name": "api",
          "packages": {"libcore": "1.3.0-rc.1"},
          "required_artifacts": ["dist/api.tar.gz"],
          "depends_on": ["lib"]
        }
      ]
    }

Package constraints may be:

* an exact version ``"1.3.0-rc.1"``;
* ``"*"`` / ``"latest"`` - highest catalog version (releases naturally
  outrank prereleases via SemVer precedence);
* a comparator set such as ``">=1.2.0 <2.0.0"`` (operators ``> >= < <= == =``);
* npm-style caret/tilde ranges ``"^1.2.0"`` / ``"~1.2.3"``.
  Prerelease catalog entries only satisfy a range when the range itself
  references a prerelease version.
"""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any, Dict, FrozenSet, List, Mapping, Sequence, Tuple

from .events import Event
from .version import Version, VersionError


class InputError(ValueError):
    """Raised when a local JSON input is structurally invalid."""


_COMPARATOR_RE = re.compile(
    r"^\s*(?P<op>>=|<=|==|=|>|<)\s*(?P<ver>\S+)\s*$"
)


def load_json(source: Any) -> Any:
    """Load JSON from a path/str/Path or pass through an already-parsed object."""
    if isinstance(source, (str, Path)):
        with open(source, "r", encoding="utf-8") as handle:
            return json.load(handle)
    return source


class PackageCatalog:
    """Offline package catalog: name -> parsed versions."""

    def __init__(self, packages: Mapping[str, Sequence[str]]) -> None:
        self._versions: Dict[str, List[Version]] = {}
        for name, raw_versions in packages.items():
            if not isinstance(name, str) or not name:
                raise InputError("package names must be non-empty strings")
            if not isinstance(raw_versions, (list, tuple)) or not raw_versions:
                raise InputError(
                    f"package {name!r}: 'versions' must be a non-empty list"
                )
            try:
                parsed = [Version.parse(v) for v in raw_versions]
            except VersionError as exc:
                raise InputError(f"package {name!r}: {exc}") from exc
            if len({str(v) for v in parsed}) != len(parsed):
                raise InputError(f"package {name!r}: duplicate versions in catalog")
            self._versions[name] = parsed

    @classmethod
    def from_data(cls, data: Any) -> "PackageCatalog":
        if not isinstance(data, dict) or "packages" not in data:
            raise InputError("package catalog must be an object with 'packages'")
        packages = data["packages"]
        if not isinstance(packages, dict):
            raise InputError("'packages' must be an object")
        return cls(
            {
                name: (spec.get("versions", []) if isinstance(spec, dict) else spec)
                for name, spec in packages.items()
            }
        )

    def versions(self, name: str) -> List[Version]:
        if name not in self._versions:
            raise InputError(f"unknown package: {name!r} (offline catalog only)")
        return list(self._versions[name])

    def resolve(self, name: str, constraint: str) -> Version:
        """Resolve one local constraint to the highest matching catalog version."""
        if not isinstance(constraint, str) or not constraint.strip():
            raise InputError(f"package {name!r}: constraint must be a non-empty string")
        candidates = self.versions(name)
        text = constraint.strip()

        if text in ("*", "latest"):
            # Prefer releases; a prerelease is "latest" only when the catalog
            # contains nothing else.
            releases = [v for v in candidates if not v.is_prerelease]
            return max(releases or candidates)

        parts = text.split()
        if parts and parts[0][:1] in ("^", "~"):
            if len(parts) != 1:
                raise InputError(
                    f"package {name!r}: caret/tilde range cannot be combined: {text!r}"
                )
            comparators = _parse_caret_tilde(name, parts[0])
        elif not any(sym in text for sym in (">", "<", "=")):
            wanted = _parse_version(name, text)
            if wanted not in candidates:
                raise InputError(
                    f"package {name!r}: exact version {wanted} not in local catalog"
                )
            return wanted
        else:
            comparators = [_parse_comparator(name, part) for part in parts]
        if not comparators:
            raise InputError(f"package {name!r}: empty constraint")
        allow_prerelease = any(ver.is_prerelease for _op, ver in comparators)
        matching = [
            v
            for v in candidates
            if all(_compare(v, op, target) for op, target in comparators)
        ]
        if allow_prerelease:
            # SemVer range rule: a prerelease matches only when a comparator
            # explicitly references a prerelease sharing its [major,minor,patch].
            pre_cores = {
                (target.major, target.minor, target.patch)
                for _op, target in comparators
                if target.is_prerelease
            }
            matching = [
                v
                for v in matching
                if not v.is_prerelease
                or (v.major, v.minor, v.patch) in pre_cores
            ]
        else:
            matching = [v for v in matching if not v.is_prerelease]
        if not matching:
            raise InputError(
                f"package {name!r}: no local version satisfies {constraint!r}"
            )
        return max(matching)


def _parse_version(package: str, text: str) -> Version:
    try:
        return Version.parse(text)
    except VersionError as exc:
        raise InputError(f"package {package!r}: {exc}") from exc


def _parse_caret_tilde(package: str, part: str) -> List[Tuple[str, Version]]:
    """Expand ``^``/``~`` shorthand into [lower-bound, upper-bound] comparators.

    ``^`` pins the left-most non-zero component (``^1.2.3`` -> ``>=1.2.3
    <2.0.0``; ``^0.2.3`` -> ``>=0.2.3 <0.3.0``; ``^0.0.3`` -> ``==0.0.3``).
    ``~`` allows only patch-level changes (``~1.2.3`` -> ``>=1.2.3 <1.3.0``).
    """
    symbol, raw = part[0], part[1:]
    lower = _parse_version(package, raw)
    if symbol == "~":
        upper = Version(lower.major, lower.minor + 1, 0)
    elif lower.major > 0:
        upper = Version(lower.major + 1, 0, 0)
    elif lower.minor > 0:
        upper = Version(0, lower.minor + 1, 0)
    else:
        return [("==", lower)]
    return [(">=", lower), ("<", upper)]


def _parse_comparator(package: str, part: str) -> Tuple[str, Version]:
    match = _COMPARATOR_RE.match(part)
    if match is None:
        raise InputError(f"package {package!r}: cannot parse constraint part {part!r}")
    op = "==" if match.group("op") == "=" else match.group("op")
    return op, _parse_version(package, match.group("ver"))


def _compare(version: Version, op: str, target: Version) -> bool:
    if op == ">":
        return version > target
    if op == ">=":
        return version >= target
    if op == "<":
        return version < target
    if op == "<=":
        return version <= target
    return version == target


def load_packages(source: Any) -> PackageCatalog:
    return PackageCatalog.from_data(load_json(source))


def load_build_input(
    source: Any, catalog: PackageCatalog
) -> Tuple[Dict[str, FrozenSet[str]], List[dict]]:
    """Parse the build definition and resolve packages locally.

    Returns ``(nodes, resolved)`` where ``nodes`` maps node name to required
    artifact names (the engine's input) and ``resolved`` records the package
    version chosen for each node, for reporting.
    """
    data = load_json(source)
    if not isinstance(data, dict) or not isinstance(data.get("nodes"), list):
        raise InputError("build input must be an object with a 'nodes' list")
    nodes: Dict[str, FrozenSet[str]] = {}
    resolved: List[dict] = []
    seen_names: set = set()
    for index, node in enumerate(data["nodes"]):
        if not isinstance(node, dict) or "name" not in node:
            raise InputError(f"node #{index}: must be an object with 'name'")
        name = node["name"]
        if not isinstance(name, str) or not name.strip():
            raise InputError(f"node #{index}: 'name' must be a non-empty string")
        if name in seen_names:
            raise InputError(f"duplicate node name: {name!r}")
        seen_names.add(name)
        artifacts = node.get("required_artifacts", [])
        if not isinstance(artifacts, list) or not all(
            isinstance(a, str) and a.strip() for a in artifacts
        ):
            raise InputError(f"node {name!r}: required_artifacts must be strings")
        if len(set(artifacts)) != len(artifacts):
            raise InputError(f"node {name!r}: duplicate required artifacts")
        packages = node.get("packages", {})
        if not isinstance(packages, dict):
            raise InputError(f"node {name!r}: 'packages' must be an object")
        picked = {
            pkg: str(catalog.resolve(pkg, constraint))
            for pkg, constraint in packages.items()
        }
        depends_on = node.get("depends_on", [])
        if not isinstance(depends_on, list) or not all(
            isinstance(d, str) for d in depends_on
        ):
            raise InputError(f"node {name!r}: depends_on must be a list of strings")
        nodes[name] = frozenset(artifacts)
        resolved.append(
            {
                "name": name,
                "packages": picked,
                "depends_on": list(depends_on),
            }
        )
    for entry in resolved:
        for dep in entry["depends_on"]:
            if dep not in nodes:
                raise InputError(
                    f"node {entry['name']!r}: depends_on unknown node {dep!r}"
                )
            if dep == entry["name"]:
                raise InputError(f"node {entry['name']!r}: node depends on itself")
    return nodes, resolved


def load_events(source: Any) -> List[Event]:
    """Parse and validate a local event log (a JSON list of event objects)."""
    data = load_json(source)
    if not isinstance(data, list):
        raise InputError("event log must be a JSON array of event objects")
    events: List[Event] = []
    for index, raw in enumerate(data):
        try:
            events.append(Event.from_dict(raw))
        except Exception as exc:
            raise InputError(f"event #{index}: {exc}") from exc
    return events

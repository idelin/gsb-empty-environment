"""Local build manifests: packages and build nodes loaded from a JSON file.

No network access is performed (and none is needed): package identity and
versions come solely from the JSON document supplied to :func:`load_manifest`.

Expected document shape::

    {
      "packages": [
        {"name": "core", "version": "2.4.1"},
        {"name": "beta-lib", "version": "3.0.0", "prerelease": "rc.2"}
      ],
      "nodes": [
        {
          "name": "compile-core",
          "package": "core",
          "version": "2.4.1",
          "inputs": {"flags": ["-O2"], "deps": {"beta-lib": "3.0.0-rc.2"}},
          "outputs": ["core.o", "core.map"]
        }
      ]
    }
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Mapping, Optional, Sequence, Union

from .digest import build_key, input_digest
from .version import InvalidVersion, Version


class ManifestError(ValueError):
    """Raised when the local JSON manifest is malformed."""


@dataclass(frozen=True)
class Package:
    name: str
    version: Version
    prerelease: Optional[str] = None

    @property
    def version_str(self) -> str:
        return str(self.version)


@dataclass(frozen=True)
class NodeSpec:
    name: str
    package: str
    version: Version
    inputs: Any
    outputs: tuple[str, ...] = field(default_factory=tuple)

    @property
    def cache_key(self) -> str:
        return build_key(self.package, str(self.version), self.inputs, self.outputs)

    @property
    def input_digest(self) -> str:
        return input_digest(self.inputs)


@dataclass(frozen=True)
class Manifest:
    packages: Mapping[str, Package]
    nodes: Mapping[str, NodeSpec]
    source: Optional[Path] = None


def _as_str(value: Any, what: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ManifestError(f"{what} must be a non-empty string")
    return value.strip()


def _check_prerelease(version: Version, prerelease: Any) -> None:
    """Validate the optional manifest ``prerelease`` field against version."""
    if prerelease is None:
        return
    if isinstance(prerelease, bool):
        if prerelease and not version.is_prerelease:
            raise ManifestError(
                f"prerelease is true but version {version} has no prerelease tag"
            )
        return
    if isinstance(prerelease, str):
        tag = prerelease.strip()
        if not tag:
            raise ManifestError("prerelease tag must be a non-empty string")
        if version.prerelease != tag:
            raise ManifestError(
                f"prerelease field {tag!r} does not match version tag {version.prerelease!r}"
            )
        return
    raise ManifestError("prerelease must be a boolean or a string tag")


def _parse_package(raw: Any) -> Package:
    if not isinstance(raw, Mapping):
        raise ManifestError("each package must be an object")
    name = _as_str(raw.get("name"), "package name")
    if "version" not in raw:
        raise ManifestError(f"package {name!r} requires a version")
    try:
        version = Version.parse(raw["version"])
    except InvalidVersion as exc:
        raise ManifestError(f"package {name!r}: {exc}") from exc
    _check_prerelease(version, raw.get("prerelease"))
    return Package(name=name, version=version, prerelease=version.prerelease)


def _parse_node(raw: Any, packages: Mapping[str, Package]) -> NodeSpec:
    if not isinstance(raw, Mapping):
        raise ManifestError("each node must be an object")
    name = _as_str(raw.get("name"), "node name")
    package_name = _as_str(raw.get("package"), f"node {name!r} package")
    if package_name not in packages:
        raise ManifestError(
            f"node {name!r} references unknown package {package_name!r}; "
            "packages must be declared in the local manifest"
        )
    package = packages[package_name]
    declared_version = raw.get("version", package.version_str)
    try:
        version = Version.parse(declared_version)
    except InvalidVersion as exc:
        raise ManifestError(f"node {name!r}: {exc}") from exc
    if version != package.version:
        raise ManifestError(
            f"node {name!r} version {version} does not match declared package "
            f"{package_name!r} version {package.version}"
        )
    _check_prerelease(version, raw.get("prerelease"))
    outputs_raw = raw.get("outputs", ())
    if isinstance(outputs_raw, str) or not isinstance(outputs_raw, Sequence):
        raise ManifestError(f"node {name!r} outputs must be a list of strings")
    outputs = tuple(_as_str(item, f"node {name!r} output") for item in outputs_raw)
    if len(set(outputs)) != len(outputs):
        raise ManifestError(f"node {name!r} outputs must be unique")
    if "inputs" in raw and not isinstance(raw["inputs"], (Mapping, list, int, float, str, bool)):
        raise ManifestError(f"node {name!r} inputs must be JSON-serializable")
    inputs = raw.get("inputs", {})
    return NodeSpec(
        name=name, package=package_name, version=version, inputs=inputs, outputs=outputs
    )


def parse_manifest(
    data: Any, source: Optional[Union[str, Path]] = None
) -> Manifest:
    """Parse an already-decoded JSON document into a :class:`Manifest`."""
    if not isinstance(data, Mapping):
        raise ManifestError("manifest root must be a JSON object")
    packages_raw = data.get("packages", [])
    if not isinstance(packages_raw, Sequence) or isinstance(packages_raw, str):
        raise ManifestError("'packages' must be a list")
    packages: dict[str, Package] = {}
    for raw in packages_raw:
        package = _parse_package(raw)
        if package.name in packages:
            raise ManifestError(f"duplicate package {package.name!r}")
        packages[package.name] = package
    if not packages:
        raise ManifestError("manifest must declare at least one package")

    nodes_raw = data.get("nodes", [])
    if not isinstance(nodes_raw, Sequence) or isinstance(nodes_raw, str):
        raise ManifestError("'nodes' must be a list")
    nodes: dict[str, NodeSpec] = {}
    for raw in nodes_raw:
        node = _parse_node(raw, packages)
        if node.name in nodes:
            raise ManifestError(f"duplicate node {node.name!r}")
        nodes[node.name] = node
    return Manifest(packages=packages, nodes=nodes, source=Path(source) if source else None)


def load_manifest(path: Union[str, Path]) -> Manifest:
    """Load a manifest from a local JSON file. Never accesses the network."""
    path = Path(path)
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise ManifestError(f"cannot read manifest {path}: {exc}") from exc
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        raise ManifestError(f"invalid JSON in manifest {path}: {exc}") from exc
    return parse_manifest(data, source=path)

"""Build-definition model loaded from local JSON.

The definition declares available packages (with exact local versions) and
build nodes.  Each node lists its package inputs and the artifacts that must
be evidenced before a ``submitted`` event can count as success.  No package
metadata is fetched remotely.
"""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path

from .version import Version, VersionError


class DefinitionError(ValueError):
    """Raised when the build-definition JSON is malformed."""


@dataclass(frozen=True)
class Package:
    name: str
    version: Version


@dataclass(frozen=True)
class InputRef:
    package: str
    version: Version


@dataclass(frozen=True)
class NodeSpec:
    name: str
    inputs: tuple[InputRef, ...]
    required_artifacts: tuple[str, ...]


@dataclass(frozen=True)
class BuildDefinition:
    packages: tuple[Package, ...]
    nodes: tuple[NodeSpec, ...]

    @property
    def package_map(self) -> dict[str, Package]:
        return {package.name: package for package in self.packages}

    @property
    def node_map(self) -> dict[str, NodeSpec]:
        return {node.name: node for node in self.nodes}


def _require_mapping(value: object, context: str) -> Mapping[str, object]:
    if not isinstance(value, Mapping):
        raise DefinitionError(f"{context} must be a JSON object")
    return value


def _require_list(value: object, context: str) -> Sequence[object]:
    if not isinstance(value, Sequence) or isinstance(value, (str, bytes)):
        raise DefinitionError(f"{context} must be a JSON array")
    return value


def _parse_packages(raw: object) -> tuple[Package, ...]:
    data = _require_mapping(raw, "packages")
    packages = []
    seen = set()
    for name, item in data.items():
        if not isinstance(name, str) or not name:
            raise DefinitionError("package names must be non-empty strings")
        if name in seen:
            raise DefinitionError(f"duplicate package {name!r}")
        seen.add(name)
        package = _require_mapping(item, f"package {name!r}")
        if set(package) - {"version"}:
            raise DefinitionError(f"package {name!r} has unknown fields")
        if "version" not in package:
            raise DefinitionError(f"package {name!r} is missing 'version'")
        try:
            version = Version.parse(package["version"])
        except VersionError as exc:
            raise DefinitionError(f"package {name!r}: {exc}") from None
        packages.append(Package(name=name, version=version))
    return tuple(packages)


def _parse_node(name: object, raw: object, packages: Mapping[str, Package]) -> NodeSpec:
    if not isinstance(name, str) or not name:
        raise DefinitionError("node names must be non-empty strings")
    data = _require_mapping(raw, f"node {name!r}")
    if set(data) - {"inputs", "requires_artifacts"}:
        raise DefinitionError(f"node {name!r} has unknown fields")

    inputs = []
    for index, item in enumerate(_require_list(data.get("inputs", []), f"node {name!r} inputs")):
        entry = _require_mapping(item, f"node {name!r} input #{index}")
        if set(entry) != {"package", "version"}:
            raise DefinitionError(
                f"node {name!r} input #{index} must have only package and version"
            )
        package_name, wanted = entry["package"], entry["version"]
        if not isinstance(package_name, str) or not package_name:
            raise DefinitionError(f"node {name!r} input #{index} has a bad package name")
        if package_name not in packages:
            raise DefinitionError(
                f"node {name!r} input #{index} references undeclared package "
                f"{package_name!r}"
            )
        try:
            wanted_version = Version.parse(wanted)
        except VersionError as exc:
            raise DefinitionError(f"node {name!r} input #{index}: {exc}") from None
        declared = packages[package_name].version
        if wanted_version != declared:
            raise DefinitionError(
                f"node {name!r} input {package_name} requests {wanted_version} "
                f"but locally declared version is {declared}"
            )
        inputs.append(InputRef(package=package_name, version=wanted_version))

    artifacts = []
    raw_artifacts = data.get("requires_artifacts", [])
    for index, item in enumerate(_require_list(raw_artifacts, f"node {name!r} requires_artifacts")):
        if not isinstance(item, str) or not item:
            raise DefinitionError(f"node {name!r} artifact #{index} must be a string")
        if item in artifacts:
            raise DefinitionError(f"node {name!r} lists artifact {item!r} twice")
        artifacts.append(item)
    if not artifacts:
        raise DefinitionError(f"node {name!r} must require at least one artifact")

    return NodeSpec(
        name=name,
        inputs=tuple(inputs),
        required_artifacts=tuple(artifacts),
    )


def parse_definition(raw: object) -> BuildDefinition:
    data = _require_mapping(raw, "build definition")
    if set(data) != {"packages", "nodes"}:
        raise DefinitionError("build definition must have only packages and nodes")
    packages = _parse_packages(data.get("packages"))
    package_map = {package.name: package for package in packages}

    nodes_data = _require_mapping(data.get("nodes", {}), "nodes")
    nodes = []
    seen = set()
    for name, raw_node in nodes_data.items():
        if name in seen:
            raise DefinitionError(f"duplicate node {name!r}")
        seen.add(name)
        nodes.append(_parse_node(name, raw_node, package_map))
    if not nodes:
        raise DefinitionError("build definition must declare at least one node")
    return BuildDefinition(packages=packages, nodes=tuple(nodes))


def load_definition(path: str | Path) -> BuildDefinition:
    source = Path(path)
    try:
        raw = json.loads(source.read_text(encoding="utf-8"))
    except FileNotFoundError:
        raise DefinitionError(f"definition file not found: {source}") from None
    except json.JSONDecodeError as exc:
        raise DefinitionError(f"definition file {source} is not valid JSON: {exc}") from None
    return parse_definition(raw)

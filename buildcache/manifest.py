"""Local JSON manifests: packages and build plans. No network access."""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from pathlib import Path

from .version import Version


@dataclass(frozen=True)
class Package:
    name: str
    version: Version


@dataclass(frozen=True)
class OutputSpec:
    path: str
    sha256: str


@dataclass(frozen=True)
class Node:
    id: str
    package: Package
    inputs: tuple[str, ...]
    outputs: tuple[OutputSpec, ...]

    @property
    def key(self) -> str:
        identity = {
            "id": self.id,
            "package": self.package.name,
            "version": str(self.package.version),
            "inputs": list(self.inputs),
        }
        blob = json.dumps(identity, sort_keys=True).encode("utf-8")
        return hashlib.sha256(blob).hexdigest()


def load_packages(path: str | Path) -> dict[str, Package]:
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    packages: dict[str, Package] = {}
    for item in raw.get("packages", []):
        name = item["name"]
        if name in packages:
            raise ValueError(f"duplicate package: {name}")
        packages[name] = Package(name=name, version=Version.parse(item["version"]))
    return packages


def load_plan(path: str | Path, packages: dict[str, Package]) -> list[Node]:
    raw = json.loads(Path(path).read_text(encoding="utf-8"))
    nodes = []
    seen_keys = set()
    for item in raw.get("nodes", []):
        package_name = item["package"]
        if package_name not in packages:
            raise ValueError(f"unknown package: {package_name}")
        outputs = tuple(_parse_output(o) for o in item["outputs"])
        if not outputs:
            raise ValueError(f"node {item['id']} declares no outputs")
        node = Node(
            id=item["id"],
            package=packages[package_name],
            inputs=tuple(item.get("inputs", [])),
            outputs=outputs,
        )
        if node.key in seen_keys:
            raise ValueError(f"duplicate node identity: {node.id}")
        seen_keys.add(node.key)
        nodes.append(node)
    return nodes


def _parse_output(item: dict) -> OutputSpec:
    digest = item["sha256"]
    if len(digest) != 64 or any(c not in "0123456789abcdef" for c in digest):
        raise ValueError(f"invalid sha256 for output {item['path']}")
    return OutputSpec(path=item["path"], sha256=digest)


def output_content(node_id: str, inputs: tuple[str, ...], output_path: str) -> bytes:
    digest = hashlib.sha256()
    digest.update(node_id.encode("utf-8"))
    for item in inputs:
        digest.update(b"\0")
        digest.update(item.encode("utf-8"))
    digest.update(b"\0")
    digest.update(output_path.encode("utf-8"))
    return digest.hexdigest().encode("ascii")


def expected_sha256(node_id: str, inputs: tuple[str, ...], output_path: str) -> str:
    return hashlib.sha256(output_content(node_id, inputs, output_path)).hexdigest()

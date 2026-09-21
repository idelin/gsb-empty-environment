"""Local JSON manifest: packages and build-input declarations.

No network access happens anywhere in this module; the manifest is read
from a local file or from an already-parsed mapping.

Schema::

    {
      "packages": [{"name": "...", "version": "1.2.3-beta.1"}],
      "nodes": [
        {
          "key": "//app/image",
          "package": "app",
          "inputs": {"SRC": "..."},
          "outputs": [
            {"path": "out/a.bin", "template": "v={VERSION}",
             "encoding": "utf-8", "sha256": "optional"}
          ]
        }
      ]
    }
"""

from __future__ import annotations

import base64
import json
from dataclasses import dataclass, field
from pathlib import Path

from .errors import ManifestError
from .version import InvalidVersion, Version

_ENCODINGS = ("utf-8", "base64")


@dataclass(frozen=True, slots=True)
class Package:
    name: str
    version: Version

    @property
    def is_prerelease(self) -> bool:
        return self.version.is_prerelease


@dataclass(slots=True)
class OutputContent:
    """Materialized bytes for one declared output."""

    path: str
    data: bytes


@dataclass(frozen=True, slots=True)
class BuildOutput:
    path: str
    template: str
    encoding: str = "utf-8"
    sha256: str | None = None  # declared expected digest, optional

    def render(self, variables: dict[str, str]) -> bytes:
        rendered = self.template
        for name, value in variables.items():
            rendered = rendered.replace("{" + name + "}", value)
        if self.encoding == "utf-8":
            return rendered.encode("utf-8")
        # base64: the template carries encoded payload; whitespace is tolerated.
        return base64.b64decode("".join(rendered.split()), validate=True)


@dataclass(frozen=True, slots=True)
class BuildNode:
    key: str
    package: str
    inputs: dict[str, str] = field(default_factory=dict)
    outputs: tuple[BuildOutput, ...] = ()
    behavior: str = "deterministic"

    @property
    def declared_digests(self) -> dict[str, str]:
        return {o.path: o.sha256 for o in self.outputs if o.sha256}


@dataclass(frozen=True, slots=True)
class Manifest:
    packages: dict[str, Package]
    nodes: dict[str, BuildNode]

    def package_for(self, node: BuildNode) -> Package:
        try:
            return self.packages[node.package]
        except KeyError:
            raise ManifestError(f"node {node.key!r} references unknown package {node.package!r}") from None


def _require_str(value: object, where: str) -> str:
    if not isinstance(value, str) or not value:
        raise ManifestError(f"{where} must be a non-empty string")
    return value


def _parse_package(raw: object) -> Package:
    if not isinstance(raw, dict):
        raise ManifestError("each package must be an object")
    name = _require_str(raw.get("name"), "package.name")
    raw_version = raw.get("version")
    if isinstance(raw_version, dict):
        core = _require_str(raw_version.get("core"), "package.version.core")
        pre = raw_version.get("prerelease")
        text = core if not pre else f"{core}-{_require_str(pre, 'package.version.prerelease')}"
    else:
        text = _require_str(raw_version, "package.version")
    try:
        version = Version.parse(text)
    except InvalidVersion as exc:
        raise ManifestError(str(exc)) from None
    return Package(name=name, version=version)


def _parse_output(raw: object) -> BuildOutput:
    if not isinstance(raw, dict):
        raise ManifestError("each output must be an object")
    path = _require_str(raw.get("path"), "output.path")
    if Path(path).is_absolute() or ".." in Path(path).parts:
        raise ManifestError(f"output.path must be a safe relative path, got {path!r}")
    template = raw.get("template", "")
    if not isinstance(template, str):
        raise ManifestError("output.template must be a string")
    encoding = raw.get("encoding", "utf-8")
    if encoding not in _ENCODINGS:
        raise ManifestError(f"output.encoding must be one of {_ENCODINGS}, got {encoding!r}")
    sha = raw.get("sha256")
    if sha is not None:
        sha = _require_str(sha, "output.sha256")
        if len(sha) != 64 or any(c not in "0123456789abcdef" for c in sha.lower()):
            raise ManifestError(f"output.sha256 must be 64 hex chars, got {sha!r}")
    return BuildOutput(path=path, template=template, encoding=encoding, sha256=(sha.lower() if sha else None))


def _parse_node(raw: object, package_names: set[str]) -> BuildNode:
    if not isinstance(raw, dict):
        raise ManifestError("each node must be an object")
    key = _require_str(raw.get("key"), "node.key")
    package = _require_str(raw.get("package"), "node.package")
    if package not in package_names:
        raise ManifestError(f"node {key!r} references unknown package {package!r}")
    raw_inputs = raw.get("inputs", {})
    if not isinstance(raw_inputs, dict) or not all(
        isinstance(k, str) and isinstance(v, str) for k, v in raw_inputs.items()
    ):
        raise ManifestError(f"node {key!r}: inputs must be an object of string->string")
    raw_outputs = raw.get("outputs", [])
    if not isinstance(raw_outputs, list) or not raw_outputs:
        raise ManifestError(f"node {key!r}: outputs must be a non-empty list")
    outputs = tuple(_parse_output(item) for item in raw_outputs)
    paths = [o.path for o in outputs]
    if len(set(paths)) != len(paths):
        raise ManifestError(f"node {key!r}: duplicate output paths")
    behavior = raw.get("behavior", "deterministic")
    if behavior not in ("deterministic", "nondeterministic", "missing_output", "bad_digest"):
        raise ManifestError(f"node {key!r}: unknown behavior {behavior!r}")
    return BuildNode(
        key=key,
        package=package,
        inputs=dict(raw_inputs),
        outputs=outputs,
        behavior=behavior,
    )


def parse_manifest(data: object) -> Manifest:
    if not isinstance(data, dict):
        raise ManifestError("manifest root must be an object")
    raw_packages = data.get("packages", [])
    if not isinstance(raw_packages, list) or not raw_packages:
        raise ManifestError("manifest requires a non-empty 'packages' list")
    packages: dict[str, Package] = {}
    for raw in raw_packages:
        pkg = _parse_package(raw)
        if pkg.name in packages:
            raise ManifestError(f"duplicate package name {pkg.name!r}")
        packages[pkg.name] = pkg
    raw_nodes = data.get("nodes", [])
    if not isinstance(raw_nodes, list) or not raw_nodes:
        raise ManifestError("manifest requires a non-empty 'nodes' list")
    nodes: dict[str, BuildNode] = {}
    for raw in raw_nodes:
        node = _parse_node(raw, set(packages))
        if node.key in nodes:
            raise ManifestError(f"duplicate node key {node.key!r}")
        nodes[node.key] = node
    return Manifest(packages=packages, nodes=nodes)


def load_manifest(path: str | Path) -> Manifest:
    """Load and validate a manifest from a local JSON file."""
    path = Path(path)
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        raise ManifestError(f"manifest not found: {path}") from None
    except json.JSONDecodeError as exc:
        raise ManifestError(f"invalid JSON in {path}: {exc}") from None
    return parse_manifest(data)

"""Data model for exported symbols, package releases and build graphs."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Dict, FrozenSet, List, Optional, Sequence, Tuple

from .version import Version

KIND_FUNCTION = "function"
KIND_CLASS = "class"
KIND_CONSTANT = "constant"
VALID_KINDS = (KIND_FUNCTION, KIND_CONSTANT, KIND_CLASS)


@dataclass(frozen=True)
class Param:
    name: str
    type: str = "object"
    required: bool = True


@dataclass(frozen=True)
class Symbol:
    package: str
    name: str
    kind: str
    params: Tuple[Param, ...] = ()
    return_type: Optional[str] = None
    value_type: Optional[str] = None
    private: Optional[bool] = None
    markers: FrozenSet[str] = frozenset()

    @property
    def is_private(self) -> bool:
        if self.private is not None:
            return self.private
        return self.name.startswith("_")

    @staticmethod
    def from_json(package: str, name: str, data: dict) -> "Symbol":
        if not isinstance(name, str) or not name:
            raise ValueError(f"{package}: symbol names must be non-empty strings")
        kind = data.get("kind")
        if kind not in VALID_KINDS:
            raise ValueError(
                f"{package}.{name}: kind must be one of {', '.join(VALID_KINDS)}"
            )
        params: List[Param] = []
        for raw in data.get("params", []) or []:
            if isinstance(raw, str):
                params.append(Param(name=raw))
            elif isinstance(raw, dict):
                if "name" not in raw:
                    raise ValueError(f"{package}.{name}: parameter needs a name")
                params.append(
                    Param(
                        name=str(raw["name"]),
                        type=str(raw.get("type", "object")),
                        required=bool(raw.get("required", True)),
                    )
                )
            else:
                raise ValueError(f"{package}.{name}: invalid parameter entry {raw!r}")
        markers = frozenset(str(m) for m in data.get("markers", []) or [])
        if kind == KIND_CONSTANT:
            value_type = data.get("value_type", "object")
            return_type = None
            params_tuple: Tuple[Param, ...] = ()
        else:
            value_type = None
            return_type = data.get("return_type", "object")
            params_tuple = tuple(params)
        return Symbol(
            package=package,
            name=name,
            kind=kind,
            params=params_tuple,
            return_type=return_type,
            value_type=value_type,
            private=data.get("private"),
            markers=markers,
        )


@dataclass(frozen=True)
class Release:
    version: Version
    symbols: Dict[str, Symbol]

    @staticmethod
    def from_json(package: str, version_text: str, data: dict) -> "Release":
        raw_symbols = data.get("symbols", {}) or {}
        if not isinstance(raw_symbols, dict):
            raise ValueError(f"{package} {version_text}: symbols must be an object")
        symbols: Dict[str, Symbol] = {}
        for name, raw_symbol in raw_symbols.items():
            symbol = Symbol.from_json(package, name, raw_symbol or {})
            if name in symbols:
                raise ValueError(f"{package} {version_text}: duplicate symbol {name}")
            symbols[name] = symbol
        return Release(version=Version.parse(version_text), symbols=symbols)


@dataclass(frozen=True)
class Package:
    name: str
    releases: Dict[str, Release]

    @staticmethod
    def from_json(name: str, data: dict) -> "Package":
        raw_releases = data.get("releases", {}) or {}
        if not isinstance(raw_releases, dict):
            raise ValueError(f"package {name}: releases must be an object")
        releases = {
            version_text: Release.from_json(name, version_text, release_data or {})
            for version_text, release_data in raw_releases.items()
        }
        if not releases:
            raise ValueError(f"package {name}: at least one release is required")
        return Package(name=name, releases=releases)

    def release(self, version: str) -> Release:
        try:
            return self.releases[version]
        except KeyError:
            known = ", ".join(sorted(self.releases))
            raise ValueError(
                f"package {self.name}: unknown version {version!r} (have {known})"
            ) from None


@dataclass(frozen=True)
class Catalog:
    packages: Dict[str, Package]
    type_parents: Dict[str, Tuple[str, ...]] = field(default_factory=dict)

    @staticmethod
    def from_json(data: dict) -> "Catalog":
        raw_packages = data.get("packages", {}) or {}
        if not isinstance(raw_packages, dict):
            raise ValueError("catalog: 'packages' must be an object")
        packages = {
            name: Package.from_json(name, package_data or {})
            for name, package_data in raw_packages.items()
        }
        type_parents = {
            str(name): tuple(str(p) for p in parents)
            for name, parents in (data.get("type_parents", {}) or {}).items()
        }
        return Catalog(packages=packages, type_parents=type_parents)

    @staticmethod
    def load(path: str) -> "Catalog":
        with open(path, "r", encoding="utf-8") as handle:
            return Catalog.from_json(json.load(handle))


@dataclass(frozen=True)
class CallSite:
    file: str
    line: int
    note: str = ""

    @staticmethod
    def from_json(data: dict) -> "CallSite":
        if "file" not in data or "line" not in data:
            raise ValueError("call sites need 'file' and 'line'")
        return CallSite(
            file=str(data["file"]),
            line=int(data["line"]),
            note=str(data.get("note", "")),
        )


@dataclass(frozen=True)
class Edge:
    src_package: str
    src_symbol: str
    dst_package: str
    dst_symbol: str
    callsites: Tuple[CallSite, ...] = ()

    @staticmethod
    def from_json(data: dict) -> "Edge":
        try:
            src_package, src_symbol = str(data["from"]).rsplit(".", 1)
            dst_package, dst_symbol = str(data["to"]).rsplit(".", 1)
        except (KeyError, AttributeError, ValueError):
            raise ValueError(
                f"edge needs dotted 'from' and 'to' like 'pkg.symbol': {data!r}"
            ) from None
        sites = tuple(
            CallSite.from_json(site) for site in data.get("callsites", []) or []
        )
        return Edge(
            src_package=src_package,
            src_symbol=src_symbol,
            dst_package=dst_package,
            dst_symbol=dst_symbol,
            callsites=sites,
        )


@dataclass(frozen=True)
class NodeVersion:
    package: str
    old_version: str
    new_version: str


@dataclass(frozen=True)
class BuildInput:
    nodes: Tuple[NodeVersion, ...]
    edges: Tuple[Edge, ...]
    roots: Optional[FrozenSet[str]]

    @staticmethod
    def from_json(data: dict) -> "BuildInput":
        raw_nodes = data.get("nodes", {})
        if isinstance(raw_nodes, dict):
            node_items: Sequence[tuple] = [
                (name, spec) for name, spec in raw_nodes.items()
            ]
        elif isinstance(raw_nodes, list):
            node_items = []
            for entry in raw_nodes:
                if not isinstance(entry, dict) or "package" not in entry:
                    raise ValueError(f"invalid node entry: {entry!r}")
                node_items.append(
                    (
                        str(entry["package"]),
                        {
                            "old": entry.get("old"),
                            "new": entry.get("new"),
                        },
                    )
                )
        else:
            raise ValueError("build input: 'nodes' must be an object or a list")

        nodes: List[NodeVersion] = []
        seen = set()
        for name, spec in node_items:
            if name in seen:
                raise ValueError(f"build input: duplicate node {name}")
            seen.add(name)
            spec = spec or {}
            if "old" not in spec or "new" not in spec:
                raise ValueError(f"node {name}: needs 'old' and 'new' versions")
            nodes.append(
                NodeVersion(
                    package=name,
                    old_version=str(spec["old"]),
                    new_version=str(spec["new"]),
                )
            )

        raw_edges = data.get("edges", []) or []
        if not isinstance(raw_edges, list):
            raise ValueError("build input: 'edges' must be a list")
        edges = tuple(Edge.from_json(entry) for entry in raw_edges)

        raw_roots = data.get("roots")
        roots = None if raw_roots is None else frozenset(str(r) for r in raw_roots)
        return BuildInput(nodes=tuple(nodes), edges=edges, roots=roots)

    @staticmethod
    def load(path: str) -> "BuildInput":
        with open(path, "r", encoding="utf-8") as handle:
            return BuildInput.from_json(json.load(handle))

    def node(self, package: str) -> NodeVersion:
        for node in self.nodes:
            if node.package == package:
                return node
        raise KeyError(package)

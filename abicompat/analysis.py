"""Propagate breaking changes backward along call edges to root packages.

A breaking change affects a root package only when at least one root
symbol can reach the changed symbol through modeled call edges.  Paths
are simple (cycles are cut off) and capped to keep reports finite.
Changes that no root can reach are reported separately; per the task
rules, an uncalled removed symbol never impacts a root.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Dict, List, Optional, Set, Tuple

from .diff import BreakingChange
from .model import BuildInput, CallSite, Catalog, NodeVersion, Release
from .typesys import TypeLattice

MAX_PATHS_PER_CHANGE = 20


@dataclass(frozen=True)
class Hop:
    src_package: str
    src_symbol: str
    dst_package: str
    dst_symbol: str
    callsites: Tuple[CallSite, ...]


@dataclass(frozen=True)
class SymbolRef:
    package: str
    symbol: str


@dataclass
class Finding:
    root: str
    change: BreakingChange
    paths: List[List[Hop]]
    affected_callsites: List[CallSite] = field(default_factory=list)
    truncated_paths: int = 0


@dataclass
class AnalysisResult:
    findings: List[Finding] = field(default_factory=list)
    unreached: List[BreakingChange] = field(default_factory=list)
    roots: List[str] = field(default_factory=list)


class ResolvedBuild:
    """Build graph joined against the catalog releases actually resolved."""

    def __init__(self, catalog: Catalog, build: BuildInput):
        self.catalog = catalog
        self.build = build
        self.node_map: Dict[str, NodeVersion] = {
            node.package: node for node in build.nodes
        }
        self.old_releases: Dict[str, Release] = {}
        self.new_releases: Dict[str, Release] = {}
        for node in build.nodes:
            if node.package not in catalog.packages:
                raise ValueError(
                    f"build node {node.package!r} is missing from package catalog"
                )
            package = catalog.packages[node.package]
            self.old_releases[node.package] = package.release(node.old_version)
            self.new_releases[node.package] = package.release(node.new_version)

        explicit = build.roots
        if explicit is not None:
            missing = explicit - set(self.node_map)
            if missing:
                raise ValueError(
                    f"explicit roots not present in build nodes: {sorted(missing)}"
                )
            self.roots = sorted(explicit)
        else:
            targeted = {edge.dst_package for edge in build.edges}
            self.roots = sorted(
                node.package
                for node in build.nodes
                if node.package not in targeted
            )
        self.root_set = set(self.roots)

        self.incoming: Dict[SymbolRef, List[Hop]] = {}
        self._index_edges()

    def _is_public(self, package: str, symbol: str, release: Release) -> bool:
        exported = release.symbols.get(symbol)
        return exported is not None and not exported.is_private

    def _index_edges(self) -> None:
        node_packages = set(self.node_map)
        for edge in self.build.edges:
            if edge.src_package not in node_packages:
                raise ValueError(
                    f"edge source package {edge.src_package!r} is not a build node"
                )
            if edge.dst_package not in node_packages:
                raise ValueError(
                    f"edge target package {edge.dst_package!r} is not a build node"
                )
            caller = self.new_releases[edge.src_package]
            callee_old = self.old_releases[edge.dst_package]
            if not self._is_public(edge.src_package, edge.src_symbol, caller):
                # Calls from private symbols are not public dependency usage.
                continue
            if not self._is_public(edge.dst_package, edge.dst_symbol, callee_old):
                continue
            target_ref = SymbolRef(edge.dst_package, edge.dst_symbol)
            existing = None
            for queued in self.incoming.setdefault(target_ref, []):
                if (
                    queued.src_package == edge.src_package
                    and queued.src_symbol == edge.src_symbol
                ):
                    existing = queued
                    break
            if existing is None:
                self.incoming[target_ref].append(
                    Hop(
                        src_package=edge.src_package,
                        src_symbol=edge.src_symbol,
                        dst_package=edge.dst_package,
                        dst_symbol=edge.dst_symbol,
                        callsites=edge.callsites,
                    )
                )
            else:
                merged = tuple(
                    {
                        (site.file, site.line, site.note): site
                        for site in existing.callsites + edge.callsites
                    }.values()
                )
                index = self.incoming[target_ref].index(existing)
                self.incoming[target_ref][index] = Hop(
                    src_package=existing.src_package,
                    src_symbol=existing.src_symbol,
                    dst_package=existing.dst_package,
                    dst_symbol=existing.dst_symbol,
                    callsites=merged,
                )


def _enumerate_paths(
    target: SymbolRef,
    root_set: Set[str],
    incoming: Dict[SymbolRef, List[Hop]],
) -> Tuple[List[List[Hop]], int]:
    results: List[List[Hop]] = []
    truncated = 0

    # Iterative DFS. Each stack frame is (target_ref, hops_tried, path_to_target).
    # Seed: every incoming hop chain; emit when hop source package is a root.
    stack: List[Tuple[List[Hop], Set[SymbolRef]]] = []

    def seed(ref: SymbolRef) -> None:
        nonlocal truncated
        for hop in incoming.get(ref, []):
            source = SymbolRef(hop.src_package, hop.src_symbol)
            if source == ref:
                continue
            if hop.src_package in root_set:
                if len(results) < MAX_PATHS_PER_CHANGE:
                    results.append([hop])
                else:
                    truncated += 1
                continue
            stack.append(([hop], {ref, source}))

    seed(target)
    while stack:
        path, visited = stack.pop()
        head = path[0]
        source_ref = SymbolRef(head.src_package, head.src_symbol)
        extended = False
        for hop in incoming.get(source_ref, []):
            next_ref = SymbolRef(hop.src_package, hop.src_symbol)
            if next_ref in visited:
                continue
            new_path = [hop] + path
            extended = True
            if hop.src_package in root_set:
                if len(results) < MAX_PATHS_PER_CHANGE:
                    results.append(new_path)
                else:
                    truncated += 1
                continue
            stack.append((new_path, visited | {next_ref}))
        # A chain that ends at a non-root symbol with no public incoming
        # edges simply does not reach a root and is dropped.
        _ = extended
    results.sort(key=lambda hops: (len(hops), _path_key(hops)))
    return results, truncated


def _path_key(path: List[Hop]) -> Tuple[str, ...]:
    key = [f"{path[0].src_package}.{path[0].src_symbol}"]
    for hop in path:
        key.append(f"{hop.dst_package}.{hop.dst_symbol}")
    return tuple(key)


def analyze(
    catalog: Catalog,
    build: BuildInput,
    changes: List[BreakingChange],
    lattice: Optional[TypeLattice] = None,
) -> AnalysisResult:
    resolved = ResolvedBuild(catalog, build)
    result = AnalysisResult(roots=list(resolved.roots))

    for change in changes:
        target = SymbolRef(change.package, change.symbol)
        # A root's own exported API has no modeled consumer above it;
        # only changes reached through dependency edges impact a root.
        if change.package in resolved.root_set:
            continue
        paths, truncated = _enumerate_paths(target, resolved.root_set, resolved.incoming)
        if not paths:
            result.unreached.append(change)
            continue
        # Keep one Finding per (root, change); retain every concrete path
        # and union the call sites reachable on those paths.
        per_root: Dict[str, Finding] = {}
        for path in paths:
            root = path[0].src_package
            finding = per_root.setdefault(
                root,
                Finding(root=root, change=change, paths=[]),
            )
            finding.paths.append(path)
        for finding in per_root.values():
            sites: List[CallSite] = []
            for path in finding.paths:
                sites.extend(_collect_callsites(path))
            finding.affected_callsites = _dedupe_sites(sites)
            finding.truncated_paths = truncated
        result.findings.extend(per_root.values())

    result.findings.sort(
        key=lambda finding: (
            finding.root,
            finding.change.package,
            finding.change.symbol,
            finding.change.reason,
        )
    )
    result.unreached.sort(
        key=lambda change: (change.package, change.symbol, change.reason)
    )
    return result


def _collect_callsites(path: List[Hop]) -> List[CallSite]:
    sites: List[CallSite] = []
    for hop in path:
        sites.extend(hop.callsites)
    return _dedupe_sites(sites)


def _dedupe_sites(sites: List[CallSite]) -> List[CallSite]:
    unique = {
        (site.file, site.line, site.note): site for site in sites
    }
    return [unique[key] for key in sorted(unique)]

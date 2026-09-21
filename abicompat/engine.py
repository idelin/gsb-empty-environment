"""High-level entry point joining catalog, build input, diff and analysis."""

from __future__ import annotations

from dataclasses import dataclass
from typing import List

from .analysis import AnalysisResult, analyze
from .diff import BreakingChange, diff_releases
from .model import BuildInput, Catalog
from .typesys import TypeLattice
from .version import Version, bump_kind


@dataclass
class RunOutput:
    result: AnalysisResult
    changes: List[BreakingChange]
    nodes_report: List[dict]
    lattice: TypeLattice


def run(catalog: Catalog, build: BuildInput) -> RunOutput:
    lattice = TypeLattice(catalog.type_parents)
    all_changes: List[BreakingChange] = []
    nodes_report: List[dict] = []
    for node in build.nodes:
        package = catalog.packages[node.package]
        old_release = package.release(node.old_version)
        new_release = package.release(node.new_version)
        changes = diff_releases(
            node.package, old_release, new_release, lattice
        )
        all_changes.extend(changes)
        nodes_report.append(
            {
                "package": node.package,
                "old_version": node.old_version,
                "new_version": node.new_version,
                "bump": bump_kind(
                    Version.parse(node.old_version), Version.parse(node.new_version)
                ),
                "breaking_change_count": len(changes),
            }
        )
    result = analyze(catalog, build, all_changes, lattice)
    return RunOutput(
        result=result,
        changes=all_changes,
        nodes_report=nodes_report,
        lattice=lattice,
    )

"""Exhaustive path enumeration + concrete ground truth.

For small (acyclic) programs we enumerate *every* execution path and
concretely replay each one, collecting the exact set of ownership errors
and exit leaks.  Comparing this ground truth against the analyzer's
findings validates the reliability of the analysis: no false definite
findings and no missed leaks.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from . import concrete as C
from . import model as M
from .analyzer import LEAK, Analyzer


def enumerate_paths(
    program: M.Program, max_len: int = 25
) -> list[tuple[tuple[str, str], ...]]:
    """All simple (cycle-free) paths from the entry to an exit."""
    paths: list[tuple[tuple[str, str], ...]] = []

    def dfs(node_name: str, visited: frozenset, path: tuple) -> None:
        if len(path) > max_len:
            return
        node = program.nodes[node_name]
        if not node.edges:
            paths.append(path)
            return
        for edge in node.edges:
            if edge.target in visited:
                continue
            dfs(edge.target, visited | {edge.target}, path + ((edge.target, edge.kind),))

    dfs(program.entry, frozenset({program.entry}), ((program.entry, "entry"),))
    return paths


@dataclass
class GroundTruth:
    errors: set[tuple[str, str]] = field(default_factory=set)  # (kind, node)
    leaks: set[tuple[str, str]] = field(default_factory=set)   # (exit, acquire node)
    paths: int = 0
    runs: int = 0


def ground_truth(program: M.Program, max_len: int = 25, max_runs: int = 512) -> GroundTruth:
    truth = GroundTruth()
    for path in enumerate_paths(program, max_len):
        truth.paths += 1
        result = C.replay(program, path, max_runs)
        for run in result.runs:
            truth.runs += 1
            for event in run.events:
                truth.errors.add((event.kind, event.node))
            if not run.error:
                exit_node = path[-1][0]
                for rid in C.leaked_resources(run):
                    truth.leaks.add((exit_node, rid.split("#")[0]))
    return truth


@dataclass
class CrossCheck:
    program_ok: bool
    analyzer_errors: set
    truth_errors: set
    analyzer_leaks: set
    truth_leaks: set
    pending: bool

    @property
    def missed_errors(self) -> set:
        return self.truth_errors - self.analyzer_errors

    @property
    def missed_leaks(self) -> set:
        return self.truth_leaks - self.analyzer_leaks

    @property
    def spurious_errors(self) -> set:
        return self.analyzer_errors - self.truth_errors

    @property
    def spurious_leaks(self) -> set:
        return self.analyzer_leaks - self.truth_leaks


def cross_check(program: M.Program, max_len: int = 25) -> CrossCheck:
    """Compare analyzer findings with exhaustive concrete ground truth."""
    report = Analyzer(program).run()
    truth = ground_truth(program, max_len)
    analyzer_errors = {
        (f.kind, f.node)
        for f in report.findings
        if f.status == "definite" and f.kind != LEAK
    }
    analyzer_leaks = {
        (f.node, (f.resource or "")[2:])
        for f in report.findings
        if f.kind == LEAK and f.status in ("definite", "possible")
    }
    ok = (
        report.complete
        and analyzer_errors == truth.errors
        and analyzer_leaks == truth.leaks
    )
    return CrossCheck(
        program_ok=ok,
        analyzer_errors=analyzer_errors,
        truth_errors=truth.errors,
        analyzer_leaks=analyzer_leaks,
        truth_leaks=truth.leaks,
        pending=not report.complete,
    )

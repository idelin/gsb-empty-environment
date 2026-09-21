"""Human-readable and JSON rendering of analysis results."""

from __future__ import annotations

import json
from typing import Dict, List

from .analysis import Finding, Hop, AnalysisResult
from .diff import BreakingChange


def path_chain(path: List[Hop]) -> str:
    if not path:
        return ""
    start = f"{path[0].src_package}.{path[0].src_symbol}"
    chain = [start]
    for hop in path:
        chain.append(f"{hop.dst_package}.{hop.dst_symbol}")
    return " -> ".join(chain)


def _change_dict(change: BreakingChange) -> dict:
    return {
        "package": change.package,
        "symbol": change.symbol,
        "reason": change.reason,
        "detail": change.detail,
        "old_version": change.old_version,
        "new_version": change.new_version,
        "kind_old": change.kind_old,
        "kind_new": change.kind_new,
    }


def _hop_dict(hop: Hop) -> dict:
    return {
        "from": f"{hop.src_package}.{hop.src_symbol}",
        "to": f"{hop.dst_package}.{hop.dst_symbol}",
        "callsites": [
            {"file": site.file, "line": site.line, "note": site.note}
            for site in hop.callsites
        ],
    }


def finding_dict(finding: Finding) -> dict:
    data = _change_dict(finding.change)
    data.update(
        {
            "root": finding.root,
            "paths": [[_hop_dict(hop) for hop in path] for path in finding.paths],
            "path_chains": [path_chain(path) for path in finding.paths],
            "affected_callsites": [
                {"file": site.file, "line": site.line, "note": site.note}
                for site in finding.affected_callsites
            ],
            "truncated_paths": finding.truncated_paths,
        }
    )
    return data


def to_json(result: AnalysisResult, nodes_report: List[dict]) -> str:
    payload = {
        "roots": result.roots,
        "finding_count": len(result.findings),
        "findings": [finding_dict(finding) for finding in result.findings],
        "unreached_changes": [_change_dict(change) for change in result.unreached],
        "nodes": nodes_report,
    }
    return json.dumps(payload, indent=2, sort_keys=False)


def to_text(result: AnalysisResult, show_unreached: bool = True) -> str:
    lines: List[str] = []
    root_count = len({finding.root for finding in result.findings})
    if not result.findings:
        lines.append("No breaking changes reach any root package.")
    else:
        lines.append(
            f"Breaking changes: {len(result.findings)} finding(s) "
            f"impacting {root_count} root package(s)."
        )

    grouped: Dict[str, List[Finding]] = {}
    for finding in result.findings:
        grouped.setdefault(finding.root, []).append(finding)

    for root in sorted(grouped):
        lines.append("")
        lines.append(f"ROOT {root}")
        for finding in grouped[root]:
            change = finding.change
            lines.append(
                f"  - {change.package} {change.old_version} -> {change.new_version} "
                f"symbol {change.package}.{change.symbol}"
            )
            lines.append(f"      reason: {change.reason}")
            lines.append(f"      detail: {change.detail}")
            if not finding.paths:
                lines.append("      breaking path: (symbol belongs to the root itself)")
            else:
                lines.append("      breaking path(s):")
                for index, path in enumerate(finding.paths, start=1):
                    lines.append(f"        {index}. {path_chain(path)}")
                if finding.truncated_paths:
                    lines.append(
                        f"        ... {finding.truncated_paths} additional path(s) "
                        f"not shown"
                    )
            if finding.affected_callsites:
                lines.append("      affected call site(s):")
                for site in finding.affected_callsites:
                    suffix = f"  # {site.note}" if site.note else ""
                    lines.append(f"        - {site.file}:{site.line}{suffix}")
            else:
                lines.append("      affected call site(s): none recorded")

    if show_unreached and result.unreached:
        lines.append("")
        lines.append("Not impacting any root (no root call path reaches the symbol):")
        for change in result.unreached:
            lines.append(
                f"  - {change.package}.{change.symbol}: {change.reason} "
                f"({change.old_version} -> {change.new_version})"
            )
    return "\n".join(lines)

"""Human-readable rendering of an analysis Report."""
from __future__ import annotations

from .core import Report


def render_text(report: Report) -> str:
    lines = []
    version = report.version_check
    pre = " (pre-release)" if version["new"].count("-") else ""
    lines.append(f"Package: {report.package} {version['old']} -> {version['new']}{pre}")
    verdict = "OK" if version["ok"] else "VIOLATION"
    lines.append(
        f"Version bump: {version['actual_bump']} "
        f"(required: {version['required_bump']}) -- {verdict}"
    )
    for message in version["messages"]:
        lines.append(f"  note: {message}")
    lines.append("")
    lines.append(f"Package impact: {report.package_impact.upper()}")
    lines.append("Public API changes:")
    if report.public_changes:
        for change in report.public_changes:
            tag = "BREAKING" if change.breaking else "ok"
            lines.append(f"  [{tag:9}] {change.symbol}: {change.detail}")
    else:
        lines.append("  (none)")
    lines.append("")
    lines.append(f"Root impact: {report.root_impact.upper()}")
    if report.breaking_paths:
        lines.append(f"Affected roots: {', '.join(report.affected_roots)}")
        lines.append("Breaking paths:")
        for path in report.breaking_paths:
            lines.append(f"  {' -> '.join(path.nodes)}")
            for change in path.changes:
                lines.append(f"    change: {change.detail}")
            if path.sites:
                lines.append(f"    call sites: {', '.join(path.sites)}")
        if report.affected_sites:
            lines.append(f"Affected call sites: {', '.join(report.affected_sites)}")
    else:
        lines.append("No root is affected by breaking changes.")
    if report.unreachable_breaking:
        lines.append("")
        lines.append("Breaking changes not reachable from any root (no impact):")
        for change in report.unreachable_breaking:
            lines.append(f"  {change.symbol}: {change.detail}")
    if report.private_changes:
        lines.append("")
        lines.append("Ignored private-symbol changes (not public breakage):")
        for change in report.private_changes:
            lines.append(f"  {change.symbol}: {change.detail}")
    return "\n".join(lines)

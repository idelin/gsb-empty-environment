"""Human-readable rendering of a :class:`PlanReport`."""

from __future__ import annotations

from .planner import PlanReport


def render_text(report: PlanReport) -> str:
    lines: list[str] = []
    scope = "all packages" if not report.targets else "targets: " + ", ".join(report.targets)
    lines.append(f"Staged build plan ({scope})")
    lines.append(f"packages in scope: {len(report.in_scope)} of {report.total_packages}")
    lines.append("")

    if report.package_errors:
        lines.append("Invalid package definitions (excluded before planning):")
        for name, reason in sorted(report.package_errors.items()):
            lines.append(f"  - {name}: {reason}")
        lines.append("")

    if report.stages:
        lines.append("Executable staged plan:")
        for group in report.stages:
            action = "co-compile" if len(group.members) > 1 else "compile"
            lines.append(f"  Stage {group.stage} | {group.scc_id} | {action}")
            lines.append("    members (no internal topological order assumed):")
            for member in group.members:
                lines.append(f"      - {member}")
            if group.internal_edges:
                lines.append("    internal soft edges (why this is one group):")
                for edge in group.internal_edges:
                    lines.append(
                        f"      {edge.source} -> {edge.target} [{edge.kind}: {edge.label}]"
                    )
        lines.append("")
    else:
        lines.append("Executable staged plan: none")
        lines.append("")

    if report.blocked:
        lines.append("BLOCKED (refused, with concrete evidence):")
        for blocked in report.blocked:
            lines.append(f"  {blocked.scc_id} [{', '.join(blocked.codes)}]")
            lines.append(f"    members: {', '.join(blocked.members)}")
            for reason in blocked.reasons:
                lines.append(f"    reason: {reason}")
            for cycle in blocked.cycles:
                lines.append(f"    cycle evidence: {cycle.render()}")
            if blocked.blocking_downstream:
                lines.append(
                    "    makes downstream groups unbuildable: "
                    + ", ".join(blocked.blocking_downstream)
                )
        lines.append("")

    if report.warnings:
        lines.append("Warnings:")
        for warning in report.warnings:
            lines.append(f"  - {warning}")
        lines.append("")

    lines.append("VERDICT: " + ("PLAN OK" if report.ok else "REJECTED"))
    return "\n".join(lines).rstrip() + "\n"

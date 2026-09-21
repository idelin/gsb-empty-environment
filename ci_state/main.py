"""Command line: reconstruct build state from local JSON files.

Usage:
    python -m ci_state --packages packages.json --input build_input.json \\
        --events events.log.json [--json]
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import List

from .engine import BuildEngine, NodeReport, NodeState
from .loader import load_build_input, load_events, load_packages


def build(packages_path: str, input_path: str, events_path: str):
    catalog = load_packages(packages_path)
    nodes, resolved = load_build_input(input_path, catalog)
    events = load_events(events_path)
    engine = BuildEngine(nodes)
    engine.apply_many(events)
    return engine, resolved


def _format_text(engine: BuildEngine, resolved: List[dict]) -> str:
    lines: List[str] = []
    lines.append("Resolved packages (offline catalog):")
    for entry in resolved:
        pkgs = ", ".join(f"{name}=={ver}" for name, ver in entry["packages"].items())
        deps = ", ".join(entry["depends_on"]) or "-"
        lines.append(f"  - {entry['name']}: {pkgs or '(none)'} | depends_on: {deps}")
    lines.append("")
    summary = {state: 0 for state in NodeState}
    for report in engine.report():
        summary[report.state] += 1
        if report.state is NodeState.SUCCESS:
            verdict = "SUCCESS  (evidence complete)"
        elif report.state is NodeState.FAILED:
            reason = report.fail_reason or "unspecified failure"
            verdict = f"FAILED   ({reason})"
        else:
            verdict = "PENDING  (undecided - not guessed)"
        lines.append(
            f"[{verdict}] node={report.node} authoritative_attempt={report.attempt} "
            f"observed_attempts={report.observed_attempts or '-'}"
        )
        lines.append(
            f"    required={report.required} written={report.written} "
            f"committed={report.committed}"
        )
        for gap in report.missing:
            lines.append(f"    - {gap}")
    lines.append("")
    lines.append(
        f"Summary: {summary[NodeState.SUCCESS]} success, "
        f"{summary[NodeState.FAILED]} failed, {summary[NodeState.PENDING]} pending "
        f"(of {len(engine.nodes)} nodes); "
        f"{len(engine.duplicate_events)} duplicate event(s) ignored"
    )
    undecided = engine.undecided_nodes()
    if undecided:
        names = ", ".join(r.node for r in undecided)
        lines.append(f"Undecided nodes (need more evidence): {names}")
        exit_code = 2
    else:
        lines.append("All nodes reached a proven terminal state.")
        exit_code = 0 if summary[NodeState.FAILED] == 0 else 1
    lines.append("")
    return "\n".join(lines), exit_code


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Reconstruct build state from local JSON inputs (offline)."
    )
    parser.add_argument("--packages", required=True, help="local package catalog")
    parser.add_argument("--input", required=True, help="build definition JSON")
    parser.add_argument("--events", required=True, help="event log JSON array")
    parser.add_argument("--json", action="store_true", help="emit JSON report")
    args = parser.parse_args(argv)

    engine, resolved = build(args.packages, args.input, args.events)
    if args.json:
        payload = {
            "resolved": resolved,
            "nodes": [report.to_dict() for report in engine.report()],
            "duplicate_events_ignored": engine.duplicate_events,
            "undecided_nodes": [r.node for r in engine.undecided_nodes()],
        }
        print(json.dumps(payload, indent=2, ensure_ascii=False))
        return 2 if engine.undecided_nodes() else 0
    text, exit_code = _format_text(engine, resolved)
    print(text)
    return exit_code


if __name__ == "__main__":
    sys.exit(main())

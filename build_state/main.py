"""Offline build-state reconstruction CLI.

Usage:
    python -m build_state.main \
        --registry data/packages.json \
        --build-input data/build_input.json \
        --events data/events.json [--strict]
"""

from __future__ import annotations

import argparse
import json
import sys

from .models import load_build_input, load_events
from .registry import PackageRegistry
from .tracker import BuildTracker, NodeState


def build_report(registry_path: str, build_input_path: str, events_path: str) -> dict:
    registry = PackageRegistry.from_file(registry_path)
    build_input = load_build_input(build_input_path, registry)
    events = load_events(events_path)

    tracker = BuildTracker(build_input)
    tracker.ingest(events)
    statuses = tracker.reconstruct()
    return tracker.as_report(statuses)


def render(report: dict) -> str:
    lines = ["Build state (reconstructed from the local event log)", "=" * 52]
    for node in sorted(report["nodes"]):
        payload = report["nodes"][node]
        line = f"  {node:<14} {payload['state']:<7} attempt={payload['attempt']}"
        if payload["missing_artifacts"]:
            line += f" missing_artifacts={payload['missing_artifacts']}"
        if payload["missing_commit"]:
            line += " missing_commit=True"
        if payload["reason"]:
            line += f" reason={payload['reason']!r}"
        lines.append(line)

    counts = report["counts"]
    lines.append(
        f"\n  success={counts['success']} failed={counts['failed']} "
        f"pending={counts['pending']}"
    )
    if report["unresolved_nodes"]:
        lines.append(f"  unresolved (reported, never guessed): {report['unresolved_nodes']}")
    if report["duplicate_event_ids"]:
        lines.append(f"  duplicate event ids (idempotent): {report['duplicate_event_ids']}")
    if report["orphan_events"]:
        lines.append(f"  orphan events (ignored): {report['orphan_events']}")
    for anomaly in report["anomalies"]:
        lines.append(f"  anomaly[{anomaly['scope']}]: {anomaly['message']}")
    lines.append("")
    lines.append("JSON report:")
    lines.append(json.dumps(report, indent=2, ensure_ascii=False))
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--registry", required=True)
    parser.add_argument("--build-input", required=True)
    parser.add_argument("--events", required=True)
    parser.add_argument(
        "--strict",
        action="store_true",
        help="exit non-zero when any node is still PENDING",
    )
    args = parser.parse_args(argv)

    report = build_report(args.registry, args.build_input, args.events)
    print(render(report))
    if args.strict and report["counts"]["pending"]:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

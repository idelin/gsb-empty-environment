"""Command line entry point: reconstruct build state from local files.

Usage::

    python -m buildstate.cli --definition examples/build.json \\
        --events examples/events.jsonl

Exit codes:

* ``0`` - every node reached a terminal state successfully
* ``1`` - all nodes are terminal, but at least one failed
* ``2`` - one or more nodes are still pending (state not guessed)
* ``64`` - invalid definition or event input
"""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence

from .events import EventError, iter_event_lines, parse_event
from .model import DefinitionError, load_definition
from .tracker import FAILED_STATE, PENDING, SUCCESS, BuildTracker

_EXIT_OK = 0
_EXIT_FAILED = 1
_EXIT_PENDING = 2
_EXIT_USAGE = 64

_STATE_LABELS = {
    SUCCESS: "SUCCESS",
    FAILED_STATE: "FAILED ",
    PENDING: "PENDING",
}


def build_report(definition_path: str, events_path: str) -> tuple[str, int]:
    definition = load_definition(definition_path)
    tracker = BuildTracker(definition)
    for line_number, raw in iter_event_lines(events_path):
        try:
            event = parse_event(raw)
        except EventError as exc:
            raise EventError(f"{events_path}:{line_number}: {exc}") from None
        tracker.ingest(event)
    return render_snapshot(tracker.snapshot())


def render_snapshot(snapshot) -> tuple[str, int]:
    lines = ["NODE             STATE    ATTEMPT  DETAIL"]
    for node in snapshot.nodes:
        label = _STATE_LABELS[node.state]
        attempt = str(node.selected_attempt) if node.selected_attempt else "-"
        if node.state == SUCCESS:
            detail = "all required artifacts evidenced"
        elif node.state == FAILED_STATE:
            latest = node.attempts[-1]
            detail = f"failed: {latest.failure_reason}"
        else:
            reasons = "; ".join(item.detail for item in node.missing) or "pending"
            detail = reasons
        lines.append(f"{node.node:<16} {label} {attempt:>7}  {detail}")

    if snapshot.pending_nodes:
        lines.append("")
        lines.append("UNRESOLVED NODES (reported as pending, not guessed):")
        for node in snapshot.pending_nodes:
            lines.append(f"  - {node.node} (attempt {node.selected_attempt or '-'})")
            for item in node.missing:
                lines.append(f"      * {item.detail}")

    if snapshot.warnings:
        lines.append("")
        lines.append("WARNINGS:")
        for warning in snapshot.warnings:
            lines.append(f"  - {warning}")

    lines.append("")
    counts = {
        SUCCESS: len(snapshot.succeeded_nodes),
        FAILED_STATE: len(snapshot.failed_nodes),
        PENDING: len(snapshot.pending_nodes),
    }
    lines.append(
        f"SUMMARY: {counts[SUCCESS]} succeeded, {counts[FAILED_STATE]} failed, "
        f"{counts[PENDING]} pending"
    )

    if snapshot.pending_nodes:
        code = _EXIT_PENDING
    elif snapshot.failed_nodes:
        code = _EXIT_FAILED
    else:
        code = _EXIT_OK
    return "\n".join(lines), code


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Reconstruct build state from a definition JSON and JSONL events."
    )
    parser.add_argument("--definition", required=True, help="build definition JSON")
    parser.add_argument("--events", required=True, help="build events JSONL")
    args = parser.parse_args(argv)

    try:
        report, code = build_report(args.definition, args.events)
    except (DefinitionError, EventError, FileNotFoundError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return _EXIT_USAGE

    print(report)
    return code


if __name__ == "__main__":
    raise SystemExit(main())

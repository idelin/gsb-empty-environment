"""JSON checkpoint persistence using only the Python standard library."""

from __future__ import annotations

import json
import os
import tempfile
from pathlib import Path
from typing import Any

from .events import (
    AddMembership,
    AddReport,
    RetractMembership,
    RetractReport,
    RevisionEntry,
)
from .index import INF, NEG_INF, EndpointIndex, SegmentState
from .ledger import CoverageLedger
from .models import Membership, Report

_INF = "__inf__"
_NEG_INF = "__neg_inf__"


def encode_number(value):
    if value == INF:
        return _INF
    if value == NEG_INF:
        return _NEG_INF
    return value


def decode_number(value):
    if value == _INF:
        return INF
    if value == _NEG_INF:
        return NEG_INF
    return value


def encode_event(event):
    if isinstance(event, AddReport):
        report = event.report
        return {
            "type": "add_report",
            "report": [
                report.device,
                report.start,
                report.end,
                report.report_id,
            ],
        }
    if isinstance(event, RetractReport):
        return {"type": "retract_report", "report_id": event.report_id}
    if isinstance(event, AddMembership):
        membership = event.membership
        return {
            "type": "add_membership",
            "membership": [
                membership.device,
                encode_number(membership.start),
                encode_number(membership.end),
                membership.rule_version,
                membership.membership_id,
            ],
        }
    if isinstance(event, RetractMembership):
        return {
            "type": "retract_membership",
            "membership_id": event.membership_id,
        }
    raise TypeError(f"unsupported event: {event!r}")


def decode_event(payload: dict[str, Any]):
    event_type = payload["type"]
    if event_type == "add_report":
        device, start, end, report_id = payload["report"]
        return AddReport(Report(device, start, end, report_id))
    if event_type == "retract_report":
        return RetractReport(payload["report_id"])
    if event_type == "add_membership":
        device, start, end, rule_version, membership_id = payload["membership"]
        return AddMembership(
            Membership(
                device,
                decode_number(start),
                decode_number(end),
                rule_version,
                membership_id,
            )
        )
    if event_type == "retract_membership":
        return RetractMembership(payload["membership_id"])
    raise ValueError(f"unknown event type: {event_type}")


def encode_segment(state: SegmentState):
    return {
        "reports": {
            device: sorted(ids) for device, ids in state.reports.items()
        },
        "memberships": {
            device: dict(rules)
            for device, rules in state.memberships.items()
        },
    }


def decode_segment(payload: dict[str, Any]) -> SegmentState:
    return SegmentState(
        reports={
            device: set(ids) for device, ids in payload["reports"].items()
        },
        memberships={
            device: dict(rules)
            for device, rules in payload["memberships"].items()
        },
    )


def encode_index(index: EndpointIndex) -> dict[str, Any]:
    return {
        "points": [encode_number(point) for point in index.points],
        "refs": [[number, count] for number, count in sorted(index.refs.items(), key=lambda item: item[0])],
        "reports": {
            identifier: [
                report.device,
                report.start,
                report.end,
                report.report_id,
            ]
            for identifier, report in sorted(index.reports.items())
        },
        "memberships": {
            identifier: [
                membership.device,
                encode_number(membership.start),
                encode_number(membership.end),
                membership.rule_version,
                membership.membership_id,
            ]
            for identifier, membership in sorted(index.memberships.items())
        },
        "report_ids_by_device": {
            device: sorted(ids)
            for device, ids in sorted(index.report_ids_by_device.items())
        },
        "membership_ids_by_device": {
            device: sorted(ids)
            for device, ids in sorted(index.membership_ids_by_device.items())
        },
        "states": [encode_segment(state) for state in index.states],
    }


def decode_index(payload: dict[str, Any]) -> EndpointIndex:
    index = EndpointIndex()
    index.points = [decode_number(point) for point in payload["points"]]
    index.refs = {number: count for number, count in payload["refs"]}
    index.reports = {
        identifier: Report(device, start, end, report_id)
        for identifier, (device, start, end, report_id) in payload[
            "reports"
        ].items()
    }
    index.memberships = {
        identifier: Membership(
            device,
            decode_number(start),
            decode_number(end),
            rule_version,
            membership_id,
        )
        for identifier, (
            device,
            start,
            end,
            rule_version,
            membership_id,
        ) in payload["memberships"].items()
    }
    index.report_ids_by_device = {
        device: set(ids)
        for device, ids in payload["report_ids_by_device"].items()
    }
    index.membership_ids_by_device = {
        device: set(ids)
        for device, ids in payload["membership_ids_by_device"].items()
    }
    index.states = [decode_segment(item) for item in payload["states"]]
    return index


def save_checkpoint(ledger: CoverageLedger, path: str | os.PathLike[str]) -> None:
    target = Path(path)
    payload = {
        "format": "coverage-ledger-checkpoint/1",
        "revision": ledger.revision,
        "index": encode_index(ledger.index),
        "entries": [
            {
                "revision": entry.revision,
                "reason": entry.reason,
                "events": [encode_event(event) for event in entry.events],
            }
            for entry in ledger.entries
        ],
    }
    target.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(
        prefix=target.name + ".", suffix=".tmp", dir=str(target.parent)
    )
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            json.dump(payload, stream, ensure_ascii=False, sort_keys=True)
            stream.write("\n")
        os.replace(temporary_name, target)
    except BaseException:
        try:
            os.unlink(temporary_name)
        except FileNotFoundError:
            pass
        raise


def load_checkpoint(path: str | os.PathLike[str]) -> CoverageLedger:
    with Path(path).open("r", encoding="utf-8") as stream:
        payload = json.load(stream)
    ledger = CoverageLedger()
    ledger.index = decode_index(payload["index"])
    ledger.entries = [
        RevisionEntry(
            revision=item["revision"],
            events=tuple(decode_event(event) for event in item["events"]),
            reason=item.get("reason", ""),
        )
        for item in payload["entries"]
    ]
    if ledger.revision != payload["revision"]:
        raise ValueError("checkpoint revision does not match entries")
    replayed = ledger.index_at_revision(ledger.revision)
    expected_index = encode_index(replayed)
    if expected_index != payload["index"]:
        raise ValueError("checkpoint index does not match event log")
    return ledger

"""Independent compatible-slice validator.

Reads only the files under a storage root and re-derives every invariant
from the journal. It deliberately does NOT import the combiner's state
machine: the replay below is a second, independent implementation, so a bug
in the combiner shows up here as a validation failure.

Checks:

- journal integrity: every record parses; only a torn final line is allowed
- published records: well-formed, checksum-valid, deterministic result id,
  and mirrored by a ``published`` journal entry (no partial/orphan writes)
- compatible slices: every published version vector contains only slices
  that were accepted, share the record's rule version, belong to shards
  expected at publish time, and never mix a retired parent with its children
- value consistency: published values and compensation deltas recompute
  exactly from the replayed slices
- duplicate acks: the ack log is a set and every ack names a published result
- historical snapshots: each snapshot equals an independent replay of the
  journal up to the snapshot's own offset
"""
from __future__ import annotations

import hashlib
import json
from pathlib import Path

from .protocol import result_id_for


class _Replay:
    """Independent replay of the journal into plain window-state dicts."""

    def __init__(self) -> None:
        self.windows: dict[str, dict] = {}
        self.children_of: dict[str, dict[str, list[str]]] = {}

    def apply(self, rec: dict) -> None:
        kind = rec["type"]
        if kind == "window_opened":
            self.windows[rec["window_id"]] = {
                "window_id": rec["window_id"],
                "expected": sorted(rec["expected"]),
                "deadline": rec["deadline"],
                "slices": {},
                "retired": {},
                "status": "open",
                "publish_seq": 0,
                "effective_value": None,
                "published_rule_version": None,
                "last_result_id": None,
            }
            self.children_of[rec["window_id"]] = {}
        elif kind == "report_accepted":
            report = rec["report"]
            self.windows[report["window_id"]]["slices"][report["shard_id"]] = {
                "shard_id": report["shard_id"],
                "epoch": report["epoch"],
                "local_version": report["local_version"],
                "watermark": report["watermark"],
                "rule_version": report["rule_version"],
                "value": report["value"],
            }
        elif kind == "slice_revoked":
            self.windows[rec["window_id"]]["slices"].pop(rec["shard_id"], None)
        elif kind == "manifest_applied":
            manifest = rec["manifest"]
            window = self.windows[manifest["window_id"]]
            expected = set(window["expected"])
            expected.discard(manifest["parent"])
            expected.update(manifest["children"])
            window["expected"] = sorted(expected)
            window["retired"][manifest["parent"]] = manifest["epoch"]
            window["slices"].pop(manifest["parent"], None)
            self.children_of[manifest["window_id"]][manifest["parent"]] = \
                list(manifest["children"])
        elif kind == "window_timeout":
            self.windows[rec["window_id"]]["status"] = "timed_out"
        elif kind == "published":
            record = rec["record"]
            window = self.windows[record["window_id"]]
            window["publish_seq"] = record["seq"]
            window["last_result_id"] = record["result_id"]
            window["published_rule_version"] = record["rule_version"]
            if record["kind"] == "publish":
                if window["status"] == "open":
                    window["status"] = "published"
                window["effective_value"] = record["value"]
            else:
                window["effective_value"] = (
                    (window["effective_value"] or 0.0) + record["delta"])


def _load_journal(path: Path, problems: list[str]) -> list[dict]:
    records: list[dict] = []
    if not path.exists():
        problems.append("journal_missing")
        return records
    lines = path.read_text(encoding="utf-8").splitlines()
    for index, line in enumerate(lines):
        if not line.strip():
            continue
        try:
            records.append(json.loads(line))
        except json.JSONDecodeError:
            if index != len(lines) - 1:
                problems.append(f"journal_corrupt_line:{index + 1}")
            break  # a torn final line is the expected crash signature
    return records


def _check_compatible(replay: _Replay, window: dict,
                      record: dict) -> list[str]:
    problems: list[str] = []
    result_id = record["result_id"]
    rule_version = record["rule_version"]
    vector = record["version_vector"]
    for shard, local_version in vector.items():
        slice_ = window["slices"].get(shard)
        if slice_ is None:
            problems.append(f"vector_shard_not_accepted:{result_id}:{shard}")
            continue
        if slice_["local_version"] != local_version:
            problems.append(f"vector_version_mismatch:{result_id}:{shard}")
        if slice_["rule_version"] != rule_version:
            problems.append(f"incompatible_rule_version:{result_id}:{shard}")
        if shard in window["retired"]:
            problems.append(f"retired_shard_counted:{result_id}:{shard}")
        if shard not in window["expected"]:
            problems.append(f"unexpected_shard_counted:{result_id}:{shard}")
    for parent, children in \
            replay.children_of[window["window_id"]].items():
        if parent in vector and any(c in vector for c in children):
            problems.append(f"parent_child_double_count:{result_id}:{parent}")
    if record["complete"]:
        missing = set(window["expected"]) - set(vector)
        if missing:
            problems.append(
                f"complete_but_missing:{result_id}:{sorted(missing)}")
    return problems


def _vector_value(window: dict, record: dict) -> float | None:
    total = 0.0
    for shard, local_version in record["version_vector"].items():
        slice_ = window["slices"].get(shard)
        if (slice_ is None
                or slice_["local_version"] != local_version
                or slice_["rule_version"] != record["rule_version"]):
            return None
        total += slice_["value"]
    return total


def validate(root: str | Path) -> list[str]:
    """Validate a storage root; returns a list of problems (empty == OK)."""
    root = Path(root)
    problems: list[str] = []
    records = _load_journal(root / "journal.log", problems)

    # ---- published records: parseable, checksummed, deterministic id ----
    published_dir = root / "published"
    published_files: dict[str, dict] = {}
    if published_dir.exists():
        for path in sorted(published_dir.glob("*.json")):
            try:
                envelope = json.loads(path.read_text(encoding="utf-8"))
            except json.JSONDecodeError:
                problems.append(f"partial_write:{path.name}")
                continue
            record = envelope.get("record", {})
            payload = json.dumps(record, sort_keys=True).encode()
            if hashlib.sha256(payload).hexdigest() != envelope.get("sha256"):
                problems.append(f"checksum_mismatch:{path.name}")
                continue
            body = {k: v for k, v in record.items() if k != "result_id"}
            if record.get("result_id") != result_id_for(body):
                problems.append(f"result_id_mismatch:{path.name}")
            published_files[record.get("result_id")] = record
        for path in sorted(published_dir.glob("*.tmp")):
            problems.append(f"orphan_partial_write:{path.name}")

    journaled = [r["record"] for r in records if r["type"] == "published"]
    journaled_ids = [r["result_id"] for r in journaled]
    for result_id in published_files:
        if result_id not in journaled_ids:
            problems.append(f"published_without_journal:{result_id}")
    for result_id in journaled_ids:
        if result_id not in published_files:
            problems.append(f"journaled_publish_missing_file:{result_id}")

    # ---- compatible slices + value consistency, checked at publish time ----
    replay = _Replay()
    effective: dict[str, float] = {}
    for rec in records:
        if rec["type"] == "published":
            record = rec["record"]
            window = replay.windows.get(record["window_id"])
            if window is None:
                problems.append(
                    f"publish_for_unknown_window:{record['result_id']}")
            else:
                problems.extend(_check_compatible(replay, window, record))
                recomputed = _vector_value(window, record)
                window_id = record["window_id"]
                if recomputed is not None:
                    if record["kind"] == "publish":
                        if abs(recomputed - record["value"]) > 1e-9:
                            problems.append(
                                f"value_mismatch:{record['result_id']}")
                        effective[window_id] = record["value"]
                    else:
                        previous = effective.get(window_id)
                        if previous is not None and abs(
                                (recomputed - previous)
                                - record["delta"]) > 1e-9:
                            problems.append(
                                f"compensation_delta_mismatch:"
                                f"{record['result_id']}")
                        effective[window_id] = recomputed
        replay.apply(rec)

    # ---- duplicate / dangling acks ----
    acks_path = root / "acks.log"
    if acks_path.exists():
        seen: set[str] = set()
        for line in acks_path.read_text(encoding="utf-8").splitlines():
            result_id = line.strip()
            if not result_id:
                continue
            if result_id in seen:
                problems.append(f"duplicate_ack:{result_id}")
            seen.add(result_id)
            if result_id not in journaled_ids:
                problems.append(f"ack_for_unknown_result:{result_id}")

    # ---- historical snapshots vs. independent replay ----
    snapshots_dir = root / "snapshots"
    if snapshots_dir.exists():
        for path in sorted(snapshots_dir.glob("*.json")):
            snapshot = json.loads(path.read_text(encoding="utf-8"))
            prefix = records[: snapshot["journal_offset"]]
            check = _Replay()
            for rec in prefix:
                check.apply(rec)
            state = check.windows.get(snapshot["window_id"])
            if state != snapshot["state"]:
                problems.append(f"snapshot_mismatch:{path.name}")

    return problems

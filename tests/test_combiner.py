import json
from pathlib import Path

import pytest

from combiner.combiner import SimulatedCrash, WindowCombiner
from combiner.downstream import DownstreamSink
from combiner.protocol import REVOKE, HandoverManifest, ShardReport
from combiner.storage import Storage
from combiner.validator import validate


@pytest.fixture
def sink():
    return DownstreamSink()


@pytest.fixture
def combiner(tmp_path, sink):
    return WindowCombiner(Storage(tmp_path / "data"), sink)


def published_events(combiner):
    return [e["record"] for e in combiner.events if e["type"] == "published"]


def test_compatible_slices_combine_to_8(combiner, tmp_path):
    combiner.open_window("W1", {"A", "B"}, deadline=100)
    combiner.ingest(ShardReport("W1", "A", 1, 1, 10, "r1", 9))
    combiner.ingest(ShardReport("W1", "A", 1, 2, 10, "r1", 5))  # supersedes
    combiner.ingest(ShardReport("W1", "B", 1, 1, 10, "r1", 3))
    records = published_events(combiner)
    assert len(records) == 1
    assert records[0]["value"] == 8
    assert records[0]["version_vector"] == {"A": 2, "B": 1}
    assert records[0]["watermarks"] == {"A": 10, "B": 10}
    assert validate(tmp_path / "data") == []


def test_other_rule_version_stays_pending(combiner):
    combiner.open_window("W1", {"A", "B"}, deadline=100)
    combiner.ingest(ShardReport("W1", "A", 1, 2, 10, "r1", 5))
    combiner.ingest(ShardReport("W1", "B", 1, 1, 10, "r2", 3))  # other rules
    assert published_events(combiner) == []
    assert combiner.windows["W1"].status == "open"
    combiner.ingest(ShardReport("W1", "B", 1, 1, 10, "r1", 3))  # compatible
    assert published_events(combiner)[0]["value"] == 8


def test_stale_and_duplicate_versions_rejected(combiner):
    combiner.open_window("W1", {"A", "B"}, deadline=100)
    combiner.ingest(ShardReport("W1", "A", 1, 2, 10, "r1", 5))
    events = combiner.ingest(ShardReport("W1", "A", 1, 1, 10, "r1", 9))
    assert events[0]["reason"] == "stale_version"
    events = combiner.ingest(ShardReport("W1", "A", 1, 2, 10, "r1", 5))
    assert events[0]["reason"] == "duplicate"
    assert combiner.windows["W1"].slices["A"].value == 5


def test_handover_no_double_count(combiner, tmp_path):
    combiner.open_window("W2", {"A", "B"}, deadline=100)
    combiner.ingest(ShardReport("W2", "A", 1, 1, 20, "r1", 100))
    combiner.apply_manifest(HandoverManifest("W2", "A", ("A1", "A2"), epoch=2))
    combiner.ingest(ShardReport("W2", "A1", 2, 1, 20, "r1", 4))
    events = combiner.ingest(ShardReport("W2", "A", 1, 2, 20, "r1", 9))
    assert events[0]["reason"] == "shard_retired"
    combiner.ingest(ShardReport("W2", "A2", 2, 1, 20, "r1", 6))
    combiner.ingest(ShardReport("W2", "B", 1, 1, 20, "r1", 3))
    records = published_events(combiner)
    assert len(records) == 1
    assert records[0]["value"] == 13  # 4 + 6 + 3, old A never counted
    assert records[0]["version_vector"] == {"A1": 1, "A2": 1, "B": 1}
    assert validate(tmp_path / "data") == []


def test_crash_between_publish_and_ack_recovers_same_id(tmp_path, sink):
    root = tmp_path / "data"
    combiner = WindowCombiner(Storage(root), sink)
    combiner.open_window("W2", {"A", "B"}, deadline=100)
    combiner.apply_manifest(HandoverManifest("W2", "A", ("A1", "A2"), epoch=2))
    combiner.ingest(ShardReport("W2", "A1", 2, 1, 20, "r1", 4))
    combiner.ingest(ShardReport("W2", "A2", 2, 1, 20, "r1", 6))
    combiner.fail_before_ack = True
    with pytest.raises(SimulatedCrash):
        combiner.ingest(ShardReport("W2", "B", 1, 1, 20, "r1", 3))
    crashed_ids = set(Storage(root).read_published())
    assert len(crashed_ids) == 1

    restarted = WindowCombiner(Storage(root), sink)
    reemitted = restarted.recover()
    assert [r["result_id"] for r in reemitted] == list(crashed_ids)
    assert reemitted[0]["value"] == 13
    # the record was delivered once before the crash and re-delivered on
    # recovery; the downstream applied the delta exactly once
    assert sink.apply_count == 2
    assert len(sink.records) == 1
    assert sink.total == 13
    assert validate(root) == []


def test_correction_after_publish_is_compensating(tmp_path, sink):
    root = tmp_path / "data"
    combiner = WindowCombiner(Storage(root), sink)
    combiner.open_window("W1", {"A", "B"}, deadline=100)
    combiner.ingest(ShardReport("W1", "A", 1, 2, 10, "r1", 5))
    combiner.ingest(ShardReport("W1", "B", 1, 1, 10, "r1", 3))
    first_id = combiner.windows["W1"].last_result_id
    combiner.ingest(ShardReport("W1", "A", 1, 3, 10, "r1", 7))
    records = published_events(combiner)
    assert [r["kind"] for r in records] == ["publish", "compensation"]
    compensation = records[1]
    assert compensation["delta"] == 2
    assert compensation["supersedes"] == first_id
    # history is not rewritten
    assert Storage(root).read_published()[first_id]["value"] == 8
    assert sink.total == 10
    assert validate(root) == []


def test_revoke_after_publish_compensates(combiner, sink):
    combiner.open_window("W4", {"E", "F"}, deadline=100)
    combiner.ingest(ShardReport("W4", "E", 1, 1, 40, "r1", 1))
    combiner.ingest(ShardReport("W4", "F", 1, 1, 40, "r1", 2))
    combiner.ingest(ShardReport("W4", "F", 1, 2, 40, "r1", kind=REVOKE))
    records = published_events(combiner)
    assert records[1]["kind"] == "compensation"
    assert records[1]["delta"] == -2
    assert records[1]["complete"] is False
    assert records[1]["missing"] == ["F"]
    assert sink.total == 1


def test_timeout_publishes_partial(combiner):
    combiner.open_window("W3", {"C", "D"}, deadline=50)
    combiner.ingest(ShardReport("W3", "C", 1, 1, 30, "r1", 5))
    events = combiner.tick(60)
    assert any(e["type"] == "window_timeout" for e in events)
    record = published_events(combiner)[0]
    assert record["complete"] is False
    assert record["missing"] == ["D"]
    assert record["value"] == 5


def test_recovery_continues_ingesting(tmp_path, sink):
    root = tmp_path / "data"
    combiner = WindowCombiner(Storage(root), sink)
    combiner.open_window("W1", {"A", "B"}, deadline=100)
    combiner.ingest(ShardReport("W1", "A", 1, 1, 10, "r1", 5))
    restarted = WindowCombiner(Storage(root), sink)
    restarted.recover()
    restarted.ingest(ShardReport("W1", "B", 1, 1, 10, "r1", 3))
    assert restarted.windows["W1"].effective_value == 8
    assert sink.total == 8
    assert validate(root) == []


# --------------------------------------------------------------- validator --

def _valid_root(tmp_path) -> Path:
    root = tmp_path / "data"
    combiner = WindowCombiner(Storage(root), DownstreamSink())
    combiner.open_window("W1", {"A", "B"}, deadline=100)
    combiner.ingest(ShardReport("W1", "A", 1, 1, 10, "r1", 5))
    combiner.ingest(ShardReport("W1", "B", 1, 1, 10, "r1", 3))
    assert validate(root) == []
    return root


def test_validator_detects_partial_write(tmp_path):
    root = _valid_root(tmp_path)
    target = next((root / "published").glob("*.json"))
    target.write_text(target.read_text()[:40])  # truncate mid-file
    problems = validate(root)
    assert any(p.startswith("partial_write:") for p in problems)


def test_validator_detects_checksum_tampering(tmp_path):
    root = _valid_root(tmp_path)
    target = next((root / "published").glob("*.json"))
    envelope = json.loads(target.read_text())
    envelope["record"]["value"] = 999
    target.write_text(json.dumps(envelope))
    problems = validate(root)
    assert any(p.startswith("checksum_mismatch:") for p in problems)


def test_validator_detects_duplicate_ack(tmp_path):
    root = _valid_root(tmp_path)
    ack = (root / "acks.log").read_text().splitlines()[0]
    Storage(root).append_ack(ack)
    problems = validate(root)
    assert any(p.startswith("duplicate_ack:") for p in problems)


def test_validator_detects_snapshot_tampering(tmp_path):
    root = _valid_root(tmp_path)
    target = next((root / "snapshots").glob("*.json"))
    snapshot = json.loads(target.read_text())
    snapshot["state"]["effective_value"] = 12345
    target.write_text(json.dumps(snapshot))
    problems = validate(root)
    assert any(p.startswith("snapshot_mismatch:") for p in problems)


def test_validator_detects_journal_corruption(tmp_path):
    root = _valid_root(tmp_path)
    with open(root / "journal.log", "r+") as fh:
        lines = fh.readlines()
        fh.seek(0)
        fh.writelines([lines[0], '{"type": "window_opened", BROKEN'] + lines[1:])
        fh.truncate()
    problems = validate(root)
    assert any(p.startswith("journal_corrupt_line:") for p in problems)

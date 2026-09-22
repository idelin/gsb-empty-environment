import json
import os

import pytest

from winmerge.downstream import FileDownstream
from winmerge.merger import WindowMerger, WindowSpec
from winmerge.store import EventLog
from winmerge import validator

WINDOWS = [
    WindowSpec("w1", 0, 100, deadline=1000),
    WindowSpec("w2", 100, 200, deadline=1000),
    WindowSpec("w3", 200, 300, deadline=1000),
    WindowSpec("w4", 300, 400, deadline=500),
]
SHARDS = ["A", "B"]


def partial(window, shard, lv, wm, value, epoch=1, rules="r1"):
    return {"type": "partial", "window": window, "shard": shard, "epoch": epoch,
            "local_version": lv, "watermark": wm, "rule_version": rules,
            "value": value}


@pytest.fixture
def rig(tmp_path):
    state_dir = str(tmp_path / "merger")
    downstream = FileDownstream(str(tmp_path / "downstream.json"))
    merger = WindowMerger(state_dir, WINDOWS, SHARDS, downstream)
    return merger, downstream, state_dir


def publishes(merger, window):
    return [p for p in merger.windows[window].publishes]


def test_compatible_slices_merge_to_8(rig):
    m, ds, _ = rig
    m.handle(partial("w1", "A", 2, 10, 5))
    m.handle(partial("w1", "A", 1, 10, 9))  # stale local version
    m.handle(partial("w1", "B", 1, 20, 3))
    pub = publishes(m, "w1")
    assert len(pub) == 1
    assert pub[0]["value"] == 8
    assert pub[0]["version_vector"] == {"A": 2, "B": 1}
    assert ds.window_total("w1") == 8
    rejected = [r["payload"] for r in m.log.records()
                if r["payload"]["rec"] == "slice_rejected"]
    assert any(r["reason"] == "stale_version" and r["local_version"] == 1
               for r in rejected)


def test_rule_version_mismatch_stays_pending(rig):
    m, ds, _ = rig
    m.handle(partial("w1", "A", 1, 10, 5, rules="r1"))
    m.handle(partial("w1", "B", 1, 20, 3, rules="r2"))
    assert publishes(m, "w1") == []
    assert ds.window_total("w1") is None


def test_handoff_split_no_double_count(rig):
    m, ds, _ = rig
    m.handle({"type": "handoff", "manifest_id": "m2", "epoch": 2,
              "splits": {"A": ["A1", "A2"]}})
    # old shard A interleaves with new sub-shards; must be rejected
    m.handle(partial("w3", "A", 3, 210, 100, epoch=1))
    m.handle(partial("w3", "A1", 1, 210, 1, epoch=2))
    m.handle(partial("w3", "A", 4, 210, 100, epoch=1))
    m.handle(partial("w3", "A2", 1, 220, 2, epoch=2))
    m.handle(partial("w3", "B", 1, 230, 4, epoch=2))
    pub = publishes(m, "w3")
    assert len(pub) == 1
    assert pub[0]["version_vector"] == {"A1": 1, "A2": 1, "B": 1}
    assert pub[0]["value"] == 7  # old A's 100 never counted
    rejected = [r["payload"] for r in m.log.records()
                if r["payload"]["rec"] == "slice_rejected"]
    assert sum(1 for r in rejected if r["reason"] == "stale_epoch") == 2


def test_correction_emits_compensation_not_rewrite(rig):
    m, ds, _ = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    original = publishes(m, "w1")[0]
    m.handle(partial("w1", "A", 2, 10, 7))  # correction after publish
    pubs = publishes(m, "w1")
    assert [p["kind"] for p in pubs] == ["result", "compensation"]
    comp = pubs[1]
    assert comp["delta"] == 2 and comp["value"] == 10
    assert comp["base_result_id"] == original["result_id"]
    assert ds.window_total("w1") == 10
    # history untouched: the original publish record is still first & intact
    on_disk = [r["payload"] for r in m.log.records()
               if r["payload"]["rec"] == "publish"]
    assert on_disk[0]["result_id"] == original["result_id"]
    assert on_disk[0]["value"] == 8


def test_revoke_after_publish_retracts(rig):
    m, ds, _ = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    m.handle({"type": "revoke", "window": "w1", "shard": "B", "epoch": 1,
              "local_version": 1, "reason": "bad source"})
    pubs = publishes(m, "w1")
    assert pubs[-1]["retracted"] is True
    assert pubs[-1]["delta"] == -8
    assert ds.window_total("w1") == 0


def test_revoke_before_publish_keeps_window_open(rig):
    m, ds, _ = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    m.handle({"type": "revoke", "window": "w1", "shard": "B", "epoch": 1,
              "local_version": 1, "reason": "oops"})
    # replacement slice heals the window via compensation
    m.handle(partial("w1", "B", 2, 20, 4))
    pubs = publishes(m, "w1")
    assert pubs[-1]["value"] == 9
    assert ds.window_total("w1") == 9


def test_timeout_expires_window_and_late_partial_rejected(rig):
    m, ds, _ = rig
    m.handle(partial("w4", "A", 1, 310, 1))
    m.handle({"type": "tick", "now": 600})
    assert m.windows["w4"].status == "expired"
    m.handle(partial("w4", "B", 1, 320, 2))
    assert publishes(m, "w4") == []
    expired = [r["payload"] for r in m.log.records()
               if r["payload"]["rec"] == "window_expired"]
    assert expired[0]["missing_shards"] == ["B"]


def test_watermark_out_of_window_rejected(rig):
    m, ds, _ = rig
    m.handle(partial("w1", "A", 1, 150, 5))  # watermark belongs to w2
    assert publishes(m, "w1") == []
    rejected = [r["payload"] for r in m.log.records()
                if r["payload"]["rec"] == "slice_rejected"]
    assert rejected[0]["reason"] == "watermark_out_of_window"


def test_crash_recovery_same_result_id_and_idempotent_delta(tmp_path):
    state_dir = str(tmp_path / "merger")
    ds_path = str(tmp_path / "downstream.json")

    class Boom(Exception):
        pass

    def crash_hook(stage, payload):
        if stage == "after_delivery" and payload["window"] == "w1":
            raise Boom("process killed between publish write and ack")

    downstream = FileDownstream(ds_path)
    m1 = WindowMerger(state_dir, WINDOWS, SHARDS, downstream, crash_hook)
    m1.handle(partial("w1", "A", 1, 10, 5))
    with pytest.raises(Boom):
        m1.handle(partial("w1", "B", 1, 20, 3))
    crashed_id = publishes(m1, "w1")[0]["result_id"]
    assert crashed_id not in m1.windows["w1"].acked

    # fresh process: state rebuilt purely from the log
    downstream2 = FileDownstream(ds_path)
    m2 = WindowMerger(state_dir, WINDOWS, SHARDS, downstream2)
    redelivered = m2.recover()
    assert redelivered == [crashed_id]  # identical result id
    assert downstream2.applied_ids().count(crashed_id) == 1
    assert downstream2.window_total("w1") == 8  # delta applied exactly once
    assert crashed_id in m2.windows["w1"].acked


def test_duplicate_and_unknown_ack(rig):
    m, ds, _ = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    rid = publishes(m, "w1")[0]["result_id"]
    m.handle({"type": "ack", "result_id": rid})
    m.handle({"type": "ack", "result_id": "res-nope"})
    recs = [r["payload"]["rec"] for r in m.log.records()]
    assert "ack_duplicate" in recs
    assert "ack_unknown" in recs
    assert recs.count("publish_acked") == 1


def test_validator_passes_clean_run(rig, tmp_path):
    m, ds, state_dir = rig
    m.handle(partial("w1", "A", 2, 10, 5))
    m.handle(partial("w1", "A", 1, 10, 9))
    m.handle(partial("w1", "B", 1, 20, 3))
    m.handle(partial("w1", "A", 3, 10, 6))
    findings = validator.audit(state_dir, SHARDS)
    assert all(f.ok for f in findings), [str(f) for f in findings]


def test_validator_detects_torn_tail(rig):
    m, ds, state_dir = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    with open(os.path.join(state_dir, "events.jsonl"), "a") as fh:
        fh.write('{"seq": 99, "prev": "abc", "paylo')  # crash mid-write
    findings = {f.check: f for f in validator.audit(state_dir, SHARDS)}
    assert not findings["torn-tail"].ok
    assert findings["log-integrity"].ok  # prefix still intact


def test_validator_detects_tampered_history(rig):
    m, ds, state_dir = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    path = os.path.join(state_dir, "events.jsonl")
    lines = open(path).read().splitlines()
    rec = json.loads(lines[0])
    rec["payload"]["value"] = 999  # silent rewrite attempt
    lines[0] = json.dumps(rec)
    open(path, "w").write("\n".join(lines) + "\n")
    findings = {f.check: f for f in validator.audit(state_dir, SHARDS)}
    assert not findings["log-integrity"].ok


def test_validator_detects_history_rewrite_via_second_result(rig):
    m, ds, state_dir = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    # an offender appends a second full 'result' publish for the same window
    log = EventLog(os.path.join(state_dir, "events.jsonl"))
    core = {"kind": "result", "window": "w1", "rule_version": "r1", "epoch": 1,
            "version_vector": {"A": 1, "B": 1}, "value": 42, "comp_index": 0}
    import hashlib
    from winmerge.store import canonical
    rid = "res-" + hashlib.sha256(canonical(core).encode()).hexdigest()[:16]
    log.append({"rec": "publish", **core, "delta": 42, "base_result_id": None,
                "retracted": False, "result_id": rid})
    findings = {f.check: f for f in validator.audit(state_dir, SHARDS)}
    assert not findings["compensation-discipline"].ok


def test_validator_detects_double_count(rig):
    m, ds, state_dir = rig
    m.handle({"type": "handoff", "manifest_id": "m2", "epoch": 2,
              "splits": {"A": ["A1", "A2"]}})
    m.handle(partial("w3", "A1", 1, 210, 1, epoch=2))
    m.handle(partial("w3", "A2", 1, 220, 2, epoch=2))
    m.handle(partial("w3", "B", 1, 230, 4, epoch=2))
    # offender injects a publish counting both old A and new A1
    log = EventLog(os.path.join(state_dir, "events.jsonl"))
    core = {"kind": "result", "window": "w2", "rule_version": "r1", "epoch": 2,
            "version_vector": {"A": 9, "A1": 1, "A2": 1, "B": 1},
            "value": 107, "comp_index": 0}
    import hashlib
    from winmerge.store import canonical
    rid = "res-" + hashlib.sha256(canonical(core).encode()).hexdigest()[:16]
    log.append({"rec": "publish", **core, "delta": 107, "base_result_id": None,
                "retracted": False, "result_id": rid})
    findings = {f.check: f for f in validator.audit(state_dir, SHARDS)}
    assert not findings["no-double-count"].ok


def test_snapshot_verifies_history_prefix(rig, tmp_path):
    m, ds, state_dir = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    snap = str(tmp_path / "snap.json")
    validator.snapshot(state_dir, snap)
    m.handle(partial("w1", "A", 2, 10, 9))  # later correction
    assert validator.verify_snapshot(state_dir, snap)
    # tamper with the prefix -> snapshot no longer verifies
    path = os.path.join(state_dir, "events.jsonl")
    lines = open(path).read().splitlines()
    rec = json.loads(lines[0])
    rec["payload"]["value"] = 0
    rec["checksum"] = "0" * 64
    lines[0] = json.dumps(rec)
    open(path, "w").write("\n".join(lines) + "\n")
    assert not validator.verify_snapshot(state_dir, snap)


def test_full_replay_reproduces_state(rig):
    m, ds, state_dir = rig
    m.handle(partial("w1", "A", 1, 10, 5))
    m.handle(partial("w1", "B", 1, 20, 3))
    m.handle(partial("w1", "A", 2, 10, 7))
    m2 = WindowMerger(state_dir, WINDOWS, SHARDS, FileDownstream(
        os.path.join(state_dir, "ds2.json")))
    assert [p["result_id"] for p in publishes(m2, "w1")] == \
           [p["result_id"] for p in publishes(m, "w1")]
    assert m2.windows["w1"].acked == m.windows["w1"].acked

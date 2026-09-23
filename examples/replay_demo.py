"""Record a deterministic run (messages, batches, clock, faults) to a JSONL
log, then replay the log and verify the outputs are bit-identical.

Usage: python3 examples/replay_demo.py [log_path]
"""
import json
import os
import random
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from dynjoin import (AckAll, BuildBarrier, BuildData, Checkpoint, Crash,
                     Deliver, Harness, JoinType, NullMode, Poll, Rebuild,
                     Strategy, Submit)

DEFAULT_LOG = os.path.join(os.path.dirname(__file__), "replay_log.jsonl")


def build_script(seed=20260923):
    rng = random.Random(seed)
    events = [
        Deliver(BuildData(0, 1, 1, "a", "b1")),
        Submit("s0", [("a", "s1"), ("c", "s2")]),      # filter incomplete
        Deliver(BuildBarrier(0, 1, 2)),                # barrier first: seq 2 still missing
        Deliver(BuildData(0, 1, 2, "c", "b2")),        # arrives after barrier
        Deliver(BuildData(0, 1, 2, "c", "b2")),        # duplicate redelivery
        Checkpoint(),
        Rebuild(0, 2),                                 # version switch
        Deliver(BuildData(0, 2, 1, "a", "n1")),
        Submit("s1", [("a", "s3"), (None, "s4")]),     # binds epoch 2
        Poll(),
        Crash(),                                       # fault after output
        Deliver(BuildBarrier(0, 2, 1)),
        Poll(),
        AckAll(),
        Submit("s2", [("c", "s5"), ("a", "s6")]),
        Poll(),
        AckAll(),
    ]
    return events


def event_to_json(event):
    if isinstance(event, Deliver):
        msg = event.msg
        if isinstance(msg, BuildData):
            return {"kind": "deliver", "msg": "data", "partition": msg.partition,
                    "epoch": msg.epoch, "seq": msg.seq, "key": msg.key,
                    "payload": msg.payload}
        return {"kind": "deliver", "msg": "barrier", "partition": msg.partition,
                "epoch": msg.epoch, "last_seq": msg.last_seq}
    if isinstance(event, Submit):
        return {"kind": "submit", "batch_id": event.batch_id,
                "rows": [[k, v] for k, v in event.rows]}
    if isinstance(event, Rebuild):
        return {"kind": "rebuild", "partition": event.partition,
                "epoch": event.epoch}
    return {"kind": type(event).__name__.lower()}


def event_from_json(obj):
    kind = obj["kind"]
    if kind == "deliver":
        if obj["msg"] == "data":
            return Deliver(BuildData(obj["partition"], obj["epoch"], obj["seq"],
                                     obj["key"], obj["payload"]))
        return Deliver(BuildBarrier(obj["partition"], obj["epoch"],
                                    obj["last_seq"]))
    if kind == "submit":
        return Submit(obj["batch_id"], [tuple(r) for r in obj["rows"]])
    if kind == "rebuild":
        return Rebuild(obj["partition"], obj["epoch"])
    return {"checkpoint": Checkpoint, "crash": Crash,
            "poll": Poll, "ackall": AckAll}[kind]()


def make_harness(tmpdir):
    return Harness(
        {"num_partitions": 1, "join_type": JoinType.LEFT,
         "null_mode": NullMode.REJECT, "strategy": Strategy.BYPASS,
         "filter_kind": "bloom", "bloom_size": 64, "bloom_hashes": 2},
        tmpdir=tmpdir)


def record(log_path, tmpdir):
    events = build_script()
    harness = make_harness(tmpdir)
    harness.run(events)
    with open(log_path, "w") as fh:
        for event in events:
            fh.write(json.dumps({"record": "event", **event_to_json(event)}) + "\n")
        for output_id, rows in harness.output_log:
            fh.write(json.dumps({"record": "output", "id": output_id,
                                 "rows": rows}) + "\n")
    return harness


def replay(log_path, tmpdir):
    events, expected_outputs = [], []
    with open(log_path) as fh:
        for line in fh:
            obj = json.loads(line)
            if obj["record"] == "event":
                events.append(event_from_json(obj))
            else:
                expected_outputs.append((obj["id"], [tuple(r) for r in obj["rows"]]))
    harness = make_harness(tmpdir)
    harness.run(events)
    assert harness.output_log == expected_outputs, "replay diverged from the log"
    return harness, expected_outputs


def main():
    log_path = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_LOG
    tmpdir = os.path.join(os.path.dirname(__file__), ".replay-tmp")
    original = record(log_path, tmpdir + "-a")
    replayed, outputs = replay(log_path, tmpdir + "-b")
    print(f"recorded {len(original.output_log)} output deliveries to {log_path}")
    print(f"replayed OK: {len(outputs)} deliveries identical, "
          f"{replayed.consumer.duplicates} redeliveries deduped, "
          f"{replayed.consumer.row_count()} distinct output rows")
    for output_id, rows in outputs:
        print(f"  {output_id}: {len(rows)} rows")


if __name__ == "__main__":
    main()

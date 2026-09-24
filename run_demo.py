"""End-to-end sample run: fixture -> pipeline -> tops, deltas, snapshots,
external-segment block accounting, checkpoint/recovery, oracle verification."""
import os
import shutil
import tempfile

from topk.engine import GroupedTopK, Pipeline
from topk.fixtures import (DEFAULT_K, DEFAULT_SPEC, GROUP_FIELD,
                           generate_events, load_fixture, write_fixture)
from topk.reference import replay_events, verify_engine

FIXTURE = os.path.join("fixtures", "changes.json")


def show_top(engine, group):
    rows = engine.top(group)
    print("  %-6s %s" % (group, [(pk, list(v)) for pk, v in rows] or "[]"))


def main():
    if not os.path.exists(FIXTURE):
        write_fixture(FIXTURE)
    events = load_fixture(FIXTURE)
    print("fixture %s: %d events, commit_seq %d..%d"
          % (FIXTURE, len(events),
             min(e["commit_seq"] for e in events),
             max(e["commit_seq"] for e in events)))

    workdir = tempfile.mkdtemp(prefix="topk-demo-")
    try:
        engine = GroupedTopK("metrics", DEFAULT_K, DEFAULT_SPEC, GROUP_FIELD,
                             os.path.join(workdir, "metrics"),
                             buffer_size=4, spill_slack=4)
        pipe = Pipeline({"metrics": engine})

        # Apply commits 1..4, pin a snapshot, then apply the rest.
        head = [e for e in events if e["commit_seq"] <= 4]
        tail = [e for e in events if e["commit_seq"] > 4]
        deltas = pipe.apply_events(head)
        snap = engine.snapshot()
        print("\nsnapshot pinned at version %d" % snap.version)
        deltas += pipe.apply_events(tail)

        print("\nper-commit stable-id deltas (table=metrics):")
        for d in deltas:
            if d["groups"]:
                print("  v%-2d seq=%-2s %s" % (d["version"], d["commit_seq"],
                                                d["groups"]))

        print("\ncurrent top-%d per group:" % DEFAULT_K)
        for group in ("hot", "cold", "nil", "tie"):
            show_top(engine, group)

        print("\nold snapshot (v%d) pagination of 'hot', page size 2:"
              % snap.version)
        for off in (0, 2, 4):
            page = snap.page("hot", offset=off, limit=2)
            print("  offset=%d -> %s" % (off, [(pk, list(v)) for pk, v in page]))
        print("snapshot is unaffected by later commits: %s"
              % (snap.ids("hot") != engine.top_ids("hot")))

        stats = engine.store.stats()
        print("\nexternal segment blocks: %d written, %d read"
              % (stats["block_writes"], stats["block_reads"]))

        # Recovery: checkpoint, recover into a fresh object, re-verify.
        pipe.checkpoint()
        recovered = GroupedTopK.recover(os.path.join(workdir, "metrics"))
        records = replay_events(events)["metrics"]
        problems = verify_engine(recovered, records)
        print("recovery: version=%d, tops match full-sort oracle: %s"
              % (recovered.version, not problems))

        live = sum(engine.candidate_count(g) for g in
                   ("hot", "cold", "nil", "tie"))
        print("live candidates tracked: %d (records: %d)" % (live, len(records)))
        print("oracle verification of final state: %s"
              % ("PASS" if not problems else "FAIL %s" % (problems,)))
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    main()

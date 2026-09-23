"""End-to-end demo: fixture -> revisions -> shuffled delivery -> acks -> GC -> verify."""

import json
from pathlib import Path

from revisioned_aggregate import (
    Aggregator, DownstreamApplier, Revision, RevisionStore,
    load_change_fixture, recompute, verify,
)
from revisioned_aggregate.model import Revision as Rev


def show(title):
    print(f"\n== {title} ==")


def main():
    store = RevisionStore(":memory:")
    agg = Aggregator(store)

    show("1. ingest fixtures/changes_basic.json (corrections included)")
    events = load_change_fixture("fixtures/changes_basic.json")
    published = agg.ingest(events)
    for rev in published:
        print(f"  {rev.id}: bases={rev.bases} deltas={rev.deltas} seq={rev.seq_lo}-{rev.seq_hi}")
    print(f"  aggregate values: {agg.values}")
    print(f"  checkpoint: {store.checkpoint()}")
    print(f"  rev-t4 parents: {sorted(store.get_revision('rev-t4').parents)}  <- DAG, not a chain")

    show("2. re-ingest same fixture (idempotent, no double publish)")
    again = agg.ingest(events)
    print(f"  newly published: {again}  values still: {agg.values}")

    show("3. downstream delivery out of order (rev-t2 before rev-t1)")
    down = DownstreamApplier()
    revs = {r.id: r for r in store.revisions()}
    print(f"  deliver rev-t2 -> {down.receive(revs['rev-t2'])}  (paused, gap on rev-t1)")
    print(f"  deliver rev-t1 -> {down.receive(revs['rev-t1'])}  (gap fills, rev-t2 drains)")
    print(f"  deliver rev-t3 -> {down.receive(revs['rev-t3'])}")
    print(f"  deliver rev-t4 -> {down.receive(revs['rev-t4'])}")
    print(f"  downstream values: {down.values}")

    show("4. duplicate delivery and stale-base conflict")
    print(f"  re-deliver rev-t1 -> {down.receive(revs['rev-t1'])}  (no double add)")
    stale = Rev(id="rev-stale", txn_id="tX", bases={"txn:A": "rev-t2"},
                deltas={"txn:A": 999}, seq_lo=6, seq_hi=6)
    print(f"  stale correction (base rev-t2, head is rev-t4) -> {down.receive(stale)}")
    print(f"  conflicts: {down.conflicts}")
    print(f"  values unchanged: {down.values}")

    show("5. retract then re-add (fixtures/changes_retract.json)")
    store2, agg2 = RevisionStore(":memory:"), None
    agg2 = Aggregator(store2)
    published2 = agg2.ingest(load_change_fixture("fixtures/changes_retract.json"))
    for rev in published2:
        print(f"  {rev.id}: deltas={rev.deltas}")
    down2 = DownstreamApplier()
    for rev in reversed(published2):  # delivered in reverse order
        print(f"  deliver {rev.id} -> {down2.receive(rev)}")
    print(f"  downstream values: {down2.values} (expect {{'txn:A': 45}})")

    show("6. acks -> garbage collection of old revisions")
    for rev_id in down.applied:
        store.record_ack(f"ack-{rev_id}", rev_id)
    collected = store.gc()
    print(f"  collected: {collected}")
    print(f"  gc_log (ack evidence): {store.gc_log()}")
    print(f"  remaining revisions: {[r.id for r in store.revisions()]}")

    show("7. independent verification from raw events")
    report = verify(published, events)
    print(f"  basic fixture: {report}")
    report2 = verify(published2, load_change_fixture("fixtures/changes_retract.json"))
    print(f"  retract fixture: {report2}")
    cycle = json.loads(Path("fixtures/revision_log_cycle.json").read_text())
    bad = [Rev.from_dict(d) for d in cycle["revisions"]]
    report3 = verify(bad, [])
    print(f"  cycle fixture: ok={report3.ok} problems={report3.problems}")


if __name__ == "__main__":
    main()

"""Runnable demo: normal delivery -> crash mid-delivery -> recovery.

Verifies the guarantees end to end:
  * offset and outputs commit in the same logical transaction
  * unacked outputs are re-sent but deduped downstream (idempotency ID)
  * a failed business effect is applied exactly once, no confirmed input is lost
  * a failed batch rolls back completely and stays retryable
"""
from __future__ import annotations

import shutil
import tempfile
from pathlib import Path

from cdc_pipeline.dispatcher import Dispatcher
from cdc_pipeline.models import ChangeEvent
from cdc_pipeline.pipeline import Pipeline, load_fixture
from cdc_pipeline.sink import IdempotentSink
from cdc_pipeline.store import StateStore


def main() -> None:
    workdir = Path(tempfile.mkdtemp(prefix="cdc-demo-"))
    state_db = str(workdir / "state.db")
    sink_db = str(workdir / "sink.db")
    audit = str(workdir / "sink_audit.jsonl")
    events = load_fixture("fixtures/changes.json")
    print(f"loaded {len(events)} change events from fixtures/changes.json")

    # --- Phase 1: normal ingest + delivery of the first 3 events -----------
    store = StateStore(state_db)
    sink = IdempotentSink(sink_db, audit)
    offset = Pipeline(store).ingest(events[:3])
    acked = Dispatcher(store, sink).deliver_pending()
    print(f"[phase 1] offset={offset} delivered+acked={acked} sink_effects={sink.count()}")

    # --- Phase 2: ingest next batch, then CRASH after send, before ack -----
    offset = Pipeline(store).ingest(events[3:])
    crashing = Dispatcher(
        store, sink,
        after_send_hook=lambda _id: (_ for _ in ()).throw(
            RuntimeError("SIMULATED CRASH")),
    )
    try:
        crashing.deliver_pending()
    except RuntimeError as exc:
        print(f"[phase 2] offset={offset} crash during delivery: {exc}")
    print(f"[phase 2] outbox at crash: {store.outbox_counts()} "
          f"sink_effects={sink.count()}")
    store.close()
    sink.close()

    # --- Phase 3: restart and recover --------------------------------------
    store = StateStore(state_db)   # reopen persisted state
    sink = IdempotentSink(sink_db, audit)
    redelivered = Dispatcher(store, sink).deliver_pending()
    print(f"[phase 3] redelivered unacked={redelivered} (deduped downstream)")
    print(f"[phase 3] final: offset={Pipeline(store).offset} "
          f"outbox={store.outbox_counts()} sink_effects={sink.count()}")

    # --- Phase 4: failed batch stays retryable ------------------------------
    bad_batch = [
        ChangeEvent("tx-1007", "accounts", "a-3", None, {"balance": 100}, 7),
        ChangeEvent("tx-1008", "accounts", "a-4", None, None, 8),  # invalid
    ]
    try:
        Pipeline(store).ingest(bad_batch)
    except ValueError as exc:
        print(f"[phase 4] batch rejected: {exc}; "
              f"offset still {Pipeline(store).offset}")
    fixed = [bad_batch[0],
             ChangeEvent("tx-1008", "accounts", "a-4", None, {"balance": 60}, 8)]
    offset = Pipeline(store).ingest(fixed)
    acked = Dispatcher(store, sink).deliver_pending()
    print(f"[phase 4] retried fixed batch: offset={offset} acked={acked} "
          f"sink_effects={sink.count()}")

    assert sink.count() == 8, "every event applied exactly once"
    assert len(Path(audit).read_text().splitlines()) == 8
    print("OK: 8 unique business effects, no loss, no duplicates")
    store.close()
    sink.close()
    shutil.rmtree(workdir)


if __name__ == "__main__":
    main()

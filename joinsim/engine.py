"""The pausable / resumable partitioned hash join engine.

Correctness rules implemented here:

* A scan row whose partition filter is INCOMPLETE is never answered from the
  filter ("not in the Bloom filter" would lose rows). Depending on the
  strategy it is deferred (WAIT), spilled (BUFFER, with backpressure when
  both memory and disk quotas are exhausted) or joined without the filter
  (BYPASS, probing the versioned snapshot table directly).
* A scan row answered by a COMPLETE filter as "absent" is a non-match: dropped
  for INNER, null-padded for LEFT. Bloom false positives only add a probe.
* Output ids are stable: ``batch_id:row_idx:ordinal``. Recovery resends
  unacknowledged outputs; the test consumer dedups by id.
"""
from __future__ import annotations

import json
import os
from typing import Any, Optional

from .buffer import SpillBuffer
from .build import BuildSide, EpochEntry
from .events import BuildBarrier, BuildData, ScanBatch

WAIT, BUFFER, BYPASS = "wait", "buffer", "bypass"
INNER, LEFT = "inner", "left"
STRATEGIES = (WAIT, BUFFER, BYPASS)
JOIN_TYPES = (INNER, LEFT)

NULL_ORDINAL = "null"


class JoinEngine:
    def __init__(self, build_side: BuildSide, *, strategy: str, join_type: str,
                 null_safe: bool, sink: Any, tmpdir: str,
                 checkpoint_path: str, mem_quota_rows: int = 8,
                 disk_quota_bytes: int = 1 << 20) -> None:
        assert strategy in STRATEGIES and join_type in JOIN_TYPES
        self.build_side = build_side
        self.strategy = strategy
        self.join_type = join_type
        self.null_safe = null_safe
        self.sink = sink
        self.checkpoint_path = checkpoint_path
        self.buffer = SpillBuffer(tmpdir, mem_quota_rows, disk_quota_bytes)

        self.clock = 0
        self.crashed = False
        # batch_id -> {"epoch", "rows", "done": set[int]}
        self.open_batches: dict[str, dict] = {}
        self.pending_rows: list[dict] = []   # WAIT: deferred until filter complete
        self.blocked_rows: list[dict] = []   # BUFFER: backpressured (both quotas full)
        self.unacked: dict[str, tuple] = {}  # out_id -> output record
        self.data_log: list[BuildData] = []  # applied build data (filter rebuild)
        self.epoch_rows: dict[int, tuple] = {}  # committed snapshots (durable)

    # ------------------------------------------------------------ lifecycle
    def _check_up(self) -> None:
        if self.crashed:
            raise RuntimeError("engine is down; recover first")

    def crash(self) -> None:
        """Simulated fault: everything not checkpointed is lost. The scheduler
        discards this object and rebuilds from the last checkpoint."""
        self.crashed = True

    # -------------------------------------------------------------- epochs
    def commit_epoch(self, epoch: int, rows: tuple) -> None:
        self._check_up()
        self.build_side.commit_epoch(epoch, rows)
        self.epoch_rows[epoch] = tuple(rows)

    def _sync_epochs(self) -> None:
        self.epoch_rows = {e: self.epoch_rows[e] for e in self.build_side.epochs}

    # ------------------------------------------------------------- messages
    def handle(self, msg: BuildData | BuildBarrier) -> None:
        self._check_up()
        self.build_side.handle(msg)
        if isinstance(msg, BuildData):
            self.data_log.append(msg)
        if self.build_side.take_completions():
            self._retry_deferred()

    # ------------------------------------------------------------------ scan
    def submit(self, batch: ScanBatch) -> None:
        self._check_up()
        if batch.batch_id in self.open_batches:
            return  # duplicate submission is idempotent
        self.build_side.acquire(batch.build_epoch)
        self.open_batches[batch.batch_id] = {
            "epoch": batch.build_epoch,
            "rows": [tuple(r) for r in batch.rows],
            "done": set(),
        }
        for idx, (key, payload) in enumerate(batch.rows):
            self._offer({"batch_id": batch.batch_id, "epoch": batch.build_epoch,
                         "row_idx": idx, "key": key, "payload": payload})

    def _ready(self, rec: dict) -> bool:
        """True when the row may be answered (null shortcut or filter complete)."""
        if rec["key"] is None and not self.null_safe:
            return True
        entry = self.build_side.epochs.get(rec["epoch"])
        if entry is None:
            return False
        return entry.partitions[entry.partition_of(rec["key"])].complete

    def _offer(self, rec: dict) -> None:
        if self.strategy == BYPASS or self._ready(rec):
            self._resolve(rec)
        elif self.strategy == WAIT:
            self.pending_rows.append(rec)
        else:  # BUFFER
            if not self.buffer.try_add(rec):
                # memory AND disk quotas exhausted: backpressure, keep the row
                self.blocked_rows.append(rec)

    def _resolve(self, rec: dict) -> None:
        entry = self.build_side.epochs[rec["epoch"]]
        key = rec["key"]
        if key is None and not self.null_safe:
            self._no_match(rec)  # null never matches in this mode
        else:
            state = entry.partitions[entry.partition_of(key)]
            if state.complete and not state.filter.might_contain(key):
                self._no_match(rec)  # complete filter: authoritative negative
            else:
                self._probe(rec, entry)  # incomplete+bypass, or maybe-present
        self._mark_done(rec)

    def _probe(self, rec: dict, entry: EpochEntry) -> None:
        key = rec["key"]
        matches = entry.table.get(key, ())
        if not matches:
            self._no_match(rec)
            return
        scan_row = (key, rec["payload"])
        for ordinal, build_payload in enumerate(matches):
            self._emit(rec, ordinal, (scan_row, (key, build_payload)))

    def _no_match(self, rec: dict) -> None:
        if self.join_type == LEFT:
            scan_row = (rec["key"], rec["payload"])
            self._emit(rec, NULL_ORDINAL, (scan_row, None))
        # INNER: non-matching rows produce nothing

    def _emit(self, rec: dict, ordinal: Any, record: tuple) -> None:
        out_id = f'{rec["batch_id"]}:{rec["row_idx"]}:{ordinal}'
        self.unacked[out_id] = record
        self.sink.emit(out_id, record)

    def _mark_done(self, rec: dict) -> None:
        batch = self.open_batches[rec["batch_id"]]
        batch["done"].add(rec["row_idx"])
        if len(batch["done"]) == len(batch["rows"]):
            epoch = batch["epoch"]
            del self.open_batches[rec["batch_id"]]
            self.build_side.release(epoch)
            self._sync_epochs()

    def _retry_deferred(self) -> None:
        if self.pending_rows:  # WAIT
            still = []
            for rec in self.pending_rows:
                if self._ready(rec):
                    self._resolve(rec)
                else:
                    still.append(rec)
            self.pending_rows = still
        if len(self.buffer) or self.blocked_rows:  # BUFFER
            for rec in self.buffer.pop_all():  # drained spill files are deleted
                if self._ready(rec):
                    self._resolve(rec)
                elif not self.buffer.try_add(rec):
                    self.blocked_rows.append(rec)
            still = []
            for rec in self.blocked_rows:
                if self._ready(rec):
                    self._resolve(rec)
                elif not self.buffer.try_add(rec):
                    still.append(rec)  # still backpressured; never dropped
            self.blocked_rows = still

    # ------------------------------------------------------------------ misc
    def tick(self, n: int = 1) -> None:
        self._check_up()
        self.clock += n

    def ack(self, out_id: str) -> None:
        self.unacked.pop(out_id, None)

    def ack_all(self) -> None:
        self.unacked.clear()

    @property
    def blocked_count(self) -> int:
        return len(self.blocked_rows)

    @property
    def done(self) -> bool:
        return (not self.open_batches and not self.pending_rows
                and not self.blocked_rows and len(self.buffer) == 0)

    # ------------------------------------------------------------ checkpoint
    def checkpoint(self) -> None:
        self._check_up()
        state = {
            "clock": self.clock,
            "strategy": self.strategy,
            "join_type": self.join_type,
            "null_safe": self.null_safe,
            "current_epoch": self.build_side.current_epoch,
            "epoch_rows": {str(e): [list(r) for r in rows]
                           for e, rows in self.epoch_rows.items()},
            "open_batches": {
                bid: {"epoch": b["epoch"], "rows": [list(r) for r in b["rows"]],
                      "done": sorted(b["done"])}
                for bid, b in self.open_batches.items()
            },
            "pending_rows": self.pending_rows,
            "blocked_rows": self.blocked_rows,
            "buffer": self.buffer.to_dict(),
            "unacked": {oid: _rec_to_json(rec) for oid, rec in self.unacked.items()},
            "data_log": [[m.partition, m.epoch, m.seq, m.key, m.payload]
                         for m in self.data_log],
            "filters": {str(e): {str(p): meta for p, meta in parts.items()}
                        for e, parts in self.build_side.filters_to_dict().items()},
        }
        tmp = self.checkpoint_path + ".tmp"
        with open(tmp, "w") as fh:
            json.dump(state, fh)
        os.replace(tmp, self.checkpoint_path)  # atomic publish

    @classmethod
    def recover(cls, checkpoint_path: str, *, build_side: BuildSide, sink: Any,
                tmpdir: str, mem_quota_rows: int = 8,
                disk_quota_bytes: int = 1 << 20) -> "JoinEngine":
        with open(checkpoint_path) as fh:
            state = json.load(fh)
        engine = cls(build_side, strategy=state["strategy"],
                     join_type=state["join_type"], null_safe=state["null_safe"],
                     sink=sink, tmpdir=tmpdir, checkpoint_path=checkpoint_path,
                     mem_quota_rows=mem_quota_rows, disk_quota_bytes=disk_quota_bytes)
        engine.clock = state["clock"]
        # rebuild versioned snapshots without triggering GC mid-restore
        for epoch_s, rows in state["epoch_rows"].items():
            epoch = int(epoch_s)
            build_side.load_epoch(epoch, tuple(tuple(r) for r in rows))
            engine.epoch_rows[epoch] = tuple(tuple(r) for r in rows)
        build_side.current_epoch = state["current_epoch"]
        # rebuild data log, then partition completeness + filters
        engine.data_log = [BuildData(*m) for m in state["data_log"]]
        build_side.restore_filters(state["filters"], engine.data_log)
        # reopen batches and re-pin their epochs (running batches keep old version)
        for bid, b in state["open_batches"].items():
            build_side.acquire(b["epoch"])
            engine.open_batches[bid] = {
                "epoch": b["epoch"],
                "rows": [tuple(r) for r in b["rows"]],
                "done": set(b["done"]),
            }
        engine.pending_rows = list(state["pending_rows"])
        engine.blocked_rows = list(state["blocked_rows"])
        engine.buffer = SpillBuffer.restore(tmpdir, state["buffer"],
                                            mem_quota_rows, disk_quota_bytes)
        engine.unacked = {oid: _rec_from_json(rec)
                          for oid, rec in state["unacked"].items()}
        build_side._gc()
        engine._sync_epochs()
        # recovery may resend already-seen ids; the consumer dedups
        for oid, rec in engine.unacked.items():
            sink.emit(oid, rec)
        # some partitions may have completed before the crash: resume deferred work
        engine._retry_deferred()
        return engine


def _rec_to_json(rec: tuple) -> list:
    scan, build = rec
    return [list(scan), list(build) if build is not None else None]


def _rec_from_json(data: list) -> tuple:
    scan, build = data
    return (tuple(scan), tuple(build) if build is not None else None)

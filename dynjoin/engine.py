"""Pausable/resumable partitioned hash join with a dynamic build-side filter.

Semantics notes
---------------
* The dynamic filter of a partition is consulted only once the partition is
  complete (barrier arrived AND all preceding messages applied). Treating
  "not in the filter" as "no match" before that would drop rows.
* While the bound filter is incomplete the scan side applies one of three
  strategies (engine parameter):
    WAIT   - hold the batch in an in-memory pending queue (bounded).
    SPILL  - buffer rows in the SpillBuffer (memory + temp-disk quotas,
             backpressure when both are full, never a dropped row).
    BYPASS - execute the join now against the partial build table without
             the filter, emit the matches found so far, and keep a residual
             per row (with the build seqs already emitted) that is re-probed
             when the partition completes.
* Scan batches bind the current build snapshot (per-partition epoch objects)
  at submit time; a rebuild creates a new epoch and running batches keep
  using the old version. Old epochs are released once unreferenced.
* Output batches have stable IDs ("<scan_batch>#<n>") and stay in `unacked`
  until acked. checkpoint()/restore() pickle input position, partition
  completeness, pending scan batches and unacked outputs; recovery may
  redeliver the same IDs and the test consumer dedups them.
"""
from __future__ import annotations

import pickle
from collections import Counter, deque
from dataclasses import dataclass, field
from enum import Enum

from .build_side import BuildSide
from .filters import default_hash
from .messages import NULL, BuildBarrier, BuildData, OutputBatch
from .reference import JoinType, NullMode
from .spill import SpillBuffer


class Strategy(Enum):
    WAIT = "wait"
    SPILL = "spill"
    BYPASS = "bypass"


@dataclass
class RowState:
    key: object
    payload: object
    nkey: object
    partition: int
    done: bool = False
    matched: set = field(default_factory=set)  # build seqs emitted (BYPASS)


@dataclass
class BatchState:
    scan_batch_id: str
    snapshot: tuple          # bound BuildPartition objects, one per partition
    rows: list
    emissions: int = 0       # next output index -> stable output IDs
    pending_out: list = field(default_factory=list)


class JoinEngine:
    def __init__(self, *, num_partitions=2, join_type=JoinType.INNER,
                 null_mode=NullMode.REJECT, strategy=Strategy.WAIT,
                 filter_kind="exact", hash_fn=default_hash,
                 bloom_size=1024, bloom_hashes=3,
                 mem_quota_rows=1 << 20, disk_quota_bytes=1 << 30,
                 max_pending_batches=1 << 20, tmpdir=None):
        if strategy is Strategy.SPILL and tmpdir is None:
            raise ValueError("SPILL strategy requires a tmpdir")
        self.num_partitions = num_partitions
        self.join_type = join_type
        self.null_mode = null_mode
        self.strategy = strategy
        self.hash_fn = hash_fn
        self.max_pending_batches = max_pending_batches
        self.tmpdir = tmpdir
        self.clock = 0  # logical clock, advanced only by harness-driven calls
        self.build = BuildSide(num_partitions, filter_kind, bloom_size, bloom_hashes)
        self.spill = (SpillBuffer(tmpdir, mem_quota_rows, disk_quota_bytes)
                      if tmpdir is not None else None)
        self.batches = {}              # scan_batch_id -> live BatchState
        self.wait_queue = deque()      # scan_batch_ids held by WAIT
        self.completed_batches = set() # for idempotent re-submit after replay
        self.scan_position = 0         # input position: batches accepted
        self.unacked = {}              # output_id -> OutputBatch
        self.outbox = deque()          # output_ids not yet polled
        self.stats = Counter()

    # ------------------------------------------------------------------ util
    def _nkey(self, key):
        return NULL if key is None else key

    def _partition_of(self, nkey):
        return self.hash_fn(nkey, 0) % self.num_partitions

    # -------------------------------------------------------------- scan side
    def submit_scan_batch(self, batch_id, rows):
        """All-or-nothing accept. Returns False on backpressure (retry later)."""
        self.clock += 1
        if batch_id in self.completed_batches or batch_id in self.batches:
            self.stats["dup_scan_batches"] += 1
            return True  # idempotent redelivery after recovery
        snapshot = tuple(self.build.current)
        row_states = []
        for key, payload in rows:
            nkey = self._nkey(key)
            row_states.append(RowState(key, payload, nkey, self._partition_of(nkey)))

        def needs_wait(rs):
            if self.null_mode is NullMode.REJECT and rs.key is None:
                return False  # NULL can never match: resolvable immediately
            return not snapshot[rs.partition].complete

        pending_rows = [rs for rs in row_states if needs_wait(rs)]
        pending_index = {id(rs): i for i, rs in enumerate(row_states)}

        if pending_rows and self.strategy is Strategy.WAIT:
            if len(self.wait_queue) >= self.max_pending_batches:
                self.stats["backpressure"] += 1
                return False
        staged = []
        if pending_rows and self.strategy is Strategy.SPILL:
            for rs in pending_rows:
                token = self.spill.try_add(
                    rs.partition, (batch_id, pending_index[id(rs)]))
                if token is None:  # both quotas full: roll back, backpressure
                    self.spill.rollback(staged)
                    self.stats["backpressure"] += 1
                    return False
                staged.append(token)
            self.stats["spilled_rows"] += len(staged)

        batch = BatchState(batch_id, snapshot, row_states)
        for bp in snapshot:
            bp.refs += 1
        self.batches[batch_id] = batch
        self.scan_position += 1

        for rs in row_states:
            bp = snapshot[rs.partition]
            if self.null_mode is NullMode.REJECT and rs.key is None:
                self._emit_no_match(batch, rs)
            elif bp.complete:
                self._resolve_complete(batch, rs, bp)
            elif self.strategy is Strategy.BYPASS:
                self._partial_probe(batch, rs, bp)
            # WAIT / SPILL rows stay pending (SPILL rows already staged)
        if self.strategy is Strategy.WAIT and any(not rs.done for rs in row_states):
            self.wait_queue.append(batch_id)
        self._cut_outputs(batch)
        self._finalize(batch)
        return True

    # ------------------------------------------------------------- build side
    def deliver(self, msg):
        self.clock += 1
        bp = self.build.route(msg)
        if bp is None:
            self.stats["dropped_messages"] += 1  # unknown/released epoch
            return
        was_complete = bp.complete
        if isinstance(msg, BuildData):
            if not bp.apply(msg):
                self.stats["duplicate_messages"] += 1
        elif isinstance(msg, BuildBarrier):
            bp.set_barrier(msg.last_seq)
        if bp.complete and not was_complete:
            self._on_complete(bp)

    def rebuild(self, partition, epoch):
        """Version switch: install a fresh epoch for one partition."""
        self.clock += 1
        current = self.build.current[partition]
        if current.epoch >= epoch:
            self.stats["stale_rebuilds"] += 1
            return current.epoch  # idempotent replay of the same switch
        self.build.current[partition] = self.build.new_partition(partition, epoch)
        self.build.by_version[(partition, epoch)] = self.build.current[partition]
        self.build.maybe_release(current)
        return epoch

    # --------------------------------------------------------- row resolution
    def _resolve_complete(self, batch, rs, bp):
        """Resolve a row against a complete partition."""
        if self.strategy is not Strategy.BYPASS:
            self.stats["filter_lookups"] += 1
            if not bp.get_filter(self.hash_fn).might_contain(rs.nkey):
                self.stats["filter_negatives"] += 1
                self._emit_no_match(batch, rs)  # LEFT: null-padded, not dropped
                return
        self.stats["table_probes"] += 1
        matches = bp.probe(rs.nkey)
        if self.strategy is Strategy.BYPASS:
            new = [m for m in matches if m[2] not in rs.matched]
        else:
            new = list(matches)
        for _key, payload, seq in ((m[0], m[1], m[2]) for m in new):
            batch.pending_out.append((rs.key, rs.payload, _key, payload))
            rs.matched.add(seq)
        if not rs.matched:
            self._emit_no_match(batch, rs)
        else:
            rs.done = True

    def _partial_probe(self, batch, rs, bp):
        """BYPASS: probe the incomplete build table now, keep the residual."""
        self.stats["table_probes"] += 1
        for key, payload, seq in bp.probe(rs.nkey):
            batch.pending_out.append((rs.key, rs.payload, key, payload))
            rs.matched.add(seq)
        # row stays pending until the partition completes

    def _emit_no_match(self, batch, rs):
        if self.join_type is JoinType.LEFT:
            batch.pending_out.append((rs.key, rs.payload, None, None))
        rs.done = True

    def _on_complete(self, bp):
        partition = bp.partition_id
        if self.strategy is Strategy.WAIT:
            for batch_id in list(self.wait_queue):
                batch = self.batches.get(batch_id)
                if batch is None:
                    self.wait_queue.remove(batch_id)
                    continue
                self._resolve_pending(batch, bp, partition)
        elif self.strategy is Strategy.SPILL:
            for batch_id, row_index in self.spill.drain(partition):
                batch = self.batches.get(batch_id)
                if batch is None or batch.snapshot[partition] is not bp:
                    # spilled row belongs to a different epoch of this partition
                    self.spill.requeue(partition, (batch_id, row_index))
                    continue
                self._resolve_complete(batch, batch.rows[row_index], bp)
                self._cut_outputs(batch)
                self._finalize(batch)
        elif self.strategy is Strategy.BYPASS:
            for batch in list(self.batches.values()):
                self._resolve_pending(batch, bp, partition)

    def _resolve_pending(self, batch, bp, partition):
        resolved = False
        for rs in batch.rows:
            if (not rs.done and rs.partition == partition
                    and batch.snapshot[partition] is bp):
                self._resolve_complete(batch, rs, bp)
                resolved = True
        if resolved:
            self._cut_outputs(batch)
        self._finalize(batch)

    # ------------------------------------------------------------ output side
    def _cut_outputs(self, batch):
        if not batch.pending_out:
            return
        output_id = f"{batch.scan_batch_id}#{batch.emissions}"
        batch.emissions += 1
        output = OutputBatch(output_id, batch.scan_batch_id,
                             list(batch.pending_out), self.clock)
        batch.pending_out.clear()
        self.unacked[output_id] = output
        self.outbox.append(output_id)
        self.stats["output_batches"] += 1

    def _finalize(self, batch):
        if not all(rs.done for rs in batch.rows):
            return
        for bp in batch.snapshot:
            bp.refs -= 1
            self.build.maybe_release(bp)
        self.batches.pop(batch.scan_batch_id, None)
        self.completed_batches.add(batch.scan_batch_id)
        try:
            self.wait_queue.remove(batch.scan_batch_id)
        except ValueError:
            pass

    def poll(self):
        out = []
        while self.outbox:
            output = self.unacked.get(self.outbox.popleft())
            if output is not None:
                out.append(output)
        return out

    def ack(self, output_id):
        self.unacked.pop(output_id, None)

    # ----------------------------------------------------- checkpoint/restore
    def checkpoint(self):
        """Snapshot input position, partition completeness, pending scan
        batches and unacked outputs into a picklable byte string."""
        if self.spill is not None:
            self.spill.flush()
        return pickle.dumps(self)

    @classmethod
    def restore(cls, data, hash_fn=default_hash):
        engine = pickle.loads(data)
        engine.hash_fn = hash_fn
        return engine

    def __getstate__(self):
        state = self.__dict__.copy()
        state["hash_fn"] = None  # injectable lambdas are not picklable
        return state

    def close(self):
        if self.spill is not None:
            self.spill.close()

package diskpq

/*
Crash recovery and power-loss guarantees (and their boundaries)

Files (all inside the caller-supplied directory):

  - wal.log: append-only, CRC-protected write-ahead log. One JSON record per
    line, framed as "<8-hex-crc> <json>\n".
  - snapshot.json: compact point of all live tasks plus the last assigned Seq.
  - snapshot.json.tmp: temp file used to publish the snapshot atomically.

What is guaranteed

  1. Process crash / kill -9 after a method returned nil:
     The record was appended and fsync'ed, so restart (Open) replays it.
  2. Crash between fsync and in-memory application:
     Recovery applies the durable record; the on-disk state is authoritative.
  3. Power loss that tears the tail of wal.log:
     Every record carries a CRC32C. Replay applies only the intact prefix and
     truncates the rest. A partially written line can never be half-applied.
  4. PopBatch atomicity:
     One "pop" record containing the whole batch is fsync'ed before any item
     leaves the queue. Crash before that fsync => all items are still queued
     (nothing lost); crash after => the batch is gone exactly once (no
     duplicates, no resurrection). If the commit returns an error, the
     in-memory heap is rolled back and the caller still owns every task.
  5. Updates cannot resurrect deletes:
     Update only touches a currently live id (in the API and in replay). A
     stale update for a deleted id is ignored. Replay order (enqueue, ...,
     delete) plus this rule means tombstoned tasks never reappear.
  6. Stable sequence numbers:
     Seq is a strictly monotonic per-queue counter, stored on every enqueue
     and in every snapshot as last_seq. Equal-priority ordering by Seq is
     therefore byte-identical before and after restart, and after compaction.
  7. Snapshot publication is atomic:
     snapshot.json.tmp is fully written and fsync'ed, then renamed over
     snapshot.json, then the directory is fsync'ed. A crash leaves either the
     old or the new snapshot, never a torn one. Recovery reads snapshot +
     whatever WAL records follow it, so no committed task is lost either way.

Boundaries / what is NOT guaranteed

  - This is single-node durability. There is no replication; losing the
    directory (disk failure, accidental deletion, corruption of the entire
    disk) loses the queue.
  - fsync honors the OS/filesystem contract, not more. Storage that lies about
    cache flushes (e.g. volatile disk write cache ignoring flush commands) can
    lose acknowledged writes on true power loss. That is a hardware boundary.
  - The directory fsync required for durable rename is meaningful on local
    POSIX filesystems (ext4, xfs, APFS, ...). Network/loop/overlay filesystems
    may provide weaker semantics; Open does not detect that.
  - The CRC detects torn and bit-flipped WAL tails, not adversarial corruption;
    a corrupt snapshot.json is reported as an error instead of silently
    starting empty (that would risk silent task loss).
  - A process crash mid-operation returns no success, so the durable state
    equals "the operation never happened". Callers that need exactly-once side
    effects for popped batches must make their own downstream action idempotent
    using Item.ID / Item.Seq; this package guarantees the queue side only.
*/

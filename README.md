# LSM MemTable Flush: WAL + MemTable -> Immutable -> L0 SSTable

TypeScript implementation of the LSM-tree write path: a write-ahead log (WAL)
plus a layered MemTable-to-SSTable persistence state machine, using only the
Node.js standard library (no dependencies).

## Architecture

```
                        foreground write path (never blocks on flush)
                        +------------------------------------------+
  put/delete ---------> | 1. append to active WAL segment (write)  |
                        | 2. apply to active MemTable              |
                        | 3. if size >= 4MB threshold: rotate()    |
                        +-------------------+----------------------+
                                            | O(1): swap table + WAL segment
                                            v
   MemTable state machine      ACTIVE --> IMMUTABLE --> FLUSHING --> FLUSHED
                                            |                    |
                                            |   background single-threaded
                                            |   sequential flush scheduler
                                            |   (one promise chain, FIFO)
                                            v                    v
                              new ACTIVE MemTable      l0/00000N.sst
                              + new WAL segment        + old WAL segment reclaimed
```

- `src/memtable.ts` — sorted in-memory table with byte-size accounting and
  tombstones; states `active | immutable | flushing | flushed`.
- `src/wal.ts` — CRC32-checksummed WAL segments (`wal/00000N.log`); torn or
  corrupt tails are detected on replay. Segment close (fsync) runs off the
  foreground path so rotation never stalls writes.
- `src/sstable.ts` — sorted, self-describing L0 table files (`l0/00000N.sst`),
  fsynced before the corresponding WAL segment is reclaimed.
- `src/engine.ts` — `LsmEngine`: threshold-triggered rotation, single-threaded
  sequential flush scheduler (one promise chain => strict FIFO into L0), read
  path across active -> immutables (newest first) -> L0 (newest first), and
  crash recovery by WAL replay.

## Requirements

Node.js >= 22.6 (runs TypeScript directly via type stripping; no build step,
no dependencies). Developed and verified on Node v23.9.0.

## Run

```bash
npm test     # node:test acceptance + unit tests
npm run demo # writes ~9.7MB against a 4MB threshold, prints rotation/flush events
```

## Sample run (`npm run demo`)

```
writing 20,000 entries (~9.21 MB) against a 4MB memtable threshold...

[rotate #1] memtable froze at 4MB -> immutable queue (pending flushes: 1)
[rotate #2] memtable froze at 4MB -> immutable queue (pending flushes: 2)

foreground writes done: 20,000 puts in 705.3ms (28,355 ops/s, never blocked on flush)
[flush  #1] immutable memtable -> l0/000001.sst (WAL segment reclaimed)
[flush  #2] immutable memtable -> l0/000002.sst (WAL segment reclaimed)

final state: rotations=2, l0 files=2, wal segments left=1
data integrity: 20,000/20,000 keys verified through the read path
after reopen from disk: key-00012345 found (440 bytes)

on-disk layout:
  wal/000003.log  1.18 MB
  l0/000001.sst  3.80 MB
  l0/000002.sst  3.80 MB
```

## Test results (`npm test`, Node v23.9.0)

```
✔ basic put/get/delete with tombstones
✔ rotation freezes memtable and flushes immutables to L0 sequentially
    [acceptance] 20000 puts in 7023.1ms (2848 ops/s), p99 put latency 449us,
                 8853 puts during in-flight flush
✔ acceptance: two 4MB threshold hits, 2 immutable rotations, sequential L0
  flush, no write stall, WAL rotated
✔ WAL recovery after crash (no clean close)
✔ persistence across clean restart: SSTable + WAL both consulted
✔ newest write wins across active, immutable and L0 layers

tests 6 | pass 6 | fail 0
```

The acceptance test drives ~9.7MB of writes at a real 4MB threshold and asserts:
exactly 2 rotations, exactly 2 sequential L0 flushes (`000001.sst`,
`000002.sst`), writes continuing while a flush is in flight (p99 put latency
in the microsecond range), WAL segments of flushed tables reclaimed (only the
active segment remains), and full data integrity both in-memory and after
reopening from disk.

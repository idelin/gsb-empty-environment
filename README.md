# extsort — checkpointed external stable sort

Sorts text segments (`key\tvalue` lines) larger than memory: sorted runs
are spilled to disk, then merged k-way with resumable checkpoints.

## Design

- **Runs** (`GenerateRuns`): inputs are read in order; every record gets a
  globally increasing **uint64 sequence number** (full precision, so
  stability never degrades with input size). In-memory batches are sorted
  by `(key, seq)` and written as length-prefixed binary run files. Each
  run's size + SHA-256 is recorded in `runs.json`.
- **Merge** (`Merge`): a deterministic plan of fan-in-bounded merge tasks
  (function of runs × fan-in only, so restarts replay it exactly). Each
  task merges its inputs with a min-heap ordered by `(key, seq)` — equal
  keys keep original input order.
- **Checkpoints**: every `CheckpointEvery` output records a task flushes
  and fsyncs its output, then atomically (tmp + rename + dir fsync)
  publishes a checkpoint binding:
  - **input digests** — size + SHA-256 of every input segment;
  - **cursors** — per-input byte offset / record count of the next
    unread record (cursors track records actually *written*, not the
    decoder read-ahead sitting in the merge heap);
  - **published output prefix** — length + record count + SHA-256 of the
    synced prefix. Bytes past the prefix are unsynced buffer residue and
    are never trusted.
- **Resume**: a task with a manifest is skipped after re-verifying its
  output digest. Otherwise its checkpoint is validated — input digests
  are re-hashed and compared, the output prefix is re-hashed and the file
  truncated back to the published length (discarding torn tails) — then
  the merge continues from the cursors. Any digest mismatch aborts with an
  error instead of trusting unsynced state.
- **Open files**: all file handles go through a counted wrapper; peak
  simultaneous opens during a merge is `fanIn + 2` (inputs + output +
  checkpoint temp file).

## CLI

```
extsort sort  -dir WORK -o OUT [-mem N] [-fanin N] [-ckpt N] INPUT...
extsort merge -dir WORK -o OUT [-fanin N] [-ckpt N]   # resume
extsort cat   FILE                                    # decode as text
```

## Tests

`go test ./...` — the fault-injection test counts every checkpoint cut
point of a clean merge, then for **each** cut point crashes the merge
right after publishing (leaving an unsynced garbage tail in the output),
resumes, and requires the final output to be byte-identical to one
in-memory stable sort (`sort.SliceStable` on the key). Further tests
cover tie stability across run boundaries, uint64 sequence precision,
tampered input/prefix rejection, empty input, and the peak-open-files
bound.

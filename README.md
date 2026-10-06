# logtail

A Go log tailer plus a replayable file-operation model used to verify it.

## Design

- `vfs.go` — in-memory filesystem. Every file has a stable identity
  (`Ino`) that survives renames; `Rename` has POSIX replace semantics;
  every mutation is recorded as an `Op` so any session can be replayed
  with `Replay(ops)`. The FS counts live handles so tests can assert
  none leak.
- `tailer.go` — the tailer. The cursor is (file identity, offset):
  - different `Ino` at the watched path (rotation / same-path
    replacement): the old handle is drained to EOF and closed before
    the new file is opened;
  - size below the current offset (truncation): the file enters a new
    generation and is re-read from offset zero.
  - a trailing partial line is buffered and published only once its
    newline arrives; if its generation ends first it is dropped.

## Checking

`model_test.go` contains an independent specification model that derives
the expected line stream directly from filesystem snapshots. Randomized
sessions (seeded, 300 seeds x 150 ops) are replayed op-by-op with a poll
after each op, asserting:

- the delivered stream equals the model's stream — every complete line
  of every generation is delivered exactly once, in order, with correct
  (generation, identity) attribution;
- every opened handle is closed by the end;
- the recorded op log replays to the identical stream.

On failure the test dumps the op log as Go source for verbatim replay.

Run: `GO111MODULE=on go test ./...`

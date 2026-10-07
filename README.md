# logtail

A polling log tailer (`tail.go`) plus a replayable file-operation model
(`model_test.go`) that verifies its two core invariants.

## Tailer design

- **Cursor = file identity + offset.** Identity is `(dev, ino)` from
  `fstat`; the path is only a lookup key. The cursor survives renames.
- **Complete lines only.** Bytes are buffered until a newline arrives; a
  trailing partial line is carried across polls and published only once
  completed.
- **Rotation/replacement:** when the path resolves to a different identity,
  the old handle is first drained to EOF (a partial line completed in the
  rotated file is still published under the old generation), then closed,
  then the new file is opened as a new generation.
- **Truncation:** if the current file shrinks below the cursor, that file
  enters a new generation and is re-read from offset 0.
- **Generations** are numbered monotonically; every emitted `Line` carries
  its generation.

## Model-based test

`model_test.go` replays an operation script (append / truncate / rotate /
rotate+create / atomic replace / remove / sync) against a real temp
directory and an abstract reference model in lockstep. After every `sync`
it asserts:

1. the tailer's emitted lines equal the model's — i.e. every complete line
   of every generation is read **exactly once**;
2. the tailer holds at most one open handle, and zero after `Close`;
3. (`TestNoHandleLeak`) the process fd count in `/dev/fd` is unchanged
   after hundreds of rotations/replacements/removals.

Rotated/replaced files are kept alive with hard links so inode reuse cannot
forge a false identity collision.

## Run

```sh
go test ./...                 # full suite
TAIL_SEED=12345 go test ./... # replay a specific random script
```

`TestModelRandom` prints the seed of any failing script; `TestScripted`
pins the edge cases (partial lines across polls, truncation, rotation with
pending data, replacement, remove+reappear) deterministically.

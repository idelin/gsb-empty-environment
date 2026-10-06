# binpatch

A small Go library for binary patches with atomic, streaming application.

## Patch model

A patch transforms a **baseline** byte string into a **target** byte string
using two operation kinds:

- **Copy** — copies a range `[offset, offset+length)` of the *original
  baseline* into the output. Copies never read from already produced output;
  offsets always address the baseline.
- **Insert** — appends literal bytes carried inside the patch.

The patch header binds the patch to its input and result:

- SHA-256 of the baseline
- exact target length
- SHA-256 of the target

## Wire format

All integers are big-endian.

```
magic        [8]byte   "BPTCH001"
baselineHash [32]byte
targetLength uint64
targetHash   [32]byte
opCount      uint64
ops          ...       tag 0x01 copy:   offset uint64, length uint64
                       tag 0x02 insert: length uint64, raw bytes
```

## Guarantees

`ApplyFile(baselinePath, patchReader, dstPath)`:

- streams the patch from any `io.Reader`; inserts are never buffered whole;
- verifies the baseline hash before writing anything;
- rejects copy ranges outside the baseline (`ErrOutOfBounds`), truncated
  patches (`ErrTruncated`), unknown op tags, and trailing garbage;
- verifies the produced target length and hash;
- stages output in a temp file in the destination directory and publishes it
  with an atomic rename — a failed apply never leaves a partial result at
  the destination and never modifies a pre-existing file there.

## Usage

```go
patch, _ := binpatch.PatchBytes(baseline, target) // build
err := binpatch.ApplyFile("baseline.bin", bytes.NewReader(patch), "out.bin")
```

## Tests

```
go test ./...
```

Covers randomized round trips (random baselines, randomly mutated targets),
baseline-only copy semantics, out-of-bounds copies, truncation at every
byte, corrupted headers, malformed op streams, and atomic publish behaviour.

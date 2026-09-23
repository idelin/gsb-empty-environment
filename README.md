# rangecoder

Segmented 32-bit range coding (arithmetic coding variant) in pure Go
standard library — no compression libraries involved. Supports model
checkpoints, random-access decoding from a checkpoint, and resumable
encoding that is bit-identical to a continuous run.

## Design

- `rangecoder.go` — byte-oriented 32-bit range coder (Subbotin/7-Zip
  style carry handling). Encoder flushes 5 `shiftLow`s; decoder reads 5
  init bytes, which drops the leading dummy byte naturally.
- `model.go` — adaptive order-0 frequency model (257 symbols: 256 bytes +
  EOF, rescale at total > 2^14) plus its verifiable serialization.
- `coder.go` — segmented `Encoder`/`Decoder`. `FinishSegment` encodes an
  EOF symbol and flushes, making every segment an independent stream while
  the model keeps adapting. `Decoder.NextSegment` mirrors this for
  sequential decoding; `NewDecoderFromCheckpoint` gives random access.

## Checkpoint format

All header integers big-endian; payload integers are uvarints.

```
magic     4 bytes   "RGCP"
version   uint16    format version (currently 1, mismatches rejected)
length    uint32    payload length in bytes (capped at 1 MiB)
checksum  uint32    CRC32-IEEE over version || length || payload
payload   numSymbols uvarint (must be 257)
          total      uvarint (must equal sum of freqs)
          freq[]     numSymbols uvarints (each >= 1)
```

Verification on load: magic, version, exact length, CRC32, and payload
consistency (symbol count, freq bounds, sum == total, no trailing bytes).

## Guarantees (covered by tests)

- Round-trip of segments of any size, including empty.
- Resuming encoding from a checkpoint produces a bitstream identical to a
  continuous run with the same segmenting policy.
- Random access: a later segment decodes from its checkpoint alone.
- Corruption of an earlier segment never pollutes a later segment's model.
- Decoding output is bounded by a configurable quota (`SetQuota`);
  corrupt/truncated/adversarial streams terminate within it.
- Checkpoints with wrong version, bad magic/length/checksum, or an
  inconsistent model are rejected with distinct errors.

## Usage

```go
enc := rangecoder.NewEncoder()
enc.Encode(part1)
seg1 := enc.FinishSegment()
cp := enc.Snapshot()          // verifiable model checkpoint
enc.Encode(part2)
seg2 := enc.FinishSegment()

// Sequential: model carries over.
dec := rangecoder.NewDecoder(seg1)
out1, _ := dec.ReadAll()
dec.NextSegment(seg2)
out2, _ := dec.ReadAll()

// Random access: only the checkpoint and seg2 are needed.
dec2, _ := rangecoder.NewDecoderFromCheckpoint(cp, seg2)
dec2.SetQuota(1 << 20)
out2b, _ := dec2.ReadAll()
```

## Run

```
go test ./... -v
go run ./cmd/demo
```

# blkconv — streaming block-encoding transcoder

`blkconv` converts a block-encoded stream from one wire encoding to
another **without materializing the plaintext**, preserving the original
SHA-256 digest, declared length and metadata, with bounded memory and
backpressure. Standard library only; both RLE block codecs are
implemented by hand (no compression library is used).

## Wire formats

Both formats share the same header layout and differ in block framing,
checksum width and payload codec. All integers are big-endian.

### Header (both formats)

| field    | size  | meaning                              |
|----------|-------|--------------------------------------|
| magic    | 4     | `BLKA` or `BLKB`                     |
| version  | 1     | format version, currently `1`        |
| flags    | 1     | must be `0`                          |
| metaLen  | 2     | JSON metadata length (≤ 32768)       |
| origLen  | 8     | declared plaintext length in bytes   |
| digest   | 32    | SHA-256 of the plaintext             |
| meta     | metaLen | JSON object, preserved by transcode |

### Records

Every record starts with a 1-byte tag: `0x01` = block, `0x02` = commit
trailer. Blocks carry at most 65536 plaintext bytes.

**Format A block**: `compLen u32 | rawLen u32 | crc32(plaintext) u32 | payload`
— payload is RLE-A (PackBits-style: token <128 = literal run of token+1
bytes, token ≥128 = run of token−125 copies of the next byte).

**Format B block**: `compLen uvarint | rawLen uvarint | crc64(plaintext) u64 | payload`
— payload is RLE-B (token high bit set = literal of (token&0x7f)+1 bytes,
else run of (token&0x7f)+2 copies of the next byte).

**Commit trailer**: `0x02 | "CMT1"/"CMT2" | blockCount (u32 / uvarint) |
streamChecksum`. The stream checksum (CRC-32 for A, CRC-64-ISO for B)
covers every byte from the header through `blockCount`. It is the
*completion marker*: a decoder that does not see a valid trailer reports
`ErrNoCommit`, and a transcoder/encoder only emits it after the whole
stream — length and digest included — has verified.

## Guarantees

- **No full materialization**: one 64 KiB block in flight per stage;
  transcoding pipelines decode→encode through a channel of depth 2, so a
  slow or failing writer back-pressures the reader (≈ 256 KiB total).
- **Bad input never publishes**: per-block length + checksum checks,
  declared-vs-actual length and digest verification, and the commit
  trailer is written last. Output files are staged in `NAME.part` and
  atomically renamed on success; any failure removes the uncommitted
  tail (`CommitTarget.Abort`).
- **Bounded decode output**: `NewDecoder(r, maxBytes)` rejects streams
  that would decode past the budget (`ErrBudget`); per-block wire and
  raw lengths are also bounded (RLE-A ≤ 1+1/128 expansion, RLE-B ≤ 4/3).
- **Explicit versioning**: unknown magic, version or flags are rejected.

## Usage

```
blkconv pack   -fmt a|b -in FILE -out FILE [-meta k=v ...]
blkconv xcode  -in FILE -out FILE [-tfmt a|b] [-max N]
blkconv unpack -in FILE -out FILE [-max N]
blkconv info   -in FILE
```

`xcode` defaults to the opposite encoding of the source. `-max` bounds
decoded bytes (suffixes K/M/G, default 1G).

## Sample run

```
$ blkconv pack -fmt a -meta author=demo -meta purpose=transcode-sample -in sample.txt -out sample.blka
packed: format=A version=1 origLen=329893 sha256=df1d469a…1ba873bed
  meta author=demo
  meta purpose=transcode-sample
$ blkconv xcode -in sample.blka -out sample.blkb
transcoded A->B: format=B version=1 origLen=329893 sha256=df1d469a…1ba873bed
$ blkconv unpack -in sample.blkb -out roundtrip.txt
unpacked: format=B version=1 origLen=329893 sha256=df1d469a…1ba873bed
$ cmp sample.txt roundtrip.txt && echo ROUNDTRIP-OK
ROUNDTRIP-OK

# corrupt input: no commit marker, no output, temp tail cleaned
$ blkconv xcode -in corrupt.blka -out nope.blkb
error: blkconv: corrupt stream: block checksum mismatch
$ ls nope.blkb nope.blkb.part
ls: nope.blkb: No such file or directory
ls: nope.blkb.part: No such file or directory

# bounded decode budget
$ blkconv unpack -in sample.blkb -out toobig.txt -max 100K
error: blkconv: decode output budget exceeded: limit 102400 bytes
```

## Tests

`go test ./...` — 13 tests, all passing:

```
--- PASS: TestRLECodecsRoundTrip            (hand-rolled RLE A/B property round trip)
--- PASS: TestStreamRoundTripBothFormats    (random-chunk writes, both formats)
--- PASS: TestTranscodeRandomChunksDecodeEquals (A->B and B->A, digest+meta preserved)
--- PASS: TestCorruptPayloadRejected
--- PASS: TestTruncationRejectedNoCommit
--- PASS: TestDeclaredLengthMismatchRejected (decoder- and encoder-side)
--- PASS: TestDigestLieRejected
--- PASS: TestDecodeBudgetBounded
--- PASS: TestWriteFailureCleansUncommittedTail (injected writer failure -> Abort, no Commit)
--- PASS: TestFileTargetCleanupOnBadInput   (no final file, no .part tail)
--- PASS: TestFileRoundTripAndCleanupOnSuccess
--- PASS: TestEmptyStream
--- PASS: TestWorstCaseExpansionAccepted    (RLE-B 4/3 worst case within bound)
ok  	blkconv	0.450s
```

## Layout

- `format.go` — header/block/trailer framing, versions, checksums, errors
- `rle.go` — hand-rolled RLE-A and RLE-B block codecs
- `stream.go` — streaming `Encoder` / `Decoder` (io.Writer / io.Reader)
- `transcode.go` — bounded-memory `Transcode` pipeline, `CommitTarget`, file helpers
- `cmd/blkconv` — CLI
- `blkconv_test.go` — go test suite

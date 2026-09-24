# incremental-utf8

Incremental UTF-8 decoder for chunked input, with a compressed bidirectional
byte ↔ UTF-16 offset map, serializable checkpoints, and two strategies for
invalid sequences. Pure TypeScript, zero dependencies, runs on Node.js ≥ 22.6
(native type stripping) and tests use the built-in `node:test` runner.

## Features

- **Chunked incremental decoding** — push `Uint8Array` chunks of any size;
  chunk boundaries never change the emitted events, the offset map, or error
  positions. A 4-byte emoji fed as four 1-byte chunks still yields exactly one
  character.
- **Two invalid-sequence strategies**
  - `strict`: throws `Utf8DecodeError` with `kind`
    (`invalid-lead` / `invalid-continuation` / `truncated`) and the **absolute
    byte offset** of the offending sequence.
  - `replace`: emits one U+FFFD per invalid sequence, consuming the maximal
    valid prefix (WHATWG-compatible, verified against `TextDecoder`).
- **Truncation is only decided at `finish()`** — an incomplete sequence at a
  chunk boundary is pending, not an error.
- **Compressed offset map** — segments are coalesced into runs (100k ASCII
  chars = 1 run); lookups are `O(log runs)` binary search, so random access
  never re-decodes the prefix.
- **UTF-16 public offsets, surrogate-safe** — queries never split a surrogate
  pair; range queries snap outward to character boundaries.
- **Explicit many-to-one coverage** — when a multi-byte invalid sequence is
  replaced by a single U+FFFD, range queries return the covered interval in
  `replacements`.
- **Serializable checkpoints** — `checkpoint()` returns JSON-safe state
  (decoder + offset map + SHA-256 digest of the consumed prefix).
  `resume(checkpoint, prefix)` verifies the digest and refuses to continue a
  different stream with `CheckpointMismatchError`.

## API sketch

```ts
import { IncrementalUtf8Decoder } from './src/index.ts';

const d = new IncrementalUtf8Decoder('replace'); // or 'strict'
const events = [
  ...d.push(chunk1),   // DecodeEvent[]: kind, byteStart/End, u16Start/Length, text
  ...d.push(chunk2),
  ...d.finish(),       // truncated tail resolved here
];

d.map.byteToU16(7);          // byte offset -> UTF-16 offset
d.map.u16ToByte(4);          // UTF-16 offset -> byte offset (never splits a pair)
d.map.byteRangeToU16(15, 18); // -> { start, end, replacements: [coverage...] }
d.map.u16RangeToByte(3, 4);   // -> snapped outward to character boundaries

const cp = d.checkpoint();    // JSON-serializable
const d2 = IncrementalUtf8Decoder.resume(cp, consumedPrefixBytes);
```

## Run

```sh
npm run demo    # node examples/demo.ts
npm test        # node --test test/*.test.ts
```

## Demo output (excerpt)

```
=== 1. Incremental decode with replace strategy (chunk size 3) ===
input bytes : [41 c3 a9 e2 82 ac f0 9f 98 80 78 79 0d 0a 7a 0a e2 82]
kind         bytes       utf16   text
text         [ 6, 10)   3+2  "😀"
replacement  [16, 18)  11+1  "�"

=== 2. Offset queries (UTF-16 space, surrogate-safe) ===
byteToU16(7)  = 3   (inside the 4-byte emoji at bytes 6..10)
u16ToByte(4)  = 6   (middle of the emoji surrogate pair -> snaps to pair start)
byteRangeToU16(15, 18) -> utf16 [10, 12)
  many-to-one coverage: bytes [16, 18) -> utf16 [11, 12)

=== 3. Serializable checkpoint, bound to the input prefix digest ===
resume with a different stream rejected: prefix digest mismatch: refusing to continue a different stream

=== 4. Strict strategy: absolute error positions ===
threw Utf8DecodeError: kind=invalid-continuation byteOffset=5 sequence=[0xc3]
finish() threw Utf8DecodeError: kind=truncated byteOffset=0
```

## Tests

`node --test` — 25 tests, all passing:

- **Exhaustive ≤ 2-byte inputs**: all 65,792 one- and two-byte sequences ×
  every chunking × both strategies, compared against the independent
  whole-buffer `TextDecoder` (fatal and non-fatal).
- **Curated + fuzzed 3–4 byte inputs**: boundary code points (U+0800, U+D7FF,
  U+D800 surrogates, U+FFFF, U+10000, U+10FFFF), overlongs, invalid leads,
  mixed valid/invalid — every chunking (2ⁿ⁻¹ compositions) and every
  truncation prefix, plus 3,000 deterministic random inputs.
- **Chunk-boundary invariance**: identical events/text/map for every chunk
  size; strict error position equals the first replacement position in
  replace mode.
- **Offset map**: boundary/interior lookups, surrogate-pair safety, outward
  range snapping, explicit many-to-one coverage intervals, monotonicity,
  run compression, random access on a large mixed stream vs. a linear
  reference, JSON round-trip.
- **Checkpoints**: resume mid-sequence reproduces the one-shot result
  exactly; digest mismatch or wrong prefix length is rejected; strict errors
  after resume keep absolute positions.
- **LF and CRLF** content is covered in decoding and mapping tests.

```
ℹ tests 25
ℹ pass 25
ℹ fail 0
```

## Layout

- `src/decoder.ts` — incremental state machine, strategies, errors, checkpoints
- `src/offset-map.ts` — run-compressed bidirectional byte ↔ UTF-16 map
- `src/index.ts` — public exports
- `test/` — `node:test` suites
- `examples/demo.ts` — runnable demo

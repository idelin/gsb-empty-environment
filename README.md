# incremental-utf8

Incremental UTF-8 decoder for arbitrarily chunked input, with a compressed
bidirectional byte ↔ UTF-16 offset map, serializable checkpoints, and strict /
replace error strategies. Pure TypeScript on Node.js (>= 22.6, runs `.ts`
directly via type stripping); tests use the built-in `node:test` runner.

## Features

- **Incremental decoding** — `push(chunk)` accepts any chunking; chunk
  boundaries never change the result (verified exhaustively, see below).
- **Two error strategies** — `strict` throws `Utf8DecodeError` carrying the
  **absolute byte offset** of the ill-formed sequence; `replace` emits one
  U+FFFD per maximal ill-formed subpart (same consumption rules as the
  platform `TextDecoder`). A truncated tail sequence is only resolved by
  `finish()`.
- **Offset map** — every decoded unit updates a block-compressed map
  (128 units/block, delta-friendly). Random access binary-searches a block
  and scans at most one block — it never re-decodes or re-scans the prefix.
- **Bidirectional range queries** — `byteRangeToTextRange` /
  `textRangeToByteRange`. Text offsets are UTF-16 code units; queries never
  split a surrogate pair (they snap outward to the character). When
  replacement collapses several bytes into one character (many-to-one), the
  covered interval is returned explicitly in `covered[]`.
- **Checkpoints** — `checkpoint()` returns a JSON-serializable snapshot bound
  to the SHA-256 digest of the consumed prefix. `resume(checkpoint, prefix)`
  verifies the digest and refuses to continue a different stream.

## API sketch

```ts
import { IncrementalUtf8Decoder } from './src/index.ts';

const decoder = new IncrementalUtf8Decoder({ strategy: 'strict' }); // or 'replace'
decoder.push(bytes1);
decoder.push(bytes2);              // may split a multi-byte sequence anywhere
const { text, byteLength, utf16Length } = decoder.finish();

decoder.map.byteToUtf16(10);                  // byte offset -> UTF-16 offset
decoder.map.utf16ToByte(4);                   // UTF-16 offset -> byte offset
decoder.map.byteRangeToTextRange(3, 9);       // { start, end, collapsed, covered }
decoder.map.textRangeToByteRange(1, 5);       // { byteStart, byteEnd, textStart, textEnd }

const checkpoint = decoder.checkpoint();      // JSON-serializable
const resumed = IncrementalUtf8Decoder.resume(checkpoint, consumedPrefix);
```

`Utf8DecodeError` exposes `byteOffset` (absolute offset of the first byte of
the ill-formed sequence), `detectedAt`, and `kind` (`'invalid' | 'truncated'`).

## Run

```sh
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## Sample run (`npm run demo`)

```
=== 1. Incremental decode (chunk boundaries are irrelevant) ===
decoded text:
  "你好，世界 🌍\r\n第二行：café\n"
lengths:
  { "bytes": 40, "utf16": 19 }

=== 2. Byte <-> UTF-16 range queries ===
emoji byte range -> text range:
  { "start": 6, "end": 8, "collapsed": true,
    "covered": [ { "byteStart": 16, "byteEnd": 20, "textStart": 6, "textEnd": 8, "reason": "multibyte" } ] }
point inside surrogate pair snaps out (utf16):
  { "byteStart": 16, "byteEnd": 20, "textStart": 6, "textEnd": 8 }
CRLF byte range -> text range:
  { "start": 8, "end": 10, "collapsed": false, "covered": [] }

=== 3. Strict strategy: absolute error positions ===
caught:
  Utf8DecodeError: Invalid UTF-8 sequence at absolute byte offset 7 (kind=invalid, detectedAt=9)
finish() caught:
  Utf8DecodeError: Truncated UTF-8 sequence at absolute byte offset 3 (kind=truncated)

=== 4. Replace strategy: explicit collapse intervals ===
decoded text:
  "A�B�"
byte range 1..6 -> text range:
  { "start": 1, "end": 4, "collapsed": true,
    "covered": [
      { "byteStart": 1, "byteEnd": 3, "textStart": 1, "textEnd": 2, "reason": "replacement" },
      { "byteStart": 4, "byteEnd": 6, "textStart": 3, "textEnd": 4, "reason": "replacement" } ] }

=== 5. Checkpoint / resume (bound to stream digest) ===
checkpoint (trimmed):
  { "version": 1, "consumed": 8, "utf16Pos": 8, "digest": "81cc65f539f37792…" }
resumed decode:
  "first-half|second-half"
foreign stream rejected:
  checkpoint digest mismatch: refusing to resume a different input stream

=== 6. Compressed blocks: O(block) random access ===
blocks / units scanned for tail query:
  { "blocks": 625, "scanned": 127 }
```

## Test results (`npm test`, Node v23.9.0)

```
✔ exhaustive: all inputs up to 4 bytes, all chunkings and truncations (5693.8ms)
  enumerated 135424 inputs, 1344000 chunked decodes
ℹ tests 16
ℹ pass 16
ℹ fail 0
```

The exhaustive suite enumerates, for every input of up to 4 bytes (all 1- and
2-byte strings over the full byte alphabet; all 3- and 4-byte strings over a
16-symbol edge-case alphabet), every truncation position and every chunking,
and compares strict and replace output against `TextDecoder` decoding the
whole prefix at once — 1,344,000 chunked decodes, all identical to the
independent whole-buffer decoder.

## Layout

- `src/utf8_decoder.ts` — incremental decoder, error strategies, checkpoints
- `src/offset_map.ts` — block-compressed bidirectional offset map
- `test/decoder.test.ts` — directed tests (emoji in 4 chunks, truncation at
  `finish()`, surrogate safety, LF/CRLF, checkpoint digest binding, …)
- `test/exhaustive.test.ts` — enumeration vs. `TextDecoder`
- `examples/demo.ts` — the sample run above

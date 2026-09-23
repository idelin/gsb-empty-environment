# slz — sliding-window LZ77 with minimum-bit parsing

Pure-Go (stdlib only) sliding-window LZ compressor/decompressor.

## Design

- **Window / bounds**: `WindowSize = 4096` (max distance), `MinMatch = 3`,
  `MaxMatch = 258`. Distances beyond the window are evicted and never
  referenced; the decoder rejects them.
- **Bounded match index**: hash chains over 3-byte prefixes, walk limited
  to `maxChain = 32` entries, chain cut at the window boundary.
- **Real cost model**: literal = 9 bits (1 flag + 8 payload);
  match(len, dist) = `1 + gamma(len-MinMatch+1) + gamma(dist)` bits
  (Elias gamma). Longest match is therefore *not* automatically best —
  a long match at a far distance can cost more than a short near one.
- **Minimum-bit parse**: backward DP costs every reachable
  (length, distance) pair at every position (`optimalParse`). Fixed
  tie-break: literal before match, then nearer distance, then shorter
  length. `greedyParse` (longest match, nearest tie-break) is kept for
  comparison; `TestGreedyCounterexample` shows it losing by 21 bits.
- **Overlapping back-copy**: matches with `dist < length` (including
  distance 1) are copied byte-by-byte and extend runs correctly.
- **Checkpoints**: the stream is split into chunks; each chunk header
  carries `crc32` of the decoder's dictionary window (last
  `min(WindowSize, produced)` bytes) at chunk start, binding the chunk to
  the exact dictionary state it was encoded against.

## Stream format

```
header:  magic "SLZ" | version u8 (=1) | totalLen uvarint | crc32(payload) 4B
chunk*:  dictCRC 4B | tokenCount uvarint | token bits (byte-aligned)
token:   literal  0 | byte(8)
         match    1 | gamma(len-MinMatch+1) | gamma(dist)
```

Version, declared payload length, and payload CRC32 are explicit in the
header. `Decode(r, maxOut)` bounds output: streams declaring more than
`maxOut` bytes are rejected up front, input reads are capped
proportionally, tokens that would overrun the declared length are
rejected, as are matches referencing not-yet-produced bytes or distances
beyond the evicted window.

## Tests (`go test ./...`)

- Exhaustive minimum-cost proof: for **all** strings over `{a,b}` up to
  length 12 and over `{a,b,c}` up to length 8, the DP parse cost equals
  the brute-force minimum over every possible parse, and never exceeds
  greedy.
- Greedy-failure counterexample (`TestGreedyCounterexample`).
- Random compressible data, random chunk sizes, roundtrip + encoder
  window-invariant check.
- Distance-1 run extension, checkpoint/version/magic/checksum rejection,
  not-yet-produced reference rejection, evicted-window distance
  rejection, output-limit enforcement, gamma roundtrip.

## Sample run

```
$ go run ./cmd/slzdemo
original:  2600 bytes
encoded:   110 bytes (4.2% of original)
roundtrip: true
```

## Test result

```
$ go test ./...
ok  	slz	3.272s
```

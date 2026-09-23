# huffcodec

Deterministic, length-limited canonical prefix coding in pure Go (standard
library only — no compression libraries are called).

## Features

- **Exact optimal code lengths** via the package-merge algorithm, under a
  configurable maximum code length (`CodeLengths`, `MaxCodeLength` = 32).
- **Deterministic**: equal-frequency symbols are tie-broken by byte order
  (lower symbol value gets the shorter-or-equal code); identical input always
  produces identical output bytes.
- **Edge cases**: empty input yields an empty (header-only) stream; a single
  used symbol gets a 1-bit code, never an undecodable empty codeword.
- **Table validation**: oversubscribed (Kraft sum > 1) and incomplete
  (Kraft sum < 1) code tables are detected and rejected with distinct errors
  (`ErrOversubscribed`, `ErrIncomplete`); `KraftScaled` and `Cost` give the
  exact Kraft sum and optimal-cost reference.
- **Canonical codes** (`CanonicalCodes`): DEFLATE-style assignment ordered by
  (length, symbol).
- **Layered streaming decode** (`StreamDecoder`): an 8-bit root table with
  chained sub-tables for longer codes; bit state is kept across calls, so
  input may arrive in arbitrary chunks (cross-byte state).
- **Shared output budget**: one budget is decremented across all `Decode`
  calls (`ErrQuotaExceeded`); `DecodeAll` takes a `maxOut` quota so decoded
  output is bounded.
- **Finish semantics**: `Finish` distinguishes a truncated tail that ends
  mid-codeword (`ErrIncompleteCodeword`) from bits matching no codeword at
  all (`ErrInvalidCode`). A truncated tail never reports success.
- **Explicit wire format** with magic, version, code table, uncompressed
  length and CRC-32 checksum; tampered tables, corrupted payloads, spliced
  streams and trailing garbage are all rejected.

## Wire format

```
byte  0-1 : magic 'H','C'
byte  2   : format version (1)
uvarint   : number of coded symbols (nSym)
nSym * 2  : (symbol byte, code length byte) pairs, sorted by symbol
uvarint   : uncompressed length in bytes
4 bytes   : CRC-32 (IEEE) of the uncompressed data, big-endian
rest      : canonical-code bitstream, MSB first, zero-padded final byte
```

## Layout

- `huffman.go` — package-merge optimal length-limited code lengths, Kraft
  sum, cost, table validation
- `canonical.go` — canonical code assignment
- `table.go` — layered (multi-level) decode table
- `bits.go` — MSB-first bit writer
- `stream.go` — `StreamDecoder` with cross-byte state and shared budget
- `format.go` — versioned wire format `Encode` / `DecodeAll`
- `cmd/huffdemo` — runnable demo

## Run sample

```
$ go run ./cmd/huffdemo
input: "the quick brown fox jumps over the lazy dog. the quick brown fox jumps over the lazy dog, again and again!"
symbols: 30 distinct, 106 bytes, max code length 16

sym  char  freq  len  code
 32   ' '    20     2  00
 33   '!'     1     6  110000
 44   ','     1     7  1111110
 ...
122   'z'     2     6  111110

Kraft sum: 4294967296/2^32 = 1.000000 (1.0 = complete)
optimal cost: 477 bits payload (4.50 bits/symbol)
wire size: 129 bytes (header + payload), ratio 1.217

DecodeAll round-trip: true
streamed round-trip (5-byte chunks): true
```

## Tests

`go test ./...` — 24 tests, all passing:

```
--- PASS: TestFormatRoundTrip
--- PASS: TestFormatDeterministic
--- PASS: TestTruncatedTailNeverSucceeds        # every truncation point fails
--- PASS: TestTamperedTableRejected             # perturbed lengths + spliced streams
--- PASS: TestCorruptedPayloadRejected          # every payload byte flipped
--- PASS: TestTrailingGarbageRejected
--- PASS: TestVersionAndMagicChecked
--- PASS: TestDecodeOutputQuotaBounded
--- PASS: TestSingleSymbolAndEmptyFormat
--- PASS: TestEmptyInput
--- PASS: TestSingleSymbolGetsOneBitCode        # no undecodable empty code
--- PASS: TestTieBreakByByteOrder               # equal freqs -> byte order
--- PASS: TestKraftCompleteForBuiltCodes
--- PASS: TestMaxCodeLengthEnforced             # Fibonacci weights, limit 10
--- PASS: TestMaxBitsTooSmall
--- PASS: TestValidateLengths                   # over/incomplete detection
--- PASS: TestCanonicalCodesAssignment
--- PASS: TestOptimalityByEnumeration           # brute-force optimal cost check
--- PASS: TestLengthLimitedMatchesBruteForce
--- PASS: TestStreamRoundTripArbitraryChunks    # chunk sizes 1..1000
--- PASS: TestLayeredTableLongCodes             # 12-bit codes via sub-tables
--- PASS: TestOutputBudgetSharedAcrossCalls
--- PASS: TestFinishDistinguishesTruncatedFromInvalid
--- PASS: TestStreamRejectsBadTables
PASS
ok  	huffcodec
```

The optimality tests enumerate every legal code-length vector (Kraft sum
exactly 1, lengths within the limit) for small alphabets and confirm the
constructed code's weighted cost equals the brute-force minimum, including
tight length limits on skewed Fibonacci weights.

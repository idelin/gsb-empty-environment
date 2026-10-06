# varint — strict streaming codec for telemetry integers

Unsigned values are encoded as little-endian base-128 varints: every
byte carries a 7-bit payload, and its most-significant bit is the
continuation flag. Signed values are mapped to unsigned ones with
ZigZag encoding first, so small-magnitude negatives stay short.

## Strictness

Values are limited to 64 bits (at most 10 bytes; the tenth byte may
carry only bit 63). The decoder rejects:

- `ErrTruncated` — input ends mid-value,
- `ErrNonCanonical` — non-minimal encoding (a longer form than the
  value requires),
- `ErrOverlong` — more than 10 bytes,
- `ErrOverflow` — a value that does not fit in 64 bits.

## Error ranges and frame recovery

Every failure is an `*varint.Error` carrying the exact byte range of
the malformed value (`Offset`, `Consumed`). The next value resumes at
the explicit frame boundary `Error.Boundary()` (= `Offset + Consumed`):

- Buffer API: `DecodeAt(buf, off)` returns `next`; on error
  `next == off + Consumed`, so decoding continues at `buf[next:]`.
- Stream API: `Decoder` consumes exactly the malformed bytes, so the
  read after an error resumes at the boundary and
  `Decoder.Offset() == err.Boundary()`.

## Verification

The test suite cross-checks the codec against arbitrary-precision
arithmetic (`math/big`): all 1–3 byte encodings exhaustively, the
extremes straddling every 7-bit block edge (2^(7k)±1), and the 64-bit
limits, plus rejection and stream-resume behavior.

# dkimcanon

Streaming DKIM body canonicalization for Go, implementing the `simple` and
`relaxed` body algorithms from [RFC 6376](https://www.rfc-editor.org/rfc/rfc6376)
(sections 3.4.4–3.4.6), plus the body-hash (`bh=`) computation from 3.7.

It is built for mail relays that must verify the body digest **while**
forwarding the stream, without buffering the whole message.

## Features

- Both algorithms: `Simple` and `Relaxed`.
- Optional `l=` body length limit (`WithBodyLength`), applied as a byte
  prefix of the canonical stream per RFC 6376 section 3.5.
- Delayed trailing empty lines: blank lines are held back until more body
  content proves they are interior, then replayed; at `Close` the tail is
  reduced exactly as the RFC mandates.
- Bounded memory: pending blank lines are tracked with a counter, never
  buffered. A million trailing `CRLF`s use the same memory as one, and the
  write/close hot path performs no heap allocations.
- Chunk-independent output: any split of the body across `Write` calls,
  including boundaries inside `CRLF` and whitespace runs, yields identical
  canonical bytes and therefore an identical digest.
- Strict line-ending validation: bare `CR` and bare `LF` produce
  `ErrInvalidNewline` and a sticky error, so such a body can never reach a
  sink as a DKIM-verified message.

## Quick start

```go
import (
    "crypto/sha256"
    "example.org/offlinecli/dkimcanon"
)

// One-shot convenience.
canon, err := dkimcanon.Canonicalize(dkimcanon.Relaxed, body)
bh, err    := dkimcanon.BodyHash(dkimcanon.Relaxed, sha256.New, body)

// Streaming: feed raw body chunks, then close.
h := sha256.New()
d := dkimcanon.NewDigester(dkimcanon.Relaxed, h)
if _, err := io.Copy(d, mailBodyReader); err != nil {
    return err // includes ErrInvalidNewline
}
if err := d.Verify(expectedBH); err != nil {
    return err // ErrDigestMismatch or ErrInvalidNewline
}
```

## Relay integration

Use `NewDigesterWithSink` to hash and forward the *same* canonical byte
stream through a write callback:

```go
h := sha256.New()
d := dkimcanon.NewDigesterWithSink(dkimcanon.Simple, h,
    dkimcanon.WriteFunc(func(p []byte) error {
        _, err := downstream.Write(p)
        return err
    }),
)
// ... d.Write(chunk) for each chunk read from the upstream connection ...
if err := d.Verify(signatureBodyHash); err != nil {
    // Do NOT deliver as verified: err is ErrInvalidNewline or ErrDigestMismatch.
}
```

## Body length limit (`l=`)

`WithBodyLength(n)` delivers at most `n` bytes of canonicalized body. The
limit counts canonical octets (not raw input octets). `Truncated()` reports
whether the limit was reached and `Emitted()` the number delivered; a verifier
uses these to decide whether the signed region covered the whole body.

## Error handling

- `ErrInvalidNewline` (wrapped in `*InvalidNewlineError` with the offending
  offset): the body is not valid for DKIM canonicalization. The error is
  sticky — all later writes and `Verify` fail, so an illegal-newline body can
  never be delivered as a verified message.
- `ErrDigestMismatch`: the recomputed `bh=` differs from the expected value.

## Design notes

- `Canonicalizer` is an `io.Writer`; line endings are validated before any
  byte reaches the sink.
- Package-level scratch slices are never resliced in place, so truncation
  cannot corrupt state shared across calls.
- The empty body canonicalizes to `CRLF` under `simple` and to the empty
  string under `relaxed`, matching RFC 6376.

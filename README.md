# DKIM body length truncation

`example.org/netsec/q0360` is an importable Go package for mail-reading
callers that need to verify DKIM signatures without silently accepting a
truncated body.

## Behavior

- The zero-value policy rejects every signature that carries an `l=` tag,
  even when `l=` happens to equal the body length.
- With `Options{AllowBodyLengthTruncation: true}`, a shorter `l=` is accepted
  only when the covered canonicalized prefix matches `bh=`. The result sets
  `Truncated`, reports `SignedBodyLength`, and exposes the unsigned suffix in
  `UncoveredBody` (canonicalized) and `UncoveredRawBody` (as received).
- A signature without `l=` is verified against the entire canonicalized body.
- `l=` greater than the canonicalized body, a bad `bh=`, or a bad header
  signature is an error.
- Header field order, folding, duplicate fields, and `simple`/`relaxed`
  canonicalization are preserved by parsing the raw message.

## Usage

```go
raw, _ := os.ReadFile("message.eml")

result, err := netsec.VerifyBytes(raw, lookupKey, netsec.Options{})
if errors.Is(err, netsec.ErrBodyLengthNotAllowed) {
	// The message carries l=; decide whether to allow truncation.
}
if err == nil && result.Truncated {
	// result.UncoveredRawBody was not authenticated by this signature.
}
```

`lookupKey` may return an `*rsa.PublicKey`, an `ed25519.PublicKey`, a PEM
public key, or a DKIM DNS TXT record such as `v=DKIM1; k=rsa; p=...`.
`VerifyWithKey` bypasses lookup when the key is already available.

The test suite signs generated messages with `l=` shorter than the body and
covers the default rejection, opt-in acceptance with uncovered-tail reporting,
full-body signatures, appended-tail detection, and tampered prefixes.

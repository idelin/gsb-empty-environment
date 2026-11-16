# BLS verifier registration and aggregate verification

`example.org/netsec/q0817` is an importable Go package for consensus nodes that
use BLS aggregate signatures. Pairing arithmetic and hash-to-curve are supplied
by the caller, so the package does not depend on a particular curve library.

## Security properties

- A public key is added only after a proof of possession verifies. The proof is
  a BLS signature over the public key's exact `Bytes()` encoding.
- Proof-of-possession and ordinary signatures use different, non-empty domain
  separation tags. A `Domains` value with identical tags is rejected.
- Registering an already-present public key is an idempotent no-op.
- Public keys and signatures at infinity are rejected.
- For one-message aggregates, the aggregate public key is computed internally
  from registered keys selected by the bitmap. Callers cannot supply it.
- The bitmap must contain exactly one byte per registered verifier. A non-zero
  byte selects that verifier.
- A bitmap selection below the threshold is rejected.
- For multi-message aggregates, every message must be pairwise distinct.
  Duplicate messages are rejected before pairing.

## API

```go
type Point interface {
	Add(q Point) Point
	Equal(q Point) bool
	IsInfinity() bool
	Bytes() []byte
}

type PairingValue interface {
	Mul(b PairingValue) PairingValue
	Equal(b PairingValue) bool
}

type PairingEngine interface {
	Generator() Point
	Infinity() Point
	HashToCurve(message, domain []byte) (Point, error)
	Pair(a, b Point) PairingValue
}
```

Construct a registry with caller-chosen domains or the defaults:

```go
domains := netsec.Domains{
	Signature:         []byte("my-network:block-signature:v1"),
	ProofOfPossession: []byte("my-network:pop:v1"),
}
set, err := netsec.NewVerifierSet(engine, domains)
// Or: set, err := netsec.NewDefaultVerifierSet(engine)
```

Create a proof of possession by signing `publicKey.Bytes()` under
`domains.ProofOfPossession`, then register it:

```go
if err := set.Register(publicKey, proofOfPossession); err != nil {
	// The key is not in the verifier set.
}
```

Verify a same-message aggregate:

```go
// bitmap has one byte per registered verifier, in registration order.
err := set.VerifySameMessage(bitmap, message, aggregateSignature, threshold)
```

Verify a multi-message aggregate. Messages correspond to selected verifiers in
ascending signer-index order:

```go
err := set.VerifyDistinctMessages(bitmap, messages, aggregateSignature, threshold)
```

Alternatively, name signers explicitly:

```go
entries := []netsec.SignerMessage{
	{Signer: 0, Message: messageA},
	{Signer: 2, Message: messageB},
}
err := set.VerifyDistinctSignerMessages(entries, aggregateSignature, threshold)
```

Errors are exported sentinels such as `ErrInvalidProofOfPossession`,
`ErrDomainCollision`, `ErrBitmapLengthMismatch`, `ErrThresholdNotMet`,
`ErrDuplicateMessage`, and `ErrInfinityPublicKey`, so callers can distinguish
policy failures with `errors.Is`.

## Tests

The test suite uses an exponent-based mock pairing with the same algebraic
identities as a real bilinear pairing. It covers rogue public keys without
proofs of possession, colliding domain tags, bitmap length mismatches,
duplicate messages, insufficient thresholds, duplicate registration, and
infinity keys.

```sh
go test ./...
go test -race ./...
```

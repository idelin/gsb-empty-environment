# macaroon

`macaroon` is an importable verification package for a Go resource service. It
authenticates a request's primary macaroon together with the discharge
macaroons for its third-party caveats.

Import path:

```go
import "example.org/netsec/q0696/macaroon"
```

## Contract

- Primary signature: `sig0 = HMAC-SHA256(rootKey, identifier)`.
- First-party caveat: `sig = HMAC(previousSignature, caveat)`.
- Third-party caveat: `sig = HMAC(previousSignature, vid || cid)`.
- Each third-party caveat needs one discharge whose identifier equals its
  `cid`; `Verifier.RecoverRootKey(vid)` resolves the discharge root key.
- Discharge binding: `HMAC-SHA256(32 zero bytes, primarySig || dischargeSig)`;
  any mismatch rejects the request.
- A discharge is valid only for its own `cid` and may be used at most once
  per verification (reuse returns `ErrDischargeReused`).
- First-party predicates are decided by `Verifier.Check`; an unrecognized
  predicate returns `false` and fails with `ErrPredicate`.
- `time < RFC3339` caveats are handled internally; when several are present
  the earliest deadline wins, and `now >= deadline` returns `ErrTime`.
- More than 32 caveats across the primary and all discharges returns
  `ErrTooManyCaveats`.

## Usage

```go
verifier := &macaroon.Verifier{
    Check: func(predicate []byte) bool {
        // Return false for any predicate the service does not understand.
        return string(predicate) == "user = alice"
    },
    RecoverRootKey: func(vid []byte) ([]byte, error) {
        // Look up the discharge-service root key sealed inside vid.
        return rootKeyStore.Get(vid)
    },
    // Now is optional; omit it to use time.Now.
}

req := macaroon.Request{
    Primary:    primaryMacaroon,
    Discharges: boundDischarges,
}
if err := verifier.Verify(requestRootKey, &req); err != nil {
    // Deny the request. Use errors.Is to distinguish ErrBinding,
    // ErrDischargeReused, ErrPredicate, ErrTime, etc.
}
```

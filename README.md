# crlstate

`crlstate` is an importable Go package for cross-generation partitioned CRL
aggregation and certificate status auditing.

It is designed for validation hosts that receive many full partition CRLs and
multi-generation delta CRLs from multiple roots, including `indirectCRL`
delegation handovers and partition split/merge transitions.

## Guarantees

- A delta is only applied to the exact full/base or previous delta CRL number it
  declares, within the same `(issuer, root, epoch, partition, serial, reason)`
  channel.
- Coverage cannot combine different roots, non-adjacent epochs, or half-filled
  partitions. Every required reason bit must be covered for every instant of
  the certificate lifetime.
- A proven blocking revocation wins over any coverage gap. A `certificateHold`
  is non-blocking only when a matching `removeFromCRL` release names the same
  issuer, serial and suspended credential (`HoldID`).
- Split and merge migrations must bind old/new roots and describe exactly equal
  serial×reason regions, with no gaps or overlaps. Both roots sign the
  declaration.
- Two valid, content-conflicting manifests under the same
  `(IssuerID, RevocationEpoch)` lock the issuer and retain both signatures and
  signing digests as fork evidence.

## Use

```go
import "formal.local/w3b-e0d5e53855/crlstate"

roots := crlstate.NewEd25519Roots()
_ = roots.AddRoot("root-2026", publicKey)

agg := crlstate.NewAggregator(roots)
if err := agg.AddManifest(manifest); err != nil {
    // Includes ErrBadSignature, ErrEpochRegression, ForkError, ...
}

res := agg.Check(crlstate.Certificate{
    IssuerID: "issuer-permanent-id",
    Serial:   serial,
    Start:    cert.NotBefore,
    End:      cert.NotAfter,
})
switch res.Status {
case crlstate.StatusGood:
case crlstate.StatusRevoked:
case crlstate.StatusLocked:
case crlstate.StatusUnknown:
    // res.Missing describes the uncovered serial/reason/time region.
}

cache := crlstate.NewCache(agg)
defer cache.Close()
res = cache.Check(query)
```

`CheckAt` evaluates an instantaneous point for timeline audits. `Validate`
converts the three terminal outcomes into `ErrRevoked`, `ErrIssuerLocked`, or
`ErrStatusUnknown` for path-validation callers.

## Tests

`go test ./...` exercises full/delta continuity, epoch isolation, reason
partitioning, cross-root isolation, released holds, split/merge migration,
dual-root signatures, fork locking, exact 2-D migration boundaries, incremental
cache invalidation, and an independent point-by-point reference timeline for
all scenarios.

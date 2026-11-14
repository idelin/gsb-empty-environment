# View-bound aggressive negative cache

`netsec` is an importable Go package for recursive resolvers that need to
synthesize DNSSEC-authenticated `NXDOMAIN` and `NODATA` answers for split-DNS
tenant views. It is designed so an NSEC or NSEC3 interval authenticated in one
view can never answer a question in another view.

## Trust boundary

The package does not implement DNSSEC cryptography. A trusted validator
supplies a `Response` whose `Attestation.ChainVerified` and
`SignaturesVerified` flags are set and whose `RRSIG.Verified` fields are set.
The cache then enforces the resolver-specific rules:

- view, authenticated apex, class, parameter set, key epoch, and canonical
  query name are all part of the `CacheKey`;
- every witness signature must cover the expected NSEC/NSEC3 type, be signed by
  the apex, use the current key generation, and be inside its validity window;
- NSEC3 hash algorithm, iteration count, salt, next-hash length, base32hex
  owner label, and opt-out flag are checked against the attestation and policy;
- proof expiry, key rotation/revocation, policy generation changes, and clock
  rollback all invalidate cached entries.

## Negative semantics

NSEC and NSEC3 are evaluated independently. Both paths prove the closest
encloser, next-closer non-existence, and wildcard non-existence before
synthesizing `NXDOMAIN`. A matching wildcard produces `NODATA` only when the
requested type is absent from its authenticated bitmap; a wildcard that can
answer the type is never turned into a negative answer.

For NSEC3, the evaluator additionally distinguishes:

- matching empty-non-terminal NSEC3 records (empty bitmap) from ordinary
  `NODATA`;
- matching wildcard owners from wildcard-absence coverage;
- delegation names from ordinary owner names;
- opt-out DS `NODATA` coverage from opt-out coverage of a regular name. An
  opt-out interval is never allowed to manufacture `NXDOMAIN`, so a possible
  unsigned delegation cannot be hidden.

`Answer.Witnesses` contains the exact authenticated NSEC/NSEC3 RRsets used,
with their role (`closest-encloser`, `next-closer-cover`, `wildcard-cover`,
`wildcard-match`, `exact-match`, `empty-non-terminal`, `opt-out-cover`, or
`delegation`), canonical RDATA, and RRSIG metadata.

## Usage

```go
clock := netsec.ClockFunc(func() time.Time { return time.Now() })
cache := netsec.NewCache(clock, netsec.WithMaxEntries(10000))

err := cache.SetPolicy(netsec.Policy{
    View:               "tenant-a",
    Generation:         7,
    KeyGeneration:      3,
    MaxNSEC3Iterations: 2500,
    AllowNSEC3OptOut:   true,
})

service := netsec.NewService(cache)
err = service.RegisterUpstream("tenant-a", netsec.UpstreamFunc(
    func(ctx context.Context, q netsec.Query) (*netsec.Response, error) {
        // The resolver's DNSSEC validator returns an authenticated denial
        // response for this view only.
        return validator.NegativeResponse(ctx, q)
    },
))

answer, err := service.Query(ctx, netsec.Query{
    View:  "tenant-a",
    Name:  netsec.MustParseName("missing.corp.example."),
    Type:  netsec.TypeA,
    Class: netsec.ClassIN,
})
```

`answer.Result` is `netsec.ResultNXDOMAIN` or `netsec.ResultNODATA`.
`answer.CacheHit` reports whether the upstream was bypassed. A `NODATA` entry
is reusable only for types absent from its authenticated bitmap; an `NXDOMAIN`
entry applies to every ordinary query type.

Use `Cache.Ingest` directly when the resolver already has a validated proof,
`Cache.Lookup` for hit-only reads, or `EvaluateResponse` to validate a proof
without storing it.

## Tests

```sh
go test -race ./...
```

The suite covers same-name cross-view isolation, NSEC3 opt-out unsigned
delegations, wildcard existence, empty non-terminals, key rotation, policy
changes, clock rollback, proof expiry, parameter-set binding, legitimate cache
hits, and the attack in which another view's interval would otherwise cover the
query name.

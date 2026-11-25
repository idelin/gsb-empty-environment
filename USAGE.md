# Usage

## Manager side

```go
key := ed25519.NewKeyFromSeed(fixedSeed)
z := zoneauth.NewZone("example", key)
_ = z.PutRecords(zoneauth.Name{"org"}, zoneauth.Record{Type: "A", Value: "203.0.113.7"})

b := zoneauth.NewPatchBuilder(z)
_ = b.AddDelegation(zoneauth.Name{"org", "new"},
    []zoneauth.Record{{Type: "NS", Value: "ns1.new.org"}})
_ = b.SetWildcard(zoneauth.Name{"org"},
    []zoneauth.Record{{Type: "A", Value: "203.0.113.9"}})
b.RotateKey(newKey)
patch := b.Sign()
b.AttachClientProofs(patch, zoneauth.Name{"org", "gone"})
```

## Client side

```go
trusted := map[string]ed25519.PublicKey{
    zoneauth.KeyIDFor(oldPub): oldPub,
}
verdicts, verified, err := zoneauth.Reevaluate(patch, cachedRoot, trusted, entries)
if err != nil {
    // Signature, sequence, handoff or evidence failure; reject the patch.
}
for _, v := range verdicts {
    switch v.Kind {
    case zoneauth.VerdictReusable:
        // Keep the cached answer.
    case zoneauth.VerdictChanged:
        // Replace it with v.NewAnswer.
    case zoneauth.VerdictPending:
        // Evidence is incomplete; issue a fresh query instead of guessing.
    }
}
_ = verified // contains the extended trusted key set and new root.
```

## Reference interpreter

`zoneinterp.Tree` answers queries against a fully materialized zone without
any signatures or proof machinery. Use it as the semantic oracle and compare
its `Resolve` output with `zoneauth.Proof.DeriveAnswer` in host tests.

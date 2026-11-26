# pathsec

`pathsec` is a dependency-free Go package for joint RPKI ROA, ASPA and
BGPsec validation plus bounded attack-path search. It uses only the Go
standard library and never accesses the network.

## Joint validation

```go
validator := pathsec.NewStrictValidator()

validator.ROA.MustAdd(pathsec.ROA{
    Prefix: "203.0.113.0/24", MaxLength: 24, OriginASN: 65001,
})
validator.ASPA.MustPublish(65001, 65002)
validator.Keys.TrustEd25519(65002, ed25519PublicKey)

result := validator.Verify(pathsec.VerifyRequest{
    Prefix:   "203.0.113.0/24",
    ASPath:   []uint32{65002, 65001},
    Role:     pathsec.RoleCustomer, // route received from a customer AS
    LocalASN: 65000,
    BGPsec:   proof,
})

if result.Status != pathsec.StatusValid {
    // Inspect result.ROA, result.ASPA and result.BGPsec.
}
```

`Role` is interpreted as the receive-side perspective by default. Set
`Config.RoleIsLocal` when using RFC 9234 local-role semantics, or set
`VerifyRequest.LocalRole` explicitly.

## ASPA empty states

```go
table := pathsec.NewASPATable()
_ = table.PublishEmpty(65010)             // published, explicit AS0/empty set
_ = table.Publish(65020, 65100, 65200)    // published provider set
table.Delete(65030)                        // unpublished => Unknown
```

## Adversarial search

```go
g := pathsec.NewGraph().
    AddCustomerProvider(65001, 65000).
    AddCustomerProvider(65001, 65002).
    AddCustomerProvider(65003, 65002).
    Corrupt(65001)

report, err := g.CrossValidate(pathsec.SearchRequest{
    ObserverASN: 65000,
    Role:        pathsec.RoleCustomer,
    Prefix:      "203.0.113.0/24",
    VictimASN:   65003,
})
```

`AttackGraph` rejects graphs with more than 12 AS nodes. `Search` performs
bounded BFS; `EnumerateAllAttackChains` is the independent small-graph
reference model, and `CrossValidate` compares their chain identity sets.

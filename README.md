# VEX proof acceptance for Go release hosts

This module is an importable, data-only verifier for a narrow but strict VEX
claim:

> the publisher says the registered vulnerable implementation did not enter the
> final Go image.

It consumes only signed symbol summaries and signed link manifests. It never
reads, executes, or disassembles a binary.

## Model

An object is identified by the exact triple:

```go
vex.ObjectID{
    Compiler:     "gc",
    SourceDigest: "sha256:...",
    BuildConfig:  "linux/amd64,go1.22",
}
```

A matching symbol name is not evidence of a matching object. Reachability and
the final verdict are always about the exact object identity.

Symbol definitions are `strong`, `weak`, or `group`. A group is a choose-one
implementation set: one object is chosen for the whole group. It is not legal to
choose the most favorable object independently for each symbol.

Archive groups are scanned in declaration order. Within a group, objects that
define currently unresolved references are extracted repeatedly until the
extraction set stops changing. Newly extracted objects can introduce more
references. Extracted objects do not become reachable merely because they were
considered by the archive scanner; reachability starts at the manifest entries
and follows the selected definitions.

Build conditions are expressions over the candidate configuration's tags, for
example `linux && amd64` or `!cgo`. Conditions on manifests, groups, objects,
symbols, and references are evaluated per configuration and recorded in the
certificate.

## Verdicts

- `NotAffected`: every deployer-allowed, actually linkable configuration and
  every legal weak/group binding is entry-unreachable from every marked object.
- `Affected`: at least one allowed, linkable configuration and legal binding
  reaches a marked object.
- `Unknown`: a legal configuration exists but signed evidence is missing,
  incomplete, contradictory, or exceeds the binding-enumeration limit. Missing
  evidence is never silently treated as unreachable.

A configuration that is signed as non-linkable, or that has a complete manifest
but no legal linking, cannot put code into a final image and is excluded from
the traversal proof.

## Minimal use

```go
evidence := vex.Evidence{
    Marked: []vex.ObjectID{vulnerableObject},
    AllowedConfigs: []string{"linux/amd64"},
    Configs: []vex.CandidateConfig{{
        ID: "linux/amd64",
        Tags: []string{"linux", "amd64"},
        Signature: vex.Signature{Signed: true},
    }},
    Manifests: []vex.LinkManifest{{
        Config: "linux/amd64",
        EntrySymbols: []string{"main.main"},
        ArchiveGroups: []vex.ArchiveGroup{{
            ID: "archive-1",
            Members: []vex.ObjectID{mainObject, vulnerableObject},
        }},
        Signature: vex.Signature{Signed: true},
    }},
    Summaries: []vex.Object{
        {
            ID: mainObject,
            Symbols: []vex.Symbol{{
                Name: "main.main",
                Binding: vex.BindingStrong,
            }},
            Signature: vex.Signature{Signed: true},
        },
        {
            ID: vulnerableObject,
            Symbols: []vex.Symbol{{
                Name: "vulnerable.function",
                Binding: vex.BindingStrong,
            }},
            Signature: vex.Signature{Signed: true},
        },
    },
}

cert, err := vex.Accept(evidence)
if err != nil {
    // malformed input, not a VEX conclusion
}

if err := vex.VerifyCertificate(cert, evidence); err != nil {
    // the certificate was modified or does not replay
}
```

`Analyze`, `Evaluate`, `Accept`, `Check`, and `Prove` are equivalent entry
points. They also accept `*Evidence` or the JSON encoding of `Evidence`.

## Signatures

By default every signed candidate, manifest, and object summary that is used by
the proof must carry a signature. `Signature` is an `any` field so a host can
supply:

```go
vex.Signature{Value: "base64 signature", KeyID: "release-key"}
```

or a host-native string, `[]byte`, boolean, or map. Supply a
`vex.SignatureVerifier` when a real trust root is available:

```go
cert, err := vex.Accept(evidence, vex.Options{
    Verifier: vex.SignatureVerifierFunc(func(payload []byte, sig any) error {
        return releaseKey.Verify(payload, sig)
    }),
})
```

Without a verifier, the package checks signature presence and an optional
`PayloadDigest`. Tests can disable the requirement explicitly with
`require := false; Options{RequireSignatures: &require}`; production acceptance
should not.

## Certificate contents

Each `ConfigProof` contains:

- the ordered extraction steps and resulting extraction closure;
- every enumerated weak/group binding, with the complete consistent group
  selection for that binding;
- the final symbol-to-object bindings and exact object identities;
- the entry-reachable objects and any marked objects reached;
- the evaluated conditions and any missing signed evidence;
- a linkability result, so an unlinkable configuration is distinguishable from
  an affected one.

`CertificateDigest` binds the complete proof. `VerifyCertificate(cert, evidence)`
recomputes acceptance and requires an exact replay, including verdict, extraction
closure, bindings, reachability, and conditions.

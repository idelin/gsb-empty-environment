# DNSSEC RRSIG / NSEC3 proof validation

Importable, dependency-free Go package for validating DNSSEC denial and
wildcard evidence. It performs all cryptography locally and never accesses the
network.

## Implemented validation

- RFC 4034 canonical domain-name ordering: ASCII case folding, right-to-left
  label comparison, unsigned octet ordering, and root/suffix ordering.
- RFC 4034 RRset canonicalization: lower-cased embedded owner names, canonical
  RDATA ordering, and wildcard-owner reconstruction from `RRSIG.Labels`.
- ECDSA-P256/SHA-256 (`algorithm 13`) RRSIG verification over real DNS wire
  data, with raw `r || s` signature validation, key-tag matching, a serial-time
  inception/expiration window, and signer/current-zone enforcement.
- RFC 5155 NSEC3 parsing and parameter checks: SHA-1 only, known flag bits,
  iteration policy limit, salt bounds, 20-octet next hash, and strict type
  bitmaps.
- Unpadded Base32hex encode/decode and NSEC3 SHA-1 iterative hashing.
- Exclusive circular interval coverage, including the wrap-around tail
  interval.
- Complete NXDOMAIN proof: exact closest-encloser, next-closer coverage, and
  wildcard coverage.
- NODATA proof for exact nodes, empty non-terminals, and Opt-Out unsigned
  delegation boundaries. CNAME presence is checked explicitly. Opt-Out
  coverage is accepted only with an established delegation context and a DS
  query.
- Wildcard positive-answer proof: the signature must indicate a one-label
  wildcard expansion, the closest encloser must exist, the exact QNAME must be
  absent, and the next-closer must be denied.

## Core API

```go
import dnssec "formal.local/w27-fb58ae6cdf"

name, _ := dnssec.ParseName("www.example.com.")
sig, _ := dnssec.ParseRRSIG(rrsigRData)
key, _ := dnssec.ParseDNSKEY(dnskeyRData)

err := dnssec.VerifyRRSIG(rrset, sig, key, now, zone)
```

For NSEC3 evidence, supply the RRSIGs for every NSEC3 RRset to a validator:

```go
validator := dnssec.NewValidator(zone, zoneKeys, now)

nxProof, err := validator.NXDOMAIN(queryName, signedNSEC3Evidence)
nodataProof, err := validator.NODATA(queryName, qtype, signedNSEC3Evidence, isDelegation)
wildProof, err := validator.WildcardAnswer(queryName, rrset, rrsigs, signedNSEC3Evidence)
```

`SignedNSEC3` couples a parsed NSEC3 record with the RRSIGs covering that
owner's single-record RRset. `AuthenticateNSEC3` checks those signatures first;
the proof rules then operate only on authenticated records.

The lower-level `ValidateNXDOMAIN`, `ValidateNODATA`, and
`ValidateWildcardAnswer` functions operate on an already-authenticated
`NSEC3Set` and are useful for resolver code that already performed signature
verification.

## Policies

- `DefaultMaxIterations` is 250. Use `NewValidatorWithLimit` for an explicit
  policy; the protocol field maximum is 65535.
- Opt-Out is never inferred from a covering hash. The caller must establish
  that the QNAME is an unsigned delegation boundary.
- A wildcard signature is never accepted merely because it verifies. The
  closest-encloser and next-closer NSEC3 chain must also prove that no exact
  owner shadows it.

Run the full real-ECDSA test suite with:

```sh
go test ./...
```

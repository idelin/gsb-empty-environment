# pkcs12verify

Raw ASN.1 DER level structural integrity and security-policy validation for
PKCS#12 (PFX / RFC 7292) key packages, written in pure Go standard library.
The package validates uploaded key packages for a key-management / certificate
import gateway. It never decrypts private keys and never imports certificates.

## Threat model

- Skipped `MacData` integrity checks allow an attacker to inject a malicious CA
  certificate into the `SafeContents` of a trusted root package, even without
  knowing the password.
- `MacData.iterations` of `1` enables offline password cracking; a value such
  as `10^8` turns parsing into a CPU-exhaustion DoS.
- Plaintext `keyBag`, RC2/RC4/DES/3DES legacy PBE and MD5/SHA-1 digests are
  downgrade hazards.

## Enforcement

1. **Strict DER.** Exactly one `PFX` TLV (`version == 3`), mandatory `MacData`,
   `authSafe` as `id-data` wrapping a primitive OCTET STRING. A custom DER
   walker rejects trailing garbage, BER indefinite lengths, non-minimal long
   form lengths and constructed primitive TLVs before `asn1.Unmarshal`.
2. **Integrity MAC policy.**
   - Legacy PKCS#12 MAC restricted to SHA-256/SHA-384/SHA-512 with the RFC 7292
     Appendix B KDF (ID = 3, BMPString UTF-16-BE + double-zero terminator);
     MD5/SHA-1 are hard rejected.
   - PBMAC1 (RFC 9579) restricted to PBKDF2-HMAC-SHA256.
   - Salt >= 16 bytes and iterations inside `[MinIterations, MaxIterations]`
     (default `[10000, 500000]`). The policy gate runs before any KDF work so a
     DoS iteration count never triggers expensive hashing.
   - HMAC over the raw `authSafe.content` octets, compared with
     `crypto/hmac.Equal` (constant time).
3. **AuthenticatedSafe / SafeBag policy.** Plaintext `keyBag` is rejected;
   `pkcs8ShroudedKeyBag` requires PBES2 with an explicit SHA-2 PBKDF2 PRF and
   AES-256-CBC or AES-256-GCM. RC2/RC4/DES/3DES, PKCS#12 legacy PBE OIDs and
   PKCS#5 v1 PBE OIDs map to `ErrWeakCipher`; everything else not on the allow
   list is default-deny. `certBag` accepts X.509 certificates only.

## Usage

```go
report, err := pkcs12verify.Verify(uploadedDER, password, pkcs12verify.Options{})
if err != nil {
    switch {
    case errors.Is(err, pkcs12verify.ErrIntegrity):
        // tampered package or wrong password
    case errors.Is(err, pkcs12verify.ErrWeakCipher):
        // RC2/3DES/DES/... downgrade
    case errors.Is(err, pkcs12verify.ErrIterations):
        // weak (cracking) or excessive (DoS) iteration count
    }
    return err
}
// report.CertBagCount, report.ShroudedKeyCount, report.Ciphers, ...
```

## Dependencies

Standard library only: `encoding/asn1`, `crypto/hmac`, `crypto/sha256`,
`crypto/sha512`, plus `crypto/aes`/`crypto/cipher` in test vectors. No network
access and no external modules are required.

## Tests

`go test -buildvcs=false ./...` covers: missing `MacData`, CA-injection
tampering of `SafeContents`, `iterations = 1` and `10^8`, RC2/SHA-1/3DES/RC4
downgrade rejection, plaintext key bag rejection, non-canonical DER, trailing
bytes, wrong password, short salt, PBMAC1, AES-256-CBC/GCM happy paths, a
PBKDF2-HMAC-SHA256 known-answer vector and RFC 7292 KDF structural checks.

// Package netsec verifies Ruby gem archives for a Go mirror service.
//
// The entry point is Verify. It defends against the two relevant gem supply
// chain attacks: stripping .sig members to downgrade a signed gem, and reusing
// a member name more than once so checksum verification and extraction
// observe different bytes.
//
// Verification always enforces:
//
//   - outer tar member names are unique (duplicates are rejected);
//   - checksums.yaml SHA512 digests match the actual member bytes;
//   - metadata.gz, data.tar.gz and checksums.yaml.gz are either all signed or
//     all unsigned, so removing a single .sig fails;
//   - present signatures are detached RSA-SHA256 PKCS#7 signatures from one
//     signer certificate.
//
// HighSecurity additionally requires the signer chain to terminate at a
// configured trusted root, while MediumSecurity accepts unsigned gems and
// validates present signatures and their embedded chains without pinning a
// trust root.
package netsec

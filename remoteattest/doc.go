// Package remoteattest implements raw, byte-level TPM 2.0 quote
// (TPMS_ATTEST) decoding and Linux IMA binary measurement-log replay for a
// remote attestation verifier.
//
// The package deliberately depends only on the Go standard library: TPM 2.0
// structures are decoded by hand from their big-endian wire representation and
// the ECDSA-P256 AK signature is checked with crypto/ecdsa. No external TPM
// SDK (go-tpm, go-attestation, tpm2-tools bindings, ...) is used anywhere.
//
// # Wire layout
//
// The attestation blob handled here is the TPMS_ATTEST structure that is
// signed by the AK, serialized in TPM 2.0 big-endian wire order:
//
//	magic             4 bytes  (0xFF544347, see MagicTCG)
//	type              2 bytes  (0x8018 TPM_ST_ATTEST_QUOTE)
//	qualifiedSigner   TPM2B    (content = compressed P-256 AK point, 33 bytes)
//	extraData         TPM2B    (attestation nonce / qualifyingData)
//	clock             8 bytes
//	resetCount        4 bytes
//	restartCount      4 bytes
//	safe              1 byte
//	firmwareVersion   8 bytes
//	pcrSelect         TPMS_PCR_SELECTION (hashAlg u16, size u8, bitmap)
//	pcrDigest         TPM2B    (SHA-256 digest over the selected PCR bank)
//
// The signature blob is the fixed ECDSA-P256 TPMT_SIGNATURE body
// (sigAlg=0x0010, hashAlg=0x000B, r[32], s[32]) and is verified over
// SHA-256 of the exact attestation blob bytes.
//
// The IMA log is parsed from its native little-endian binary layout: every
// entry starts with a 4-byte LE PCR index and a 20-byte SHA-1 template hash,
// followed by the NUL-terminated template name ("ima-ng"/"ima-sig") and the
// variable-length, length-prefixed template fields.
package remoteattest

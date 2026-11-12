// Package netsec registers the hash implementations needed to replay TPM PCR
// banks and verify TPM2 Quote signatures. TPM logs may legitimately carry
// SHA-1 banks, so the SHA-1 implementation must be linked even though Go does
// not enable it by default.
package netsec

import (
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
)

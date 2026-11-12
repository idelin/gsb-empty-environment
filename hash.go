package netsec

import "crypto/sha256"

// hmacHash is the hash used by the HMAC pseudo signer/verifier.
var hmacHash = sha256.New

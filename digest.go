package netsec

import (
	"crypto/sha1"
	"encoding/base64"
)

// DigestID derives the ZooKeeper "digest" identity for user/password:
// "user:Base64(SHA-1(user:password))". Standard (padded) base64 is used,
// matching the reference implementation.
func DigestID(user, password string) string {
	sum := sha1.Sum([]byte(user + ":" + password))
	return user + ":" + base64.StdEncoding.EncodeToString(sum[:])
}

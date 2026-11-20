package netsec

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

// SHA512Base64 returns the SHA-512 digest of r encoded as standard (RFC 4648
// with padding) base64, matching the contentHash format of packages.lock.json.
func SHA512Base64(r io.Reader) (string, error) {
	h := sha512.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// NupkgHashFunc adapts a package-to-path resolver into a HashFunc. The
// resolver returns the on-disk path of the .nupkg for a given id and version.
// A missing package is reported as ("", false) without failing verification of
// the rest of the lock file.
func NupkgHashFunc(resolvePath func(packageID, resolvedVersion string) (path string, ok bool)) HashFunc {
	return func(packageID, resolvedVersion string) (string, bool) {
		path, ok := resolvePath(packageID, resolvedVersion)
		if !ok {
			return "", false
		}
		f, err := os.Open(path)
		if err != nil {
			return "", false
		}
		defer f.Close()
		hash, err := SHA512Base64(f)
		if err != nil {
			return "", false
		}
		return hash, true
	}
}

// VerifyNupkgHash is a convenience wrapper comparing the SHA-512 standard
// base64 of the given .nupkg reader against an expected lock-file hash. It
// returns nil only when the encodings are canonical and byte-identical.
func VerifyNupkgHash(r io.Reader, expectedContentHash string) error {
	if !isCanonicalStdBase64(expectedContentHash) {
		return fmt.Errorf("expected hash is not canonical standard base64")
	}
	actual, err := SHA512Base64(r)
	if err != nil {
		return err
	}
	if actual != expectedContentHash {
		return fmt.Errorf("contentHash mismatch: expected %s, got %s", expectedContentHash, actual)
	}
	return nil
}

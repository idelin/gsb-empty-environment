package update

import (
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
)

// digest computes one algorithm digest over data.
func digest(algorithm HashAlgorithm, data []byte) (HexBytes, error) {
	switch algorithm {
	case SHA256:
		sum := sha256.Sum256(data)
		return append(HexBytes(nil), sum[:]...), nil
	case SHA512:
		sum := sha512.Sum512(data)
		return append(HexBytes(nil), sum[:]...), nil
	default:
		return nil, fmt.Errorf("update: unsupported hash algorithm %q", algorithm)
	}
}

// verifyDigests checks data against every pinned digest and, when present,
// against the advertised length. An empty digest set is rejected for target
// references because it would not pin any bytes.
func verifyDigests(ref MetaRef, data []byte) error {
	if ref.Length != 0 && int64(len(data)) != ref.Length {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrLengthMismatch, len(data), ref.Length)
	}
	if len(ref.Hashes) == 0 {
		return fmt.Errorf("%w: reference carries no digests", ErrDigestMismatch)
	}
	for algorithm, want := range ref.Hashes {
		got, err := digest(algorithm, data)
		if err != nil {
			return err
		}
		if !bytesEqual(got, want) {
			return fmt.Errorf("%w: %s digest", ErrDigestMismatch, algorithm)
		}
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package netsec

import "crypto"

// AlgorithmID is the TPM2_ALG_* two-byte identifier.
type AlgorithmID uint16

// TPM2 algorithm identifiers used by this package.
const (
	AlgSHA1   AlgorithmID = 0x0004
	AlgSHA256 AlgorithmID = 0x0009
	AlgSHA384 AlgorithmID = 0x000A
	AlgSHA512 AlgorithmID = 0x000B

	AlgRSA       AlgorithmID = 0x0001
	AlgECC       AlgorithmID = 0x0023
	AlgRSASSA    AlgorithmID = 0x0014
	AlgRSAPSS    AlgorithmID = 0x0016
	AlgECDSA     AlgorithmID = 0x0018
	AlgNull      AlgorithmID = 0x0010
	AlgKeyedHash AlgorithmID = 0x0008
)

type hashSpec struct {
	size   int
	crypto crypto.Hash
}

var hashAlgorithms = map[AlgorithmID]hashSpec{
	AlgSHA1:   {size: 20, crypto: crypto.SHA1},
	AlgSHA256: {size: 32, crypto: crypto.SHA256},
	AlgSHA384: {size: 48, crypto: crypto.SHA384},
	AlgSHA512: {size: 64, crypto: crypto.SHA512},
}

// hashSpecFor returns the registered specification of a TPM hash algorithm.
// Unknown algorithms or algorithms unavailable in the running binary are
// rejected.
func hashSpecFor(alg AlgorithmID) (hashSpec, error) {
	spec, ok := hashAlgorithms[alg]
	if !ok || !spec.crypto.Available() {
		return hashSpec{}, ErrUnknownAlgorithm
	}
	return spec, nil
}

// algorithmForHash maps a Go hash back to its TPM algorithm identifier.
func algorithmForHash(h crypto.Hash) (AlgorithmID, error) {
	for alg, spec := range hashAlgorithms {
		if spec.crypto == h {
			return alg, nil
		}
	}
	return 0, ErrUnknownAlgorithm
}

package netsec

import (
	"crypto"
	"crypto/hmac"
	"hash"
)

const (
	// MasterSecretLength is the fixed length in bytes of a TLS 1.2 master
	// secret.
	MasterSecretLength = 48

	masterSecretLabel       = "master secret"
	extendedMasterSecretLbl = "extended master secret"
	keyExpansionLabel       = "key expansion"
	clientFinishedLabel     = "client finished"
	serverFinishedLabel     = "server finished"
)

// HashFunc identifies the PRF hash negotiated by a TLS 1.2 cipher suite. It
// mirrors crypto.Hash but defaults to SHA-256 when unset.
type HashFunc crypto.Hash

func (h HashFunc) hash() crypto.Hash {
	if h == 0 {
		return crypto.SHA256
	}
	return crypto.Hash(h)
}

func (h HashFunc) new() hash.Hash {
	ch := h.hash()
	if !ch.Available() {
		panic("netsec: requested PRF hash is not linked into the binary")
	}
	return ch.New()
}

// pHash implements the TLS 1.2 P_hash expansion (RFC 5246, section 5) using
// the hash function carried by hf.
func pHash(hf HashFunc, secret, seed []byte, length int) []byte {
	mac := hmac.New(hf.new, secret)

	out := make([]byte, 0, length)
	var a []byte
	var block []byte
	for len(out) < length {
		mac.Reset()
		if a == nil {
			mac.Write(seed)
		} else {
			mac.Write(a)
		}
		a = mac.Sum(nil)

		mac.Reset()
		mac.Write(a)
		mac.Write(seed)
		block = mac.Sum(block[:0])
		out = append(out, block...)
	}
	return out[:length]
}

// PRF evaluates the TLS 1.2 PRF (RFC 5246, section 5). TLS 1.2 uses a single
// cipher-suite-defined hash rather than the split construction of TLS 1.0/1.1.
func PRF(hf HashFunc, secret []byte, label string, seed []byte, length int) []byte {
	labeledSeed := make([]byte, 0, len(label)+len(seed))
	labeledSeed = append(labeledSeed, label...)
	labeledSeed = append(labeledSeed, seed...)
	return pHash(hf, secret, labeledSeed, length)
}

// SessionHash returns the hash over the complete, ordered TLS 1.2 handshake
// transcript. The caller must concatenate every handshake message exactly as
// it appeared on the wire, including the four-byte record headers.
func SessionHash(hf HashFunc, transcript []byte) []byte {
	h := hf.new()
	h.Write(transcript)
	return h.Sum(nil)
}

// DeriveMasterSecret computes the TLS 1.2 master secret for a full handshake.
//
// When extendedMasterSecret is false it uses the classic
// "master secret" label with the client and server random values as seed
// (RFC 5246). When true it uses the "extended master secret" label and the
// session hash over the full handshake transcript instead (RFC 7627).
func DeriveMasterSecret(hf HashFunc, preMasterSecret, clientRandom, serverRandom, transcript []byte, extendedMasterSecret bool) []byte {
	if extendedMasterSecret {
		seed := SessionHash(hf, transcript)
		return PRF(hf, preMasterSecret, extendedMasterSecretLbl, seed, MasterSecretLength)
	}
	seed := make([]byte, 0, len(clientRandom)+len(serverRandom))
	seed = append(seed, clientRandom...)
	seed = append(seed, serverRandom...)
	return PRF(hf, preMasterSecret, masterSecretLabel, seed, MasterSecretLength)
}

// DeriveKeyBlock expands a master secret into the TLS 1.2 key block used to
// derive MAC keys and encryption keys. Both classic and extended master
// secrets feed the same "key expansion" step.
func DeriveKeyBlock(hf HashFunc, masterSecret, clientRandom, serverRandom []byte, length int) []byte {
	seed := make([]byte, 0, len(serverRandom)+len(clientRandom))
	seed = append(seed, serverRandom...)
	seed = append(seed, clientRandom...)
	return PRF(hf, masterSecret, keyExpansionLabel, seed, length)
}

// VerifyData computes the 12-byte verify_data placed in a TLS 1.2 Finished
// message. The handshake hash for an extended master secret session is
// computed over the same transcript as the master secret.
func VerifyData(hf HashFunc, masterSecret, transcript []byte, fromClient bool) []byte {
	finishedHash := SessionHash(hf, transcript)
	label := serverFinishedLabel
	if fromClient {
		label = clientFinishedLabel
	}
	return PRF(hf, masterSecret, label, finishedHash, 12)
}

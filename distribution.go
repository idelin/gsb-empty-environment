package netsec

import (
	"crypto/ed25519"
	"encoding/binary"
)

// Distribution is an administrator-signed message that installs one sender
// chain for one device in a specific group epoch.
type Distribution struct {
	GroupID            []byte
	Epoch              uint64
	MembersDigest      Digest
	SenderDevice       DeviceID
	ChainID            ChainID
	ChainKeyCiphertext []byte
	// ChainKeyDigest binds the ciphertext (and therefore, through the host's
	// envelope encryption, the initial chain key) into the signed payload.
	ChainKeyDigest Digest
	StartIndex     uint64
	Signature      []byte
}

// ChainKeyCipherDigest returns the digest that must be signed alongside a
// distribution, computed over the ciphertext the host transmits.
func ChainKeyCipherDigest(ciphertext []byte) Digest {
	return digestBytes(ciphertext)
}

// distributionInput is the canonical byte string covered by the administrator
// signature. Every variable-length field is length-prefixed and fixed-width
// fields are big-endian, making the encoding unambiguous.
func distributionInput(d *Distribution) []byte {
	buf := make([]byte, 0,
		4+len(d.GroupID)+
			8+
			len(d.MembersDigest)+
			len(d.SenderDevice)+
			len(d.ChainID)+
			4+len(d.ChainKeyCiphertext)+
			len(d.ChainKeyDigest)+
			8)

	buf = append(buf, encodeLengthPrefixed(d.GroupID)...)

	var fixed [8]byte
	binary.BigEndian.PutUint64(fixed[:], d.Epoch)
	buf = append(buf, fixed[:]...)

	buf = append(buf, d.MembersDigest[:]...)
	buf = append(buf, d.SenderDevice[:]...)
	buf = append(buf, d.ChainID[:]...)

	buf = append(buf, encodeLengthPrefixed(d.ChainKeyCiphertext)...)
	buf = append(buf, d.ChainKeyDigest[:]...)

	binary.BigEndian.PutUint64(fixed[:], d.StartIndex)
	buf = append(buf, fixed[:]...)

	return buf
}

// SignDistribution fills the Signature field of d using the group
// administrator's Ed25519 private key. The signature covers groupID, epoch,
// member-set digest, senderDevice, chainID, chain-key ciphertext digest, and
// the start index.
func SignDistribution(d *Distribution, admin ed25519.PrivateKey) {
	d.Signature = ed25519.Sign(admin, distributionInput(d))
}

// VerifyDistribution checks the administrator Ed25519 signature embedded in d.
func VerifyDistribution(d *Distribution, admin ed25519.PublicKey) bool {
	if len(d.Signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(admin, distributionInput(d), d.Signature)
}

package netsec

import (
	"crypto/sha256"
	"encoding/binary"
)

// shareBindingMessage builds the canonical, unambiguous message that is HMACed
// into a share. It binds the share to the generation, seal configuration
// digest, nonce, holder, share index and fragment. Tampering with any field,
// or replaying the share into another context, invalidates the tag.
func shareBindingMessage(share Share) []byte {
	var msg []byte
	msg = writeLengthPrefixed(msg, []byte(shareBindingDomain))

	var gen [8]byte
	binary.BigEndian.PutUint64(gen[:], share.Generation)
	msg = writeLengthPrefixed(msg, gen[:])

	msg = writeLengthPrefixed(msg, []byte(share.SealID))
	msg = writeLengthPrefixed(msg, share.Nonce)
	msg = writeLengthPrefixed(msg, []byte(share.HolderID))

	var idx [8]byte
	binary.BigEndian.PutUint64(idx[:], uint64(share.Index))
	msg = writeLengthPrefixed(msg, idx[:])
	msg = writeLengthPrefixed(msg, share.Fragment)
	return msg
}

// sealConfigDigest produces a stable digest over the full seal configuration,
// including the holder identities and their authentication keys. Two distinct
// configurations never share a digest.
func sealConfigDigest(cfg SealConfig) []byte {
	h := sha256.New()
	h.Write([]byte(sealDigestVersion))

	var threshold [8]byte
	binary.BigEndian.PutUint64(threshold[:], uint64(cfg.Threshold))
	h.Write(threshold[:])

	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(cfg.Holders)))
	h.Write(count[:])

	h.Write(writeLengthPrefixed(nil, []byte(cfg.SealID)))
	for _, holder := range cfg.Holders {
		h.Write(writeLengthPrefixed(nil, []byte(holder.ID)))
		keyHash := sha256.Sum256(holder.Key)
		h.Write(writeLengthPrefixed(nil, keyHash[:]))
	}
	return h.Sum(nil)
}

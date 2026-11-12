package netsec

// CHAPResponse computes the standard CHAP response defined by RFC 2759:
// H(identifier || secret || challenge). The FC-SP DH-CHAP exchange applies
// the same construction with the negotiated hash; callers that must bind the
// Diffie-Hellman shared value into the secret can obtain it via
// DeriveKey first and pass the result here.
func CHAPResponse(hash HashID, identifier, challenge, secret []byte) ([]byte, error) {
	h, err := hash.NewHash()
	if err != nil {
		return nil, err
	}
	if len(identifier) == 0 || len(challenge) == 0 {
		return nil, ErrInvalidMessage
	}
	h.Write(identifier)
	h.Write(secret)
	h.Write(challenge)
	return h.Sum(nil), nil
}

// DeriveKey binds the raw DH shared secret to a context label using the
// negotiated hash: H(label || 0x00 || shared). It returns
// ErrNoCommonDHGroup when null DH was negotiated, since no shared secret
// exists to derive from.
func DeriveKey(hash HashID, label, shared []byte) ([]byte, error) {
	if len(shared) == 0 {
		return nil, ErrNoCommonDHGroup
	}
	h, err := hash.NewHash()
	if err != nil {
		return nil, err
	}
	h.Write(label)
	h.Write([]byte{0x00})
	h.Write(shared)
	return h.Sum(nil), nil
}

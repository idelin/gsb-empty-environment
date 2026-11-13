package netsec

import "bytes"

// FlipOCSPResponseNonce returns a copy of responseDER with the first bit of
// the response nonce inverted. The original response is not modified. The
// returned response is intentionally invalid for nonce matching and is useful
// for negative tests; its signature is no longer valid.
func FlipOCSPResponseNonce(responseDER []byte) ([]byte, error) {
	parsed, err := parseOCSPResponse(responseDER)
	if err != nil {
		return nil, err
	}
	if !parsed.info.NoncePresent || parsed.nonceStart == parsed.nonceEnd {
		return nil, ErrNonceMissing
	}

	flipped := bytes.Clone(responseDER)
	flipped[parsed.nonceStart] ^= 0x01
	return flipped, nil
}

// FlipNonce is a short alias for FlipOCSPResponseNonce.
func FlipNonce(responseDER []byte) ([]byte, error) {
	return FlipOCSPResponseNonce(responseDER)
}

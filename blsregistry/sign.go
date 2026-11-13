package blsregistry

// Sign produces an ordinary BLS signature over message in the
// DomainSignature domain: signature = secret * H_sig(message).
func Sign(engine Engine, secret Scalar, message []byte) G2Point {
	return engine.HashToG2(DomainSignature, message).Mul(secret)
}

// SignProofOfPossession produces a proof of possession for publicKey, i.e. a
// signature over its canonical encoding in the dedicated
// DomainProofOfPossession domain:
//
//	proof = secret * H_pop(Encode(publicKey)).
//
// It panics if secret is zero: a zero secret yields the identity public key,
// which the registry rejects anyway.
func SignProofOfPossession(engine Engine, secret Scalar, publicKey G1Point) G2Point {
	return engine.HashToG2(DomainProofOfPossession, publicKey.Encode()).Mul(secret)
}

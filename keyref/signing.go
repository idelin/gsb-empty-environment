package keyref

import "crypto/sha256"

// SigningRequest is the immutable object handed to the build/signing
// service. It carries the fully resolved key identity so the signer never
// has to interpret an alias as a global name.
type SigningRequest struct {
	Account      string
	Region       string
	KeyID        string
	KeyVersion   int64
	Fingerprint  string
	Alias        string
	AliasVersion int64
	Chain        []Hop
	PayloadHash  [32]byte
}

// NewSigningRequest resolves ref inside tx and binds the result to the
// payload. Because resolution goes through tx, the request is consistent
// with every other resolution performed in the same transaction.
func (r *Resolver) NewSigningRequest(tx *Tx, ref Ref, payload []byte) (SigningRequest, error) {
	res, err := r.Resolve(tx, ref)
	if err != nil {
		return SigningRequest{}, err
	}
	return SigningRequest{
		Account:      res.Account,
		Region:       res.Region,
		KeyID:        res.KeyID,
		KeyVersion:   res.KeyVersion,
		Fingerprint:  res.Fingerprint,
		Alias:        res.Alias,
		AliasVersion: res.AliasVersion,
		Chain:        res.Chain,
		PayloadHash:  sha256.Sum256(payload),
	}, nil
}

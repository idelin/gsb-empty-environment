package netsec

import (
	"bytes"
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
)

func newHash(id HashAlgorithm) (hash.Hash, crypto.Hash, error) {
	switch id {
	case HashSHA1:
		return sha1.New(), crypto.SHA1, nil
	case HashSHA224:
		return sha256.New224(), crypto.SHA224, nil
	case HashSHA256:
		return sha256.New(), crypto.SHA256, nil
	case HashSHA384:
		return sha512.New384(), crypto.SHA384, nil
	case HashSHA512:
		return sha512.New(), crypto.SHA512, nil
	default:
		return nil, 0, fmt.Errorf("%w: hash algorithm %s", ErrUnsupportedAlgorithm, id)
	}
}

func verifySignature(key *Key, sig *Signature, signedData []byte) error {
	if !algorithmsCompatible(key.Algorithm, sig.PubKeyAlgorithm) {
		return fmt.Errorf("%w: signature algorithm %s does not match key algorithm %s",
			ErrBadSignature, sig.PubKeyAlgorithm, key.Algorithm)
	}

	h, hashID, err := newHash(sig.HashAlgorithm)
	if err != nil {
		return err
	}
	_, _ = h.Write(sig.hashInput(signedData))
	digest := h.Sum(nil)
	if len(sig.HashTag) != 2 || !bytes.Equal(sig.HashTag, digest[:2]) {
		return fmt.Errorf("%w: signature hash tag mismatch", ErrBadSignature)
	}

	rd := newMaterialReader(sig.Material)
	switch sig.PubKeyAlgorithm {
	case PubKeyRSAEncryptSign, PubKeyRSASignOnly:
		pub, ok := key.PublicKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: public key is not RSA", ErrBadSignature)
		}
		s, err := rd.mpi()
		if err != nil {
			return err
		}
		signature := s.Bytes()
		size := pub.Size()
		if len(signature) < size {
			signature = append(bytes.Repeat([]byte{0}, size-len(signature)), signature...)
		}
		if len(signature) != size {
			return fmt.Errorf("%w: invalid RSA signature length", ErrBadSignature)
		}
		if err := rsa.VerifyPKCS1v15(pub, hashID, digest, signature); err != nil {
			return fmt.Errorf("%w: %v", ErrBadSignature, err)
		}
		return nil
	case PubKeyDSA:
		pub, ok := key.PublicKey.(*dsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: public key is not DSA", ErrBadSignature)
		}
		r, err := rd.mpi()
		if err != nil {
			return err
		}
		s, err := rd.mpi()
		if err != nil {
			return err
		}
		if !dsa.Verify(pub, digest, r, s) {
			return ErrBadSignature
		}
		return nil
	case PubKeyECDSA:
		pub, ok := key.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: public key is not ECDSA", ErrBadSignature)
		}
		r, err := rd.mpi()
		if err != nil {
			return err
		}
		s, err := rd.mpi()
		if err != nil {
			return err
		}
		if !ecdsa.Verify(pub, digest, r, s) {
			return ErrBadSignature
		}
		return nil
	case PubKeyEdDSA, PubKeyEd25519:
		pub, ok := key.PublicKey.(ed25519.PublicKey)
		if !ok || len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: public key is not Ed25519", ErrBadSignature)
		}
		r, err := rd.mpi()
		if err != nil {
			return err
		}
		s, err := rd.mpi()
		if err != nil {
			return err
		}
		rBytes := r.Bytes()
		if len(rBytes) == 33 && rBytes[0] == 0x40 {
			rBytes = rBytes[1:]
		}
		sBytes := s.Bytes()
		if len(rBytes) > 32 || len(sBytes) > 32 {
			return fmt.Errorf("%w: invalid Ed25519 signature", ErrBadSignature)
		}
		raw := make([]byte, ed25519.SignatureSize)
		copy(raw[32-len(rBytes):32], rBytes)
		copy(raw[64-len(sBytes):], sBytes)
		if !ed25519.Verify(pub, digest, raw) {
			return ErrBadSignature
		}
		return nil
	default:
		return fmt.Errorf("%w: signature public-key algorithm %s", ErrUnsupportedAlgorithm, sig.PubKeyAlgorithm)
	}
}

func algorithmsCompatible(keyAlg, sigAlg PublicKeyAlgorithm) bool {
	switch sigAlg {
	case PubKeyRSAEncryptSign, PubKeyRSASignOnly:
		return keyAlg == PubKeyRSAEncryptSign || keyAlg == PubKeyRSAEncryptOnly || keyAlg == PubKeyRSASignOnly
	case PubKeyDSA:
		return keyAlg == PubKeyDSA
	case PubKeyECDSA:
		return keyAlg == PubKeyECDSA
	case PubKeyEdDSA:
		return keyAlg == PubKeyEdDSA || keyAlg == PubKeyEd25519
	case PubKeyEd25519:
		return keyAlg == PubKeyEdDSA || keyAlg == PubKeyEd25519
	default:
		return false
	}
}

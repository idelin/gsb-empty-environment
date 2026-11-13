package netsec

import (
	"crypto/hkdf"
	"fmt"
	"hash"
)

// EarlySecret is the early secret from RFC 8446, Section 7.1.
type EarlySecret struct {
	secret []byte
	hash   func() hash.Hash
	err    error
}

// HandshakeSecret is the handshake secret from RFC 8446, Section 7.1.
type HandshakeSecret struct {
	secret []byte
	hash   func() hash.Hash
	err    error
}

// MasterSecret is the master secret from RFC 8446, Section 7.1.
type MasterSecret struct {
	secret []byte
	hash   func() hash.Hash
	err    error
}

// Secret returns a copy of the derived secret.
func (s *EarlySecret) Secret() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.secret...)
}

// Secret returns a copy of the derived secret.
func (s *HandshakeSecret) Secret() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.secret...)
}

// Secret returns a copy of the derived secret.
func (s *MasterSecret) Secret() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.secret...)
}

// HashFunc returns the hash constructor used by the secret.
func (s *EarlySecret) HashFunc() func() hash.Hash {
	if s == nil {
		return nil
	}
	return s.hash
}

// HashFunc returns the hash constructor used by the secret.
func (s *HandshakeSecret) HashFunc() func() hash.Hash {
	if s == nil {
		return nil
	}
	return s.hash
}

// HashFunc returns the hash constructor used by the secret.
func (s *MasterSecret) HashFunc() func() hash.Hash {
	if s == nil {
		return nil
	}
	return s.hash
}

// NewEarlySecret computes Early Secret = HKDF-Extract(0, PSK). A nil or
// omitted PSK is valid and produces the all-zero early secret used when no
// PSK is negotiated.
func NewEarlySecret(args ...any) *EarlySecret {
	h := defaultHash()
	var psk []byte
	for _, arg := range args {
		if v, ok := hashFromValue(arg); ok {
			h = v
			continue
		}
		if b, ok := bytesFromValue(arg); ok {
			psk = b
		}
	}
	secret, err := extract(h, psk, nil)
	return &EarlySecret{secret: secret, hash: h, err: err}
}

// ResumptionBinderKey derives the resumption binder key.
func (s *EarlySecret) ResumptionBinderKey() []byte {
	if s == nil || s.err != nil {
		return nil
	}
	out, _ := deriveSecretBytes(s.hash, s.secret, "res binder", nil)
	return out
}

// ClientEarlyTrafficSecret derives client_early_traffic_secret.
func (s *EarlySecret) ClientEarlyTrafficSecret(args ...any) []byte {
	return s.derive("c e traffic", args)
}

// HandshakeSecret derives the handshake secret from the ECDHE shared secret.
func (s *EarlySecret) HandshakeSecret(args ...any) *HandshakeSecret {
	if s == nil || s.err != nil {
		return &HandshakeSecret{err: ErrInvalidSecret}
	}
	var shared []byte
	for _, arg := range args {
		if b, ok := bytesFromValue(arg); ok {
			shared = b
		}
	}
	derived, err := deriveSecretBytes(s.hash, s.secret, "derived", nil)
	if err != nil {
		return &HandshakeSecret{err: err}
	}
	secret, err := extract(s.hash, shared, derived)
	return &HandshakeSecret{secret: secret, hash: s.hash, err: err}
}

// EarlyExporterMasterSecret derives early_exporter_master_secret from the
// early secret and the transcript through ClientHello.
func (s *EarlySecret) EarlyExporterMasterSecret(args ...any) *EarlyExporterMasterSecret {
	if s == nil || s.err != nil {
		return &EarlyExporterMasterSecret{err: ErrInvalidSecret}
	}
	secret, err := s.deriveErr("e exp master", args)
	return &EarlyExporterMasterSecret{secret: secret, hash: s.hash, err: err}
}

func (s *EarlySecret) derive(label string, args []any) []byte {
	out, _ := s.deriveErr(label, args)
	return out
}

func (s *EarlySecret) deriveErr(label string, args []any) ([]byte, error) {
	if s == nil || s.err != nil {
		return nil, ErrInvalidSecret
	}
	return deriveWithTranscript(s.hash, s.secret, label, args)
}

// ClientHandshakeTrafficSecret derives client_handshake_traffic_secret.
func (s *HandshakeSecret) ClientHandshakeTrafficSecret(args ...any) []byte {
	return s.derive("c hs traffic", args)
}

// ServerHandshakeTrafficSecret derives server_handshake_traffic_secret.
func (s *HandshakeSecret) ServerHandshakeTrafficSecret(args ...any) []byte {
	return s.derive("s hs traffic", args)
}

func (s *HandshakeSecret) derive(label string, args []any) []byte {
	if s == nil || s.err != nil {
		return nil
	}
	out, _ := deriveWithTranscript(s.hash, s.secret, label, args)
	return out
}

// MasterSecret derives the master secret.
func (s *HandshakeSecret) MasterSecret() *MasterSecret {
	if s == nil || s.err != nil {
		return &MasterSecret{err: ErrInvalidSecret}
	}
	derived, err := deriveSecretBytes(s.hash, s.secret, "derived", nil)
	if err != nil {
		return &MasterSecret{err: err}
	}
	secret, err := extract(s.hash, nil, derived)
	return &MasterSecret{secret: secret, hash: s.hash, err: err}
}

// ClientApplicationTrafficSecret derives client_application_traffic_secret_0.
func (s *MasterSecret) ClientApplicationTrafficSecret(args ...any) []byte {
	return s.derive("c ap traffic", args)
}

// ServerApplicationTrafficSecret derives server_application_traffic_secret_0.
func (s *MasterSecret) ServerApplicationTrafficSecret(args ...any) []byte {
	return s.derive("s ap traffic", args)
}

// ResumptionMasterSecret derives resumption_master_secret.
func (s *MasterSecret) ResumptionMasterSecret(args ...any) []byte {
	return s.derive("res master", args)
}

// ExporterMasterSecret derives exporter_master_secret from the master secret
// and the transcript through server Finished. The result has a distinct type
// from EarlyExporterMasterSecret.
func (s *MasterSecret) ExporterMasterSecret(args ...any) *ExporterMasterSecret {
	if s == nil || s.err != nil {
		return &ExporterMasterSecret{err: ErrInvalidSecret}
	}
	secret, err := deriveWithTranscript(s.hash, s.secret, "exp master", args)
	return &ExporterMasterSecret{secret: secret, hash: s.hash, err: err}
}

func (s *MasterSecret) derive(label string, args []any) []byte {
	if s == nil || s.err != nil {
		return nil
	}
	out, _ := deriveWithTranscript(s.hash, s.secret, label, args)
	return out
}

func extract(h func() hash.Hash, newSecret, currentSecret []byte) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("netsec: nil hash constructor")
	}
	if newSecret == nil {
		newSecret = make([]byte, h().Size())
	}
	return hkdf.Extract(h, newSecret, currentSecret)
}

func deriveWithTranscript(h func() hash.Hash, secret []byte, label string, args []any) ([]byte, error) {
	var transcript []byte
	var transcriptHash hash.Hash
	for _, arg := range args {
		switch v := arg.(type) {
		case hash.Hash:
			transcriptHash = v
		case []byte:
			if transcript == nil {
				transcript = v
			}
		}
	}
	if transcriptHash != nil {
		return deriveSecretHash(h, secret, label, transcriptHash)
	}
	return deriveSecretBytes(h, secret, label, transcript)
}

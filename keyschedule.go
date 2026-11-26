package tls13

import (
	"crypto/hmac"
	"errors"
)

// BinderLabel selects the PSK binder derivation label.
type BinderLabel string

const (
	ExternalBinder   BinderLabel = "ext binder"
	ResumptionBinder BinderLabel = "res binder"
)

// EarlySecret is the first stage of the TLS 1.3 key schedule.
type EarlySecret struct {
	Suite  CipherSuite
	Secret []byte
	PSK    []byte
}

// NewEarlySecret creates HKDF-Extract(0, PSK). A nil/empty PSK is replaced by
// HashLen zero bytes as required for non-PSK handshakes.
func NewEarlySecret(suite CipherSuite, psk []byte) *EarlySecret {
	if len(psk) == 0 {
		psk = make([]byte, suite.HashSize())
	}
	secret := HKDFExtract(suite.hashFunc(), make([]byte, suite.HashSize()), psk)
	return &EarlySecret{Suite: suite, Secret: secret, PSK: append([]byte(nil), psk...)}
}

// BinderKey derives the "ext binder" or "res binder" key. transcriptHash is
// Transcript-Hash(Truncate(ClientHello)).
func (e *EarlySecret) BinderKey(transcriptHash []byte, label BinderLabel) ([]byte, error) {
	if label == "" {
		label = ResumptionBinder
	}
	return DeriveSecretChecked(e.Suite, e.Secret, string(label), transcriptHash)
}

// ClientEarlyTrafficSecret derives "c e traffic".
func (e *EarlySecret) ClientEarlyTrafficSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(e.Suite, e.Secret, "c e traffic", transcriptHash)
}

// EarlyExporterMasterSecret derives "e exp master".
func (e *EarlySecret) EarlyExporterMasterSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(e.Suite, e.Secret, "e exp master", transcriptHash)
}

// DerivedSecret derives the "derived" value used between schedule stages.
func (e *EarlySecret) DerivedSecret() []byte {
	empty := hashMessages(e.Suite)
	return mustDeriveSecret(e.Suite, e.Secret, "derived", empty)
}

// HandshakeSecret mixes the (EC)DHE shared secret into the schedule.
func (e *EarlySecret) HandshakeSecret(sharedSecret []byte) *HandshakeSecret {
	derived := e.DerivedSecret()
	secret := HKDFExtract(e.Suite.hashFunc(), derived, sharedSecret)
	return &HandshakeSecret{Suite: e.Suite, Secret: secret, early: e}
}

// HandshakeSecret is the second stage of the TLS 1.3 key schedule.
type HandshakeSecret struct {
	Suite  CipherSuite
	Secret []byte
	early  *EarlySecret
}

// ClientHandshakeTrafficSecret derives "c hs traffic".
func (h *HandshakeSecret) ClientHandshakeTrafficSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(h.Suite, h.Secret, "c hs traffic", transcriptHash)
}

// ServerHandshakeTrafficSecret derives "s hs traffic".
func (h *HandshakeSecret) ServerHandshakeTrafficSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(h.Suite, h.Secret, "s hs traffic", transcriptHash)
}

// DerivedSecret derives the "derived" value before MasterSecret.
func (h *HandshakeSecret) DerivedSecret() []byte {
	empty := hashMessages(h.Suite)
	return mustDeriveSecret(h.Suite, h.Secret, "derived", empty)
}

// MasterSecret creates the final schedule stage using HashLen zero bytes as
// its input keying material.
func (h *HandshakeSecret) MasterSecret() *MasterSecret {
	derived := h.DerivedSecret()
	zeros := make([]byte, h.Suite.HashSize())
	secret := HKDFExtract(h.Suite.hashFunc(), derived, zeros)
	return &MasterSecret{Suite: h.Suite, Secret: secret, handshake: h}
}

// MasterSecret is the final TLS 1.3 key schedule stage.
type MasterSecret struct {
	Suite     CipherSuite
	Secret    []byte
	handshake *HandshakeSecret
}

// ClientApplicationTrafficSecret derives "c ap traffic".
func (m *MasterSecret) ClientApplicationTrafficSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(m.Suite, m.Secret, "c ap traffic", transcriptHash)
}

// ServerApplicationTrafficSecret derives "s ap traffic".
func (m *MasterSecret) ServerApplicationTrafficSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(m.Suite, m.Secret, "s ap traffic", transcriptHash)
}

// ExporterMasterSecret derives "exp master".
func (m *MasterSecret) ExporterMasterSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(m.Suite, m.Secret, "exp master", transcriptHash)
}

// ResumptionMasterSecret derives "res master".
func (m *MasterSecret) ResumptionMasterSecret(transcriptHash []byte) ([]byte, error) {
	return DeriveSecretChecked(m.Suite, m.Secret, "res master", transcriptHash)
}

// ExportKeyingMaterial evaluates RFC 8446 TLS-Exporter.
func ExportKeyingMaterial(suite CipherSuite, exporterMasterSecret []byte, label string, context []byte, length int) ([]byte, error) {
	contextHash := hashMessages(suite, context)
	derived, err := DeriveSecretChecked(suite, exporterMasterSecret, label, contextHash)
	if err != nil {
		return nil, err
	}
	return ExpandLabel(suite.hashFunc(), derived, "exporter", contextHash, length)
}

// TrafficKeys contains an AEAD key and 12-byte nonce base IV.
type TrafficKeys struct {
	Key []byte
	IV  []byte
}

// DeriveTrafficKeys expands "key" and "iv" from a traffic secret.
func DeriveTrafficKeys(suite CipherSuite, trafficSecret []byte) (TrafficKeys, error) {
	if len(trafficSecret) != suite.HashSize() {
		return TrafficKeys{}, errors.New("tls13: traffic secret length does not match hash size")
	}
	key, err := ExpandLabel(suite.hashFunc(), trafficSecret, "key", nil, suite.KeyLen())
	if err != nil {
		return TrafficKeys{}, err
	}
	iv, err := ExpandLabel(suite.hashFunc(), trafficSecret, "iv", nil, suite.IVLen())
	if err != nil {
		return TrafficKeys{}, err
	}
	return TrafficKeys{Key: key, IV: iv}, nil
}

// MustDeriveTrafficKeys is DeriveTrafficKeys for known-good suite values.
func MustDeriveTrafficKeys(suite CipherSuite, trafficSecret []byte) TrafficKeys {
	keys, err := DeriveTrafficKeys(suite, trafficSecret)
	if err != nil {
		panic(err)
	}
	return keys
}

// FinishedKey derives the "finished" HMAC key from a handshake/application
// traffic secret or PSK binder key.
func FinishedKey(suite CipherSuite, baseSecret []byte) []byte {
	return mustExpandLabel(suite.hashFunc(), baseSecret, "finished", nil, suite.HashSize())
}

// FinishedVerifyData derives the Finished key from trafficSecret and computes
// HMAC(finished_key, transcriptHash). Use FinishedMAC when key is already the
// expanded "finished" key.
func FinishedVerifyData(suite CipherSuite, trafficSecret, transcriptHash []byte) []byte {
	return CalculateVerifyData(suite, trafficSecret, transcriptHash)
}

// VerifyFinished checks a Finished verify_data value in constant time.
func VerifyFinished(suite CipherSuite, trafficSecret, transcriptHash, verifyData []byte) bool {
	expected := CalculateVerifyData(suite, trafficSecret, transcriptHash)
	return hmac.Equal(expected, verifyData)
}

// NextTrafficSecret performs the "traffic upd" derivation used by KeyUpdate.
func NextTrafficSecret(suite CipherSuite, trafficSecret []byte) ([]byte, error) {
	if len(trafficSecret) != suite.HashSize() {
		return nil, errors.New("tls13: traffic secret length does not match hash size")
	}
	return ExpandLabel(suite.hashFunc(), trafficSecret, "traffic upd", nil, suite.HashSize())
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append(make([]byte, 0, len(b)), b...)
}

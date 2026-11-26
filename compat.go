package tls13

import "errors"

// RecordProtection is an alternative name for a one-direction record state.
type RecordProtection = RecordState

// CipherState is another common name for a one-direction record state.
type CipherState = RecordState

// NewRecordProtection creates a one-direction record protection state.
func NewRecordProtection(suite CipherSuite, trafficSecret []byte) (*RecordProtection, error) {
	return NewRecordState(suite, trafficSecret)
}

// NewCipherState creates a one-direction AEAD state.
func NewCipherState(suite CipherSuite, trafficSecret []byte) (*CipherState, error) {
	return NewRecordState(suite, trafficSecret)
}

// Connection is a bidirectional TLS 1.3 record protection state machine.
type Connection = RecordProtector

// RecordLayer is an alternative bidirectional record-layer name.
type RecordLayer = RecordProtector

// NewConnection creates a bidirectional client/server record state machine.
func NewConnection(suite CipherSuite, clientTrafficSecret, serverTrafficSecret []byte, role Role) (*Connection, error) {
	return NewRecordProtector(suite, clientTrafficSecret, serverTrafficSecret, role)
}

// NewRecordLayer is an alias for NewConnection.
func NewRecordLayer(suite CipherSuite, clientTrafficSecret, serverTrafficSecret []byte, role Role) (*RecordLayer, error) {
	return NewRecordProtector(suite, clientTrafficSecret, serverTrafficSecret, role)
}

// NewCipherSuite returns the supported suite for a TLS cipher suite ID.
func NewCipherSuite(id uint16) (CipherSuite, error) {
	suite, ok := CipherSuiteByID(id)
	if !ok {
		return CipherSuite{}, errors.New("tls13: unsupported cipher suite")
	}
	return suite, nil
}

// Direction aliases using send/receive terminology.
const (
	DirectionSend    Direction = Outbound
	DirectionReceive Direction = Inbound
)

// Encrypt seals with default options. Use Seal for padding and outer-header
// controls.
func (s *RecordState) Encrypt(contentType byte, plaintext []byte) ([]byte, error) {
	return s.Seal(contentType, plaintext, nil)
}

// Decrypt opens a complete outer record.
func (s *RecordState) Decrypt(record []byte) (contentType byte, plaintext []byte, err error) {
	return s.Open(record)
}

// UpdateKey is an alias for Rekey.
func (s *RecordState) UpdateKey() error { return s.Rekey() }

// Encrypt uses the outbound state of a bidirectional protector.
func (p *RecordProtector) Encrypt(contentType byte, plaintext []byte) ([]byte, error) {
	return p.Seal(contentType, plaintext, nil)
}

// Decrypt uses the inbound state of a bidirectional protector.
func (p *RecordProtector) Decrypt(record []byte) (byte, []byte, error) {
	return p.Open(record)
}

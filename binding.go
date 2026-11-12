package netsec

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors returned by the endpoint binding package. Callers should
// test them with errors.Is because the package adds context to most errors.
var (
	ErrInvalidJoin           = errors.New("netsec: invalid CAPWAP join request")
	ErrInvalidFingerprint    = errors.New("netsec: invalid certificate fingerprint")
	ErrInvalidEnrollment     = errors.New("netsec: invalid certificate enrollment")
	ErrUnknownCertificate    = errors.New("netsec: DTLS certificate is not enrolled")
	ErrBoardMismatch         = errors.New("netsec: WTP board data does not match the certificate")
	ErrIdentityMismatch      = errors.New("netsec: WTP identity does not match the certificate")
	ErrEnrollmentConflict    = errors.New("netsec: conflicting enrollment for certificate")
	ErrDuplicateJoin         = errors.New("netsec: duplicate join for the same bound endpoint")
	ErrBindingConflict       = errors.New("netsec: endpoint identity is already bound")
	ErrSessionConflict       = errors.New("netsec: session ID is already in use")
	ErrBindingClosed         = errors.New("netsec: endpoint binding is closed")
	ErrUnauthenticatedRecord = errors.New("netsec: record was not authenticated by DTLS")
	ErrConnectionMismatch    = errors.New("netsec: record does not belong to the bound DTLS connection")
	ErrInvalidRecord         = errors.New("netsec: invalid DTLS record")
	ErrReplay                = errors.New("netsec: record sequence number is a replay or outside the replay window")
	ErrStaleEpoch            = errors.New("netsec: record belongs to an older DTLS epoch")
	ErrStaleAddress          = errors.New("netsec: datagram came from a retired source address")
)

// SessionID is the CAPWAP session identifier carried by the join request.
type SessionID uint64

// ConnectionID uniquely identifies one DTLS association. The controller's DTLS
// layer must generate a fresh ID for every handshake; MAC addresses and
// certificate identities are not substitutes for a connection ID.
type ConnectionID string

// WTPBoardData is the identity reported in the CAPWAP WTP Board Data element.
type WTPBoardData struct {
	SerialNumber string
	MAC          net.HardwareAddr
}

// Normalize trims surrounding whitespace from the serial number and returns a
// copy of the MAC. It does not invent or rewrite board data.
func (b WTPBoardData) Normalize() WTPBoardData {
	return WTPBoardData{
		SerialNumber: strings.TrimSpace(b.SerialNumber),
		MAC:          append(net.HardwareAddr(nil), b.MAC...),
	}
}

// Enrollment is the controller's trusted mapping for one AP certificate. The
// certificate fingerprint is only accepted for the enrolled WTP identity,
// board serial number, and MAC.
type Enrollment struct {
	Fingerprint  Fingerprint
	WTPID        string
	SerialNumber string
	MAC          net.HardwareAddr
}

// BoardData returns the enrolled board identity.
func (e Enrollment) BoardData() WTPBoardData {
	return WTPBoardData{SerialNumber: e.SerialNumber, MAC: e.MAC}
}

// JoinRequest is the part of a CAPWAP Join Request that is relevant to
// endpoint binding. Certificate, CertificateDER, and Fingerprint are
// alternatives; when more than one is present they must agree.
type JoinRequest struct {
	WTPID      string
	SessionID  SessionID
	Connection ConnectionID
	Source     net.Addr
	Board      WTPBoardData

	Certificate    *x509.Certificate
	CertificateDER []byte
	Fingerprint    Fingerprint
}

// ResolveFingerprint returns the certificate fingerprint to use for this join.
// It never derives identity from the attacker-controlled board data.
func (r JoinRequest) ResolveFingerprint() (Fingerprint, error) {
	var (
		fp  Fingerprint
		set bool
	)
	if r.Certificate != nil {
		if len(r.Certificate.Raw) == 0 {
			return Fingerprint{}, fmt.Errorf("%w: certificate has no DER body", ErrInvalidJoin)
		}
		fp = FingerprintOfCertificate(r.Certificate)
		set = true
	}
	if r.CertificateDER != nil {
		if len(r.CertificateDER) == 0 {
			return Fingerprint{}, fmt.Errorf("%w: DER certificate body is empty", ErrInvalidJoin)
		}
		derFP := FingerprintOfDER(r.CertificateDER)
		if set && derFP != fp {
			return Fingerprint{}, fmt.Errorf("%w: certificate and DER body disagree", ErrInvalidJoin)
		}
		fp = derFP
		set = true
	}
	if !r.Fingerprint.IsZero() {
		if set && r.Fingerprint != fp {
			return Fingerprint{}, fmt.Errorf("%w: supplied fingerprint does not match certificate", ErrInvalidJoin)
		}
		fp = r.Fingerprint
		set = true
	}
	if !set || fp.IsZero() {
		return Fingerprint{}, fmt.Errorf("%w: certificate fingerprint is required", ErrInvalidJoin)
	}
	return fp, nil
}

// BindingKey is the complete endpoint identity. A join request is a duplicate
// only when all four components are equal.
type BindingKey struct {
	Fingerprint Fingerprint
	WTPID       string
	SessionID   SessionID
	Connection  ConnectionID
}

// Options controls the binder.
type Options struct {
	// ReplayWindowSize is the number of DTLS record sequence numbers tracked
	// by each binding. Zero selects DefaultReplayWindowSize.
	ReplayWindowSize uint
}

// DefaultOptions returns the default binder options.
func DefaultOptions() Options {
	return Options{ReplayWindowSize: DefaultReplayWindowSize}
}

// EnrollmentLookup resolves a certificate fingerprint from a controller
// database or other external trust store. Return false for an unknown
// certificate.
type EnrollmentLookup func(Fingerprint) (Enrollment, bool)

// Binder owns the enrolled identities and active endpoint bindings. It is safe
// for concurrent use.
type Binder struct {
	mu sync.Mutex

	options Options
	lookup  EnrollmentLookup

	enrollments  map[Fingerprint]Enrollment
	bindings     map[BindingKey]*Binding
	byCert       map[Fingerprint]BindingKey
	byWTP        map[string]BindingKey
	bySession    map[SessionID]BindingKey
	byConnection map[ConnectionID]BindingKey
}

// NewBinder creates a binder with the supplied trusted enrollments. It panics
// if an enrollment is malformed; use Enroll to handle that error instead.
func NewBinder(enrollments ...Enrollment) *Binder {
	return NewBinderWithOptions(DefaultOptions(), enrollments...)
}

// NewBinderWithOptions creates a binder with explicit options. It panics if an
// enrollment is malformed; use Enroll to handle that error instead.
func NewBinderWithOptions(options Options, enrollments ...Enrollment) *Binder {
	if options.ReplayWindowSize == 0 {
		options.ReplayWindowSize = DefaultReplayWindowSize
	}
	b := &Binder{
		options:      options,
		enrollments:  make(map[Fingerprint]Enrollment),
		bindings:     make(map[BindingKey]*Binding),
		byCert:       make(map[Fingerprint]BindingKey),
		byWTP:        make(map[string]BindingKey),
		bySession:    make(map[SessionID]BindingKey),
		byConnection: make(map[ConnectionID]BindingKey),
	}
	for _, enrollment := range enrollments {
		if err := b.Enroll(enrollment); err != nil {
			panic(err)
		}
	}
	return b
}

// NewBinderWithLookup creates a binder whose enrollments are resolved through
// lookup. Enrollments added with Enroll are consulted before lookup.
func NewBinderWithLookup(lookup EnrollmentLookup, options Options) *Binder {
	if options.ReplayWindowSize == 0 {
		options.ReplayWindowSize = DefaultReplayWindowSize
	}
	b := &Binder{
		options:      options,
		lookup:       lookup,
		enrollments:  make(map[Fingerprint]Enrollment),
		bindings:     make(map[BindingKey]*Binding),
		byCert:       make(map[Fingerprint]BindingKey),
		byWTP:        make(map[string]BindingKey),
		bySession:    make(map[SessionID]BindingKey),
		byConnection: make(map[ConnectionID]BindingKey),
	}
	return b
}

// Enroll adds or updates a trusted certificate-to-board mapping. Re-enrolling
// the same fingerprint with identical data is idempotent. Changing an enrolled
// mapping while that certificate has an active binding is rejected.
func (b *Binder) Enroll(enrollment Enrollment) error {
	normalized, err := normalizeEnrollment(enrollment)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.enrollments[normalized.Fingerprint]; ok {
		if !equalEnrollment(existing, normalized) {
			return fmt.Errorf("%w: %s", ErrEnrollmentConflict, normalized.Fingerprint)
		}
		return nil
	}
	if key, active := b.byCert[normalized.Fingerprint]; active {
		if binding := b.bindings[key]; binding != nil && equalEnrollment(binding.enrollment, normalized) {
			return nil
		}
		return fmt.Errorf("%w: certificate has an active binding", ErrEnrollmentConflict)
	}
	b.enrollments[normalized.Fingerprint] = normalized
	return nil
}

// Revoke removes a trusted enrollment and closes its active binding, if any.
func (b *Binder) Revoke(fingerprint Fingerprint) {
	b.mu.Lock()
	delete(b.enrollments, fingerprint)
	var binding *Binding
	if key, ok := b.byCert[fingerprint]; ok {
		binding = b.bindings[key]
	}
	b.mu.Unlock()
	if binding != nil {
		_ = binding.Close()
	}
}

// BindJoin validates a CAPWAP Join Request and creates the endpoint binding.
// The certificate fingerprint must resolve to an enrolled WTP identity whose
// serial number and MAC equal the values in the join request. Neither the DTLS
// connection, the certificate, the WTP identity, nor the CAPWAP session may
// already carry another active binding.
func (b *Binder) BindJoin(request JoinRequest) (*Binding, error) {
	fingerprint, err := request.ResolveFingerprint()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.WTPID) == "" {
		return nil, fmt.Errorf("%w: WTP identity is required", ErrInvalidJoin)
	}
	if strings.TrimSpace(string(request.Connection)) == "" {
		return nil, fmt.Errorf("%w: DTLS connection ID is required", ErrInvalidJoin)
	}
	board, err := normalizeBoard(request.Board)
	if err != nil {
		return nil, err
	}
	sourceKey := ""
	if request.Source != nil {
		sourceKey, err = addressKey(request.Source)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidJoin, err)
		}
	}

	b.mu.Lock()
	if enrollment, ok := b.enrollments[fingerprint]; ok {
		defer b.mu.Unlock()
		return b.bindJoinLocked(request, fingerprint, board, sourceKey, enrollment)
	}
	lookup := b.lookup
	b.mu.Unlock()

	if lookup == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCertificate, fingerprint)
	}
	enrollment, ok := lookup(fingerprint)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCertificate, fingerprint)
	}
	enrollment, err = normalizeEnrollment(enrollment)
	if err != nil {
		return nil, err
	}
	if enrollment.Fingerprint != fingerprint {
		return nil, fmt.Errorf("%w: lookup returned a different certificate", ErrUnknownCertificate)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if local, ok := b.enrollments[fingerprint]; ok {
		enrollment = local
	}
	return b.bindJoinLocked(request, fingerprint, board, sourceKey, enrollment)
}

// bindJoinLocked validates the trusted identity and installs the binding. The
// caller must hold b.mu so enrollment revocation cannot race with installation.
func (b *Binder) bindJoinLocked(request JoinRequest, fingerprint Fingerprint, board WTPBoardData, sourceKey string, enrollment Enrollment) (*Binding, error) {
	if !equalBoard(enrollment, board) {
		return nil, fmt.Errorf("%w: enrolled serial/MAC does not match join request", ErrBoardMismatch)
	}
	if !equalIdentity(enrollment.WTPID, request.WTPID) {
		return nil, fmt.Errorf("%w: enrolled WTP identity does not match join request", ErrIdentityMismatch)
	}

	key := BindingKey{
		Fingerprint: fingerprint,
		WTPID:       strings.TrimSpace(request.WTPID),
		SessionID:   request.SessionID,
		Connection:  request.Connection,
	}
	if _, exists := b.bindings[key]; exists {
		return nil, fmt.Errorf("%w: %s/%s/session %d/connection %s", ErrDuplicateJoin,
			key.Fingerprint, key.WTPID, key.SessionID, key.Connection)
	}
	if existing, exists := b.byCert[key.Fingerprint]; exists {
		return nil, fmt.Errorf("%w: certificate already bound as %s/session %d/connection %s",
			ErrBindingConflict, existing.WTPID, existing.SessionID, existing.Connection)
	}
	if existing, exists := b.byWTP[key.WTPID]; exists {
		return nil, fmt.Errorf("%w: WTP %s already bound to certificate %s/session %d/connection %s",
			ErrBindingConflict, key.WTPID, existing.Fingerprint, existing.SessionID, existing.Connection)
	}
	if existing, exists := b.bySession[key.SessionID]; exists {
		return nil, fmt.Errorf("%w: session %d already bound to %s/%s/connection %s",
			ErrSessionConflict, key.SessionID, existing.Fingerprint, existing.WTPID, existing.Connection)
	}
	if existing, exists := b.byConnection[key.Connection]; exists {
		return nil, fmt.Errorf("%w: DTLS connection %s already bound to %s/%s/session %d",
			ErrBindingConflict, key.Connection, existing.Fingerprint, existing.WTPID, existing.SessionID)
	}

	binding := &Binding{
		binder:         b,
		key:            key,
		enrollment:     enrollment,
		sourceKey:      sourceKey,
		source:         cloneAddr(request.Source),
		retiredSources: make(map[string]struct{}),
		window:         NewReplayWindow(b.options.ReplayWindowSize),
	}
	b.bindings[key] = binding
	b.byCert[key.Fingerprint] = key
	b.byWTP[key.WTPID] = key
	b.bySession[key.SessionID] = key
	b.byConnection[key.Connection] = key
	return binding, nil
}

// Binding returns the active binding for key.
func (b *Binder) Binding(key BindingKey) (*Binding, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	binding, ok := b.bindings[key]
	return binding, ok
}

// BindingByFingerprint returns the active binding for a certificate.
func (b *Binder) BindingByFingerprint(fingerprint Fingerprint) (*Binding, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key, ok := b.byCert[fingerprint]
	if !ok {
		return nil, false
	}
	binding, ok := b.bindings[key]
	return binding, ok
}

// BindingByWTP returns the active binding for a WTP identity.
func (b *Binder) BindingByWTP(wtpID string) (*Binding, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key, ok := b.byWTP[strings.TrimSpace(wtpID)]
	if !ok {
		return nil, false
	}
	binding, ok := b.bindings[key]
	return binding, ok
}

// BindingBySession returns the active binding for a CAPWAP session ID.
func (b *Binder) BindingBySession(sessionID SessionID) (*Binding, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key, ok := b.bySession[sessionID]
	if !ok {
		return nil, false
	}
	binding, ok := b.bindings[key]
	return binding, ok
}

// BindingByConnection returns the active binding for a DTLS connection.
func (b *Binder) BindingByConnection(connection ConnectionID) (*Binding, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key, ok := b.byConnection[connection]
	if !ok {
		return nil, false
	}
	binding, ok := b.bindings[key]
	return binding, ok
}

// Bindings returns a snapshot of active bindings.
func (b *Binder) Bindings() []*Binding {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]*Binding, 0, len(b.bindings))
	for _, binding := range b.bindings {
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].key, result[j].key
		if left.Fingerprint != right.Fingerprint {
			return bytes.Compare(left.Fingerprint[:], right.Fingerprint[:]) < 0
		}
		if left.WTPID != right.WTPID {
			return left.WTPID < right.WTPID
		}
		if left.SessionID != right.SessionID {
			return left.SessionID < right.SessionID
		}
		return left.Connection < right.Connection
	})
	return result
}

// CloseAll closes every active binding.
func (b *Binder) CloseAll() {
	for _, binding := range b.Bindings() {
		_ = binding.Close()
	}
}

func (b *Binder) release(binding *Binding) {
	key := binding.key
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.bindings, key)
	if current, ok := b.byCert[key.Fingerprint]; ok && current == key {
		delete(b.byCert, key.Fingerprint)
	}
	if current, ok := b.byWTP[key.WTPID]; ok && current == key {
		delete(b.byWTP, key.WTPID)
	}
	if current, ok := b.bySession[key.SessionID]; ok && current == key {
		delete(b.bySession, key.SessionID)
	}
	if current, ok := b.byConnection[key.Connection]; ok && current == key {
		delete(b.byConnection, key.Connection)
	}
}

// Binding is one authenticated CAPWAP control channel. It records the
// certificate, WTP identity, session ID, DTLS connection, current source
// address, and DTLS replay window.
type Binding struct {
	mu sync.Mutex

	binder     *Binder
	key        BindingKey
	enrollment Enrollment

	sourceKey      string
	source         net.Addr
	retiredSources map[string]struct{}

	epoch    uint64
	epochSet bool
	window   *ReplayWindow

	closed bool
}

// Record is a decrypted CAPWAP control record. Authenticated must only be set
// by the DTLS layer after it has verified the record's integrity tag and
// decryption. A zero-value Record is therefore rejected.
type Record struct {
	Connection    ConnectionID
	Source        net.Addr
	Epoch         uint64
	Sequence      uint64
	Payload       []byte
	Authenticated bool
}

// AuthenticatedRecord is an alias kept for callers that name the post-DTLS
// type explicitly.
type AuthenticatedRecord = Record

// NewAuthenticatedRecord builds a record already verified by the DTLS layer.
// It is intended for the DTLS adapter and tests; production callers must not
// set Authenticated on unverified input.
func NewAuthenticatedRecord(connection ConnectionID, source net.Addr, epoch, sequence uint64, payload []byte) Record {
	return Record{
		Connection:    connection,
		Source:        source,
		Epoch:         epoch,
		Sequence:      sequence,
		Payload:       payload,
		Authenticated: true,
	}
}

// ControlMessage is a control record that passed connection, authentication,
// source, and replay checks.
type ControlMessage struct {
	Connection ConnectionID
	Source     net.Addr
	Epoch      uint64
	Sequence   uint64
	Payload    []byte
}

// Key returns the immutable binding identity.
func (b *Binding) Key() BindingKey {
	return b.key
}

// Fingerprint returns the bound certificate fingerprint.
func (b *Binding) Fingerprint() Fingerprint {
	return b.key.Fingerprint
}

// WTPID returns the bound WTP identity.
func (b *Binding) WTPID() string {
	return b.key.WTPID
}

// SessionID returns the bound CAPWAP session ID.
func (b *Binding) SessionID() SessionID {
	return b.key.SessionID
}

// Connection returns the bound DTLS connection ID.
func (b *Binding) Connection() ConnectionID {
	return b.key.Connection
}

// Enrollment returns the trusted identity attached to the binding.
func (b *Binding) Enrollment() Enrollment {
	enrollment := b.enrollment
	enrollment.MAC = append(net.HardwareAddr(nil), enrollment.MAC...)
	return enrollment
}

// Source returns the currently bound source address. It can be nil if the join
// request did not carry one; the first authenticated control record then
// establishes the address.
func (b *Binding) Source() net.Addr {
	b.mu.Lock()
	defer b.mu.Unlock()
	return cloneAddr(b.source)
}

// Closed reports whether the binding has been closed.
func (b *Binding) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// Process handles one control record. It rejects the record unless it is
// authenticated, belongs to this DTLS connection, is not from a retired
// source, and its sequence number is accepted by the replay window. A fresh
// authenticated record from a new source migrates the binding; the previous
// source is retired before its payload is returned, and all later datagrams
// from it are dropped.
func (b *Binding) Process(record Record) (ControlMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ControlMessage{}, ErrBindingClosed
	}
	if b.window == nil || b.retiredSources == nil {
		return ControlMessage{}, ErrBindingClosed
	}
	if !record.Authenticated {
		return ControlMessage{}, ErrUnauthenticatedRecord
	}
	if record.Connection != b.key.Connection {
		return ControlMessage{}, fmt.Errorf("%w: got %s, want %s",
			ErrConnectionMismatch, record.Connection, b.key.Connection)
	}
	if record.Source == nil {
		return ControlMessage{}, fmt.Errorf("%w: source address is required", ErrInvalidRecord)
	}
	sourceKey, err := addressKey(record.Source)
	if err != nil {
		return ControlMessage{}, err
	}

	if _, retired := b.retiredSources[sourceKey]; retired {
		return ControlMessage{}, fmt.Errorf("%w: %s", ErrStaleAddress, record.Source)
	}
	if err := b.acceptSequence(record.Epoch, record.Sequence); err != nil {
		return ControlMessage{}, err
	}

	if b.sourceKey == "" {
		b.sourceKey = sourceKey
		b.source = cloneAddr(record.Source)
	} else if sourceKey != b.sourceKey {
		b.retiredSources[b.sourceKey] = struct{}{}
		b.sourceKey = sourceKey
		b.source = cloneAddr(record.Source)
	}

	return ControlMessage{
		Connection: record.Connection,
		Source:     cloneAddr(record.Source),
		Epoch:      record.Epoch,
		Sequence:   record.Sequence,
		Payload:    record.Payload,
	}, nil
}

// ProcessPayload is Process returning only the control payload.
func (b *Binding) ProcessPayload(record Record) ([]byte, error) {
	message, err := b.Process(record)
	if err != nil {
		return nil, err
	}
	return message.Payload, nil
}

// AcceptControl is an alias for Process.
func (b *Binding) AcceptControl(record Record) (ControlMessage, error) {
	return b.Process(record)
}

// Close deactivates the binding and removes it from the binder. Closing is
// idempotent; subsequent control records are rejected.
func (b *Binding) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	if b.binder != nil {
		b.binder.release(b)
	}
	return nil
}

func (b *Binding) acceptSequence(epoch, sequence uint64) error {
	if !b.epochSet {
		b.epoch = epoch
		b.epochSet = true
		b.window.Accept(sequence)
		return nil
	}
	if epoch < b.epoch {
		return fmt.Errorf("%w: got epoch %d, current epoch %d", ErrStaleEpoch, epoch, b.epoch)
	}
	if epoch > b.epoch {
		b.epoch = epoch
		b.window.Reset()
		b.window.Accept(sequence)
		return nil
	}
	if !b.window.Accept(sequence) {
		return fmt.Errorf("%w: sequence %d", ErrReplay, sequence)
	}
	return nil
}

func normalizeEnrollment(enrollment Enrollment) (Enrollment, error) {
	if enrollment.Fingerprint.IsZero() {
		return Enrollment{}, fmt.Errorf("%w: fingerprint is required", ErrInvalidEnrollment)
	}
	enrollment.WTPID = strings.TrimSpace(enrollment.WTPID)
	if enrollment.WTPID == "" {
		return Enrollment{}, fmt.Errorf("%w: WTP identity is required", ErrInvalidEnrollment)
	}
	board, err := normalizeBoard(enrollment.BoardData())
	if err != nil {
		return Enrollment{}, fmt.Errorf("%w: %w", ErrInvalidEnrollment, err)
	}
	enrollment.SerialNumber = board.SerialNumber
	enrollment.MAC = board.MAC
	return enrollment, nil
}

func normalizeBoard(board WTPBoardData) (WTPBoardData, error) {
	board = board.Normalize()
	if board.SerialNumber == "" {
		return WTPBoardData{}, fmt.Errorf("%w: board serial number is required", ErrInvalidJoin)
	}
	if len(board.MAC) != 6 && len(board.MAC) != 8 {
		return WTPBoardData{}, fmt.Errorf("%w: board MAC must contain 6 or 8 bytes", ErrInvalidJoin)
	}
	return board, nil
}

func equalEnrollment(a, b Enrollment) bool {
	return a.Fingerprint == b.Fingerprint &&
		equalIdentity(a.WTPID, b.WTPID) &&
		equalBoard(a, WTPBoardData{SerialNumber: b.SerialNumber, MAC: b.MAC})
}

func equalBoard(enrollment Enrollment, board WTPBoardData) bool {
	return enrollment.SerialNumber == board.SerialNumber &&
		bytes.Equal(enrollment.MAC, board.MAC)
}

func equalIdentity(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func cloneAddr(addr net.Addr) net.Addr {
	switch value := addr.(type) {
	case *net.UDPAddr:
		if value == nil {
			return nil
		}
		copyAddr := *value
		copyAddr.IP = append(net.IP(nil), value.IP...)
		return &copyAddr
	case *net.TCPAddr:
		if value == nil {
			return nil
		}
		copyAddr := *value
		copyAddr.IP = append(net.IP(nil), value.IP...)
		return &copyAddr
	default:
		return addr
	}
}

// addressKey canonicalizes the transport address. The source IP and port are
// part of the key; the network ("udp" or "tcp") prevents cross-protocol
// aliasing.
func addressKey(addr net.Addr) (string, error) {
	if addr == nil {
		return "", fmt.Errorf("%w: source address is required", ErrInvalidRecord)
	}
	switch value := addr.(type) {
	case *net.UDPAddr:
		if value == nil {
			return "", fmt.Errorf("%w: source address is required", ErrInvalidRecord)
		}
		if value.Port < 0 || value.Port > 65535 {
			return "", fmt.Errorf("%w: UDP port %d is out of range", ErrInvalidRecord, value.Port)
		}
		if ip, ok := netip.AddrFromSlice(value.IP); ok {
			if value.Zone != "" {
				ip = ip.WithZone(value.Zone)
			}
			ip = ip.Unmap()
			return "udp|" + netip.AddrPortFrom(ip, uint16(value.Port)).String(), nil
		}
		return "udp|" + value.String(), nil
	case *net.TCPAddr:
		if value == nil {
			return "", fmt.Errorf("%w: source address is required", ErrInvalidRecord)
		}
		if value.Port < 0 || value.Port > 65535 {
			return "", fmt.Errorf("%w: TCP port %d is out of range", ErrInvalidRecord, value.Port)
		}
		if ip, ok := netip.AddrFromSlice(value.IP); ok {
			if value.Zone != "" {
				ip = ip.WithZone(value.Zone)
			}
			ip = ip.Unmap()
			return "tcp|" + netip.AddrPortFrom(ip, uint16(value.Port)).String(), nil
		}
		return "tcp|" + value.String(), nil
	default:
		return addr.Network() + "|" + addr.String(), nil
	}
}

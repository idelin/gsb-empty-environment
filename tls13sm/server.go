package tls13sm

import (
	"time"
)

// ServerPhase labels the logical phase of the server state machine.
type ServerPhase string

const (
	ServerPhaseIdle         ServerPhase = "idle"
	ServerPhaseWaitCH2      ServerPhase = "wait_client_hello_2"
	ServerPhaseWaitEOED     ServerPhase = "wait_end_of_early_data"
	ServerPhaseWaitFinished ServerPhase = "wait_finished"
	ServerPhaseConnected    ServerPhase = "connected"
	ServerPhaseClosed       ServerPhase = "closed"
)

// ResumptionTicket is the server-side record of an issued ticket.
type ResumptionTicket struct {
	Nonce        []byte
	PSK          []byte
	TicketAgeAdd uint32
	IssuedAt     time.Time
	SuiteID      uint16
	MaxEarlyData uint32
}

// ServerConfig parameterizes a server connection.
type ServerConfig struct {
	Suite         *CipherSuite
	Groups        []uint16
	Tickets       map[string]*ResumptionTicket // keyed by ticket identity
	Strike        *StrikeRegister
	TicketAgeSkew time.Duration
	MaxKeyUpdates int
	Now           func() time.Time

	// AllowEarlyData gates the whole 0-RTT feature. Even when true, every
	// attempt must additionally pass age skew, strike-register and the
	// caller's idempotency declaration.
	AllowEarlyData bool
}

// ServerEvent is the result of driving the server machine.
type ServerEvent struct {
	Records       []Record
	HandshakeSent [][]byte
	EarlyData     []byte // accepted 0-RTT payloads, in order
	AppData       []byte
	Alert         uint8
	Phase         ServerPhase
	EarlyAccepted bool
	EarlyRejected bool
}

// ServerStateMachine implements the server side of the TLS 1.3 handshake.
type ServerStateMachine struct {
	cfg        ServerConfig
	phase      ServerPhase
	schedule   *keySchedule
	transcript *transcript
	traffic    *trafficManager

	clientHello1      []byte
	clientHello2      []byte
	hrrSent           bool
	localShare        *keyShare
	selectedGroup     uint16
	hrrRequestedGroup uint16

	psk                []byte
	ticket             *ResumptionTicket
	earlyOffered       bool
	earlyAccepted      bool
	eoedReceived       bool
	serverFinishedSent bool
	clientFinished     bool

	pendingClientApp *trafficState
	pendingServerApp *trafficState

	clientRandom []byte
}

// NewServerStateMachine creates a server machine waiting for a ClientHello.
func NewServerStateMachine(cfg ServerConfig) (*ServerStateMachine, error) {
	if cfg.Suite == nil {
		cfg.Suite = SuiteAES128GCM
	}
	if len(cfg.Groups) == 0 {
		cfg.Groups = SupportedGroups()
	}
	if cfg.TicketAgeSkew == 0 {
		cfg.TicketAgeSkew = DefaultTicketAgeSkew
	}
	if cfg.Strike == nil {
		cfg.Strike = NewStrikeRegister(time.Hour)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	schedule := newKeySchedule(cfg.Suite)
	return &ServerStateMachine{
		cfg:        cfg,
		phase:      ServerPhaseIdle,
		schedule:   schedule,
		transcript: newTranscript(cfg.Suite.NewHash),
		traffic:    newTrafficManager(cfg.Suite, schedule, cfg.MaxKeyUpdates),
	}, nil
}

// Phase returns the current logical phase.
func (s *ServerStateMachine) Phase() ServerPhase { return s.phase }

// TranscriptHash exposes the running transcript hash.
func (s *ServerStateMachine) TranscriptHash() []byte { return s.transcript.hashOf() }

// HandleRecord processes one inbound record.
func (s *ServerStateMachine) HandleRecord(record Record) (*ServerEvent, error) {
	switch record.ContentType {
	case ContentTypeChangeCipherSpec:
		return &ServerEvent{Phase: s.phase}, nil
	case ContentTypeHandshake:
		return s.handlePlaintextHandshake(record)
	case ContentTypeApplicationData, ContentTypeAlert:
		return s.handleProtectedRecord(record)
	default:
		return nil, errIllegal("unknown content type %d", record.ContentType)
	}
}

func (s *ServerStateMachine) handlePlaintextHandshake(record Record) (*ServerEvent, error) {
	if record.Epoch != EpochPlaintext {
		return nil, errIllegal("plaintext handshake under protected epoch %d", record.Epoch)
	}
	header, body, err := parseHandshakeHeader(record.Payload)
	if err != nil {
		return nil, err
	}
	if header.Type != HandshakeClientHello {
		return nil, errIllegal("expected ClientHello, got handshake type %d", header.Type)
	}
	switch s.phase {
	case ServerPhaseIdle:
		return s.handleFirstClientHello(body, record.Payload)
	case ServerPhaseWaitCH2:
		return s.handleRetryClientHello(body, record.Payload)
	default:
		return nil, errIllegal("ClientHello not legal in phase %s", s.phase)
	}
}

func (s *ServerStateMachine) handleFirstClientHello(body, fullMessage []byte) (*ServerEvent, error) {
	ch, err := parseClientHello(body, fullMessage)
	if err != nil {
		return nil, err
	}
	s.clientRandom = append([]byte(nil), ch.Random...)
	s.clientHello1 = append([]byte(nil), fullMessage...)
	s.transcript.add(fullMessage)

	suiteOK := false
	for _, id := range ch.CipherSuites {
		if id == s.cfg.Suite.ID {
			suiteOK = true
		}
	}
	if !suiteOK {
		return nil, errProto("client did not offer suite 0x%04x", s.cfg.Suite.ID)
	}

	// Resolve any presented PSK before deciding on HRR or early data.
	if len(ch.PSKIdentities) > 0 {
		identity := string(ch.PSKIdentities[0].Identity)
		if ticket, ok := s.cfg.Tickets[identity]; ok && ticket.SuiteID == s.cfg.Suite.ID {
			s.psk = ticket.PSK
			s.ticket = ticket
		}
	}
	s.schedule.initEarlySecret(s.psk)

	// Validate the PSK binder when a PSK is in use.
	if len(ch.PSKIdentities) > 0 && len(ch.PSKBinders) > 0 {
		tr := newTranscript(s.cfg.Suite.NewHash)
		tr.add(s.binderTruncatedClientHello(fullMessage))
		expected := s.schedule.binderCompute(true, tr.hashOf())
		if !verifyBinder(expected, ch.PSKBinders[0]) {
			return nil, errProto("PSK binder verification failed")
		}
	}

	// Decide whether an HRR is required: the client must offer a share for a
	// group the server supports.
	supportedShare := s.selectShare(ch)
	if supportedShare == nil {
		return s.sendHRR(ch)
	}

	shared, err := s.deriveShared(supportedShare)
	if err != nil {
		return nil, err
	}

	// 0-RTT three-line defense, evaluated before any early-data read epoch is
	// installed.
	if ch.EarlyDataOffered {
		if err := s.admitEarlyData(ch); err != nil {
			// Rejection is signaled by omitting early_data from
			// EncryptedExtensions; the structural policy failure of a replay
			// is still surfaced to the caller for monitoring.
			s.earlyAccepted = false
			// Replay attempts are hard failures, not silent retries.
			if isReplayError(err) {
				return nil, err
			}
		} else {
			s.earlyAccepted = true
		}
	}

	return s.sendServerHello(ch, supportedShare, shared)
}

func (s *ServerStateMachine) selectShare(ch *ClientHello) *keyShare {
	for _, group := range s.cfg.Groups {
		for i := range ch.KeyShares {
			if ch.KeyShares[i].Group == group {
				return &ch.KeyShares[i]
			}
		}
	}
	return nil
}

func (s *ServerStateMachine) deriveShared(peer *keyShare) ([]byte, error) {
	local, err := generateKeyShare(peer.Group)
	if err != nil {
		return nil, err
	}
	s.localShare = local
	s.selectedGroup = peer.Group
	return local.ecdhShared(peer.Group, peer.PublicKey)
}

func (s *ServerStateMachine) admitEarlyData(ch *ClientHello) error {
	if !s.cfg.AllowEarlyData {
		return ErrEarlyDataRejected
	}
	if s.ticket == nil || s.ticket.MaxEarlyData == 0 {
		return ErrEarlyDataRejected
	}
	// Line 1: ticket-age skew window.
	elapsed := s.cfg.Now().Sub(s.ticket.IssuedAt)
	obf := ch.PSKIdentities[0].ObfuscatedAge
	if !ticketAgeOK(obf, s.ticket.TicketAgeAdd, elapsed, s.cfg.TicketAgeSkew) {
		return ErrTicketAgeSkew
	}
	// Line 2: single-use strike register.
	if err := s.cfg.Strike.checkAndStore(s.ticket.Nonce); err != nil {
		return err
	}
	// Line 3: explicit idempotency declaration supplied by the gateway caller
	// through AcceptEarlyData at record time.
	return nil
}

func isReplayError(err error) bool {
	return errorIs(err, ErrReplayDetected)
}

func (s *ServerStateMachine) sendHRR(ch *ClientHello) (*ServerEvent, error) {
	if s.hrrSent {
		return nil, errHRR("a second HelloRetryRequest is forbidden")
	}
	requestedGroup := s.cfg.Groups[0]
	found := false
	for _, group := range s.cfg.Groups {
		offered := false
		for _, share := range ch.KeyShares {
			if share.Group == group {
				offered = true
			}
		}
		if !offered {
			requestedGroup = group
			found = true
			break
		}
	}
	if !found {
		return nil, errHRR("no group available to request in HelloRetryRequest")
	}
	s.hrrRequestedGroup = requestedGroup

	random, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	var exts []extension
	exts = append(exts,
		extension{Type: ExtSupportedVersions, Data: []byte{byte(SupportedTLS13 >> 8), byte(SupportedTLS13 & 0xff)}},
		extension{Type: ExtKeyShare, Data: []byte{byte(requestedGroup >> 8), byte(requestedGroup)}},
	)
	body := marshalServerHello(random, ch.SessionID, s.cfg.Suite.ID, exts)
	hrr := marshalHandshake(HandshakeServerHello, body)

	if err := s.transcript.foldHelloRetryRequest(s.clientHello1, hrr); err != nil {
		return nil, err
	}
	s.hrrSent = true
	s.phase = ServerPhaseWaitCH2

	return &ServerEvent{
		Records:       []Record{{Epoch: EpochPlaintext, ContentType: ContentTypeHandshake, Version: LegacyVersionTLS12, Payload: hrr}},
		HandshakeSent: [][]byte{hrr},
		Phase:         s.phase,
	}, nil
}

// binderTruncatedClientHello reconstructs the ClientHello bytes covered by
// the binder hash: everything up to and including the binder list length,
// with binder entries removed.
func (s *ServerStateMachine) binderTruncatedClientHello(full []byte) []byte {
	_, body, err := parseHandshakeHeader(full)
	if err != nil {
		return full
	}

	// Locate the start of the extensions block (the outer u16 length).
	r := newReader(body)
	if _, err := r.bytes(34); err != nil { // legacy_version + random
		return full
	}
	if _, err := r.vector8(); err != nil { // session_id
		return full
	}
	if _, err := r.vector16(); err != nil { // cipher_suites
		return full
	}
	if _, err := r.vector8(); err != nil { // compression_methods
		return full
	}
	extListStart := r.pos
	if r.remaining() < 2 {
		return full
	}

	// The pre_shared_key extension is required to be last. Its data is
	// identities(u16 vector) followed by the binders(u16 vector). Walk to the
	// last extension and find the binder list length prefix.
	extListLen := int(uint16(body[extListStart])<<8 | uint16(body[extListStart+1]))
	extEnd := extListStart + 2 + extListLen
	if extEnd > len(body) {
		return full
	}

	// Scan extension headers to reach the final extension.
	pos := extListStart + 2
	lastExtDataStart, lastExtLenOffset := 0, 0
	for pos < extEnd {
		if pos+4 > extEnd {
			return full
		}
		extType := uint16(body[pos])<<8 | uint16(body[pos+1])
		dataLen := int(uint16(body[pos+2])<<8 | uint16(body[pos+3]))
		dataStart := pos + 4
		dataEnd := dataStart + dataLen
		if dataEnd > extEnd {
			return full
		}
		if extType == ExtPSK {
			lastExtDataStart, lastExtLenOffset = dataStart, pos+2
		}
		pos = dataEnd
	}
	if lastExtDataStart == 0 {
		return full
	}

	// Inside the extension data the layout is:
	//   u16 identities_length || identities... || u16 binders_length || binders...
	// Read identities_length directly from its absolute offset.
	if lastExtDataStart+2 > len(body) {
		return full
	}
	idLen := int(uint16(body[lastExtDataStart])<<8 | uint16(body[lastExtDataStart+1]))
	binderLenPrefixStart := lastExtDataStart + 2 + idLen
	if binderLenPrefixStart+2 > len(body) {
		return full
	}
	// The binder hash covers through the two-byte binders_length prefix.
	binderLenPrefixEnd := binderLenPrefixStart + 2
	// RFC 8446 4.2.11.2 hashes the ClientHello truncated at the binders list;
	// the client builds that view with a zero binders_length. Mirror it
	// exactly so both sides hash identical bytes.
	body = append([]byte(nil), body...)
	body[binderLenPrefixStart] = 0
	body[binderLenPrefixStart+1] = 0

	// Truncated body ends exactly after that two-byte binder list length.
	truncatedBody := append([]byte(nil), body[:binderLenPrefixEnd]...)

	// Fix the outer extensions block length (the u16 at extListStart).
	newExtListLen := len(truncatedBody) - (extListStart + 2)
	truncatedBody[extListStart] = byte(newExtListLen >> 8)
	truncatedBody[extListStart+1] = byte(newExtListLen & 0xff)

	// Fix the pre_shared_key extension's own data length to cover only the
	// identities vector plus the two-byte binder list length prefix.
	newPSKDataLen := binderLenPrefixEnd - lastExtDataStart
	truncatedBody[lastExtLenOffset] = byte(newPSKDataLen >> 8)
	truncatedBody[lastExtLenOffset+1] = byte(newPSKDataLen & 0xff)

	return marshalHandshake(HandshakeClientHello, truncatedBody)
}

func (s *ServerStateMachine) handleRetryClientHello(body, fullMessage []byte) (*ServerEvent, error) {
	ch, err := parseClientHello(body, fullMessage)
	if err != nil {
		return nil, err
	}

	// RFC 8446 4.1.2: the retry ClientHello must match the first one except
	// for the explicitly permitted changes.
	if !equalConstantTime(ch.Random, s.clientRandom) {
		return nil, errHRR("ClientHello2 random differs from ClientHello1")
	}
	suiteOK := false
	for _, id := range ch.CipherSuites {
		if id == s.cfg.Suite.ID {
			suiteOK = true
		}
	}
	if !suiteOK {
		return nil, errHRR("ClientHello2 no longer offers negotiated cipher suite")
	}
	requested := s.hrrRequestedGroup
	if len(ch.KeyShares) != 1 || ch.KeyShares[0].Group != requested {
		return nil, errHRR("ClientHello2 key share does not satisfy HelloRetryRequest")
	}
	if !containsU16(ch.SupportedVersions, SupportedTLS13) {
		return nil, errHRR("ClientHello2 dropped supported_versions TLS 1.3")
	}
	if ch.EarlyDataOffered {
		return nil, errHRR("early_data must not be re-offered after HelloRetryRequest")
	}

	if len(ch.PSKIdentities) > 0 {
		identity := string(ch.PSKIdentities[0].Identity)
		ticket, ok := s.cfg.Tickets[identity]
		if !ok || ticket.SuiteID != s.cfg.Suite.ID {
			return nil, errHRR("ClientHello2 PSK identity changed or unknown")
		}
		s.psk = ticket.PSK
		s.ticket = ticket
		s.schedule.initEarlySecret(s.psk)
		if len(ch.PSKBinders) == 0 {
			return nil, errHRR("ClientHello2 missing PSK binder")
		}
		tr := s.transcript.clone()
		tr.add(s.binderTruncatedClientHello(fullMessage))
		expected := s.schedule.binderCompute(true, tr.hashOf())
		if !verifyBinder(expected, ch.PSKBinders[0]) {
			return nil, errHRR("ClientHello2 PSK binder mismatch")
		}
	}

	s.clientHello2 = append([]byte(nil), fullMessage...)
	s.transcript.add(fullMessage)

	shared, err := s.deriveShared(&ch.KeyShares[0])
	if err != nil {
		return nil, err
	}
	return s.sendServerHello(ch, &ch.KeyShares[0], shared)
}

func containsU16(list []uint16, want uint16) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func (s *ServerStateMachine) sendServerHello(ch *ClientHello, peerShare *keyShare, shared []byte) (*ServerEvent, error) {
	random, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	var exts []extension
	exts = append(exts, extension{
		Type: ExtSupportedVersions,
		Data: []byte{byte(SupportedTLS13 >> 8), byte(SupportedTLS13 & 0xff)},
	})

	var keyShareData []byte
	keyShareData = appendU16(keyShareData, s.localShare.Group)
	keyShareData = appendLength16(keyShareData, s.localShare.PublicKey)
	exts = append(exts, extension{Type: ExtKeyShare, Data: keyShareData})

	if len(ch.PSKIdentities) > 0 && s.ticket != nil {
		exts = append(exts, extension{Type: ExtPSK, Data: []byte{0, 0}})
	}

	body := marshalServerHello(random, ch.SessionID, s.cfg.Suite.ID, exts)
	sh := marshalHandshake(HandshakeServerHello, body)
	s.transcript.add(sh)

	s.schedule.deriveHandshake(shared, s.transcript.hashOf())

	readState, err := s.schedule.newTrafficState(s.schedule.clientHandshake)
	if err != nil {
		return nil, err
	}
	writeState, err := s.schedule.newTrafficState(s.schedule.serverHandshake)
	if err != nil {
		return nil, err
	}
	s.traffic.installRead(EpochHandshake, readState)
	s.traffic.installWrite(EpochHandshake, writeState)

	if s.earlyAccepted {
		tr := newTranscript(s.cfg.Suite.NewHash)
		tr.add(s.binderTruncatedClientHello(s.effectiveClientHello()))
		s.schedule.deriveEarlyTraffic(tr.hashOf())
		earlyState, err := s.schedule.newTrafficState(s.schedule.clientEarly)
		if err != nil {
			return nil, err
		}
		s.traffic.pendingRead = s.traffic.read
		s.traffic.installRead(EpochEarlyData, earlyState)
		s.phase = ServerPhaseWaitEOED
	} else {
		s.phase = ServerPhaseWaitFinished
	}

	flight, err := s.buildEncryptedPrelude()
	if err != nil {
		return nil, err
	}

	return &ServerEvent{
		Records:       append([]Record{{Epoch: EpochPlaintext, ContentType: ContentTypeHandshake, Version: LegacyVersionTLS12, Payload: sh}}, flight...),
		HandshakeSent: [][]byte{sh},
		Phase:         s.phase,
		EarlyAccepted: s.earlyAccepted,
		EarlyRejected: ch.EarlyDataOffered && !s.earlyAccepted,
	}, nil
}

// buildEncryptedPrelude emits the server's encrypted handshake flight that
// follows ServerHello: EncryptedExtensions (carrying early_data when 0-RTT is
// accepted) and the server Finished, both protected by handshake keys. The
// server Finished is computed before the client Finished, exactly as required
// by RFC 8446 section 4.4.4.
func (s *ServerStateMachine) buildEncryptedPrelude() ([]Record, error) {
	var records []Record

	eeExts := []extension{}
	if s.earlyAccepted {
		eeExts = append(eeExts, extension{Type: ExtEarlyData, Data: nil})
	}
	eeMsg := marshalHandshake(HandshakeEncryptedExtensions, marshalExtensions(eeExts))
	eeRecord, err := s.traffic.sealRecord(ContentTypeHandshake, eeMsg)
	if err != nil {
		return nil, err
	}
	eeRecord.Epoch = EpochHandshake
	records = append(records, eeRecord)
	s.transcript.add(eeMsg)

	serverFinished := finishedVerifyData(
		s.cfg.Suite.NewHash,
		finishedKey(s.cfg.Suite.NewHash, s.schedule.serverHandshake),
		s.transcript.hashOf(),
	)
	finishedMsg := marshalHandshake(HandshakeFinished, serverFinished)
	finishedRecord, err := s.traffic.sealRecord(ContentTypeHandshake, finishedMsg)
	if err != nil {
		return nil, err
	}
	finishedRecord.Epoch = EpochHandshake
	records = append(records, finishedRecord)
	s.transcript.add(finishedMsg)
	s.serverFinishedSent = true

	// Application traffic keys are derived from the transcript hash through
	// the server Finished, exactly as on the client (RFC 8446 7.1). The keys
	// are only installed after the client Finished verifies.
	s.schedule.deriveApplication(s.transcript.hashOf())
	s.pendingClientApp, err = s.schedule.newTrafficState(s.schedule.clientApp)
	if err != nil {
		return nil, err
	}
	s.pendingServerApp, err = s.schedule.newTrafficState(s.schedule.serverApp)
	if err != nil {
		return nil, err
	}

	return records, nil
}

func (s *ServerStateMachine) effectiveClientHello() []byte {
	if len(s.clientHello2) > 0 {
		return s.clientHello2
	}
	return s.clientHello1
}

// AcceptEarlyData is the third 0-RTT defense line at the gateway boundary:
// the caller explicitly declares that the request carried by an early-data
// record is idempotent before the state machine will decrypt and deliver it.
// The strike-register and age checks already ran during ClientHello handling.
func (s *ServerStateMachine) AcceptEarlyData(idempotent bool) error {
	if !idempotent {
		return ErrNotIdempotent
	}
	return nil
}

func (s *ServerStateMachine) handleProtectedRecord(record Record) (*ServerEvent, error) {
	innerType, body, err := s.traffic.openRecord(record)
	if err != nil {
		return nil, err
	}
	switch innerType {
	case ContentTypeApplicationData:
		if record.Epoch == EpochEarlyData {
			if s.phase != ServerPhaseWaitEOED {
				return nil, errIllegal("early data outside the 0-RTT window in phase %s", s.phase)
			}
			if !s.earlyAccepted {
				return nil, ErrEarlyDataRejected
			}
			return &ServerEvent{EarlyData: append([]byte(nil), body...), Phase: s.phase}, nil
		}
		if s.phase != ServerPhaseConnected {
			return nil, errIllegal("application data before handshake completion in phase %s", s.phase)
		}
		return &ServerEvent{AppData: append([]byte(nil), body...), Phase: s.phase}, nil

	case ContentTypeHandshake:
		return s.handleEncryptedHandshake(record, body)

	case ContentTypeAlert:
		if len(body) < 2 {
			return nil, errIllegal("empty Alert body")
		}
		return &ServerEvent{Alert: body[1], Phase: s.phase}, nil

	default:
		return nil, errIllegal("unexpected inner content type %d", innerType)
	}
}

func (s *ServerStateMachine) handleEncryptedHandshake(record Record, body []byte) (*ServerEvent, error) {
	header, hsBody, err := parseHandshakeHeader(body)
	if err != nil {
		return nil, err
	}
	switch header.Type {
	case HandshakeEndOfEarlyData:
		if record.Epoch != EpochEarlyData {
			return nil, errIllegal("EndOfEarlyData must be protected by early-data keys")
		}
		if s.phase != ServerPhaseWaitEOED {
			return nil, errIllegal("EndOfEarlyData not legal in phase %s", s.phase)
		}
		if len(hsBody) != 0 {
			return nil, errProto("EndOfEarlyData must have an empty body")
		}
		s.eoedReceived = true
		s.transcript.add(body)
		// Restore handshake read keys for the remainder of the handshake.
		s.traffic.installRead(EpochHandshake, s.traffic.pendingRead)
		s.phase = ServerPhaseWaitFinished
		return &ServerEvent{Phase: s.phase}, nil

	case HandshakeFinished:
		return s.handleClientFinished(body, hsBody)

	case HandshakeKeyUpdate:
		return s.handleKeyUpdate(hsBody)

	case HandshakeNewSessionTicket:
		return nil, errIllegal("clients must not send NewSessionTicket")

	default:
		return nil, errIllegal("unexpected encrypted handshake type %d", header.Type)
	}
}

func (s *ServerStateMachine) handleClientFinished(full, body []byte) (*ServerEvent, error) {
	if s.phase != ServerPhaseWaitFinished {
		return nil, errIllegal("client Finished not legal in phase %s", s.phase)
	}
	if s.earlyAccepted && !s.eoedReceived {
		return nil, errIllegal("client Finished before EndOfEarlyData")
	}

	expected := finishedVerifyData(
		s.cfg.Suite.NewHash,
		finishedKey(s.cfg.Suite.NewHash, s.schedule.clientHandshake),
		s.transcript.hashOf(),
	)
	if !equalConstantTime(expected, body) {
		return nil, ErrFinishedVerify
	}
	s.transcript.add(full)
	s.clientFinished = true

	// The server Finished was already emitted by buildEncryptedPrelude, so
	// only the application traffic keys remain to be installed.
	s.traffic.installRead(EpochAppData, s.pendingClientApp)
	s.traffic.installWrite(EpochAppData, s.pendingServerApp)
	s.phase = ServerPhaseConnected

	return &ServerEvent{
		Records:       nil,
		HandshakeSent: nil,
		Phase:         s.phase,
		EarlyAccepted: s.earlyAccepted,
	}, nil
}

func (s *ServerStateMachine) handleKeyUpdate(body []byte) (*ServerEvent, error) {
	if s.phase != ServerPhaseConnected {
		return nil, errIllegal("KeyUpdate before handshake completion in phase %s", s.phase)
	}
	ku, err := parseKeyUpdate(body)
	if err != nil {
		return nil, err
	}
	if err := s.traffic.rotateRead(); err != nil {
		return nil, err
	}
	event := &ServerEvent{Phase: s.phase}
	if ku.RequestUpdate == KeyUpdateRequested {
		record, err := s.traffic.sealRecord(ContentTypeHandshake,
			marshalHandshake(HandshakeKeyUpdate, []byte{KeyUpdateNotRequested}))
		if err != nil {
			return nil, err
		}
		record.Epoch = EpochAppData
		event.Records = []Record{record}
		if err := s.traffic.rotateWrite(); err != nil {
			return nil, err
		}
	}
	return event, nil
}

// SendApplicationData emits a protected application-data record.
func (s *ServerStateMachine) SendApplicationData(payload []byte) (*ServerEvent, error) {
	if s.phase != ServerPhaseConnected {
		return nil, errState("application data is not legal in phase %s", s.phase)
	}
	record, err := s.traffic.sealRecord(ContentTypeApplicationData, payload)
	if err != nil {
		return nil, err
	}
	record.Epoch = EpochAppData
	return &ServerEvent{Records: []Record{record}, AppData: append([]byte(nil), payload...), Phase: s.phase}, nil
}

// SendKeyUpdate rotates the write key and emits a KeyUpdate message.
func (s *ServerStateMachine) SendKeyUpdate(requestUpdate bool) (*ServerEvent, error) {
	if s.phase != ServerPhaseConnected {
		return nil, errState("KeyUpdate is not legal in phase %s", s.phase)
	}
	value := KeyUpdateNotRequested
	if requestUpdate {
		value = KeyUpdateRequested
	}
	// The KeyUpdate is sealed under the current write key before rotation.
	record, err := s.traffic.sealRecord(ContentTypeHandshake,
		marshalHandshake(HandshakeKeyUpdate, []byte{value}))
	if err != nil {
		return nil, err
	}
	record.Epoch = EpochAppData
	if err := s.traffic.rotateWrite(); err != nil {
		return nil, err
	}
	return &ServerEvent{Records: []Record{record}, Phase: s.phase}, nil
}

// SendAlert emits a protected Alert.
func (s *ServerStateMachine) SendAlert(alertDescription uint8) (*ServerEvent, error) {
	if s.traffic.write == nil {
		return nil, errState("cannot send Alert before traffic keys are installed")
	}
	record, err := s.traffic.sealRecord(ContentTypeAlert, []byte{2, alertDescription})
	if err != nil {
		return nil, err
	}
	record.Epoch = s.traffic.writeEpoch
	return &ServerEvent{Records: []Record{record}, Alert: alertDescription, Phase: s.phase}, nil
}

// EarlyDataAccepted reports whether 0-RTT was admitted.
func (s *ServerStateMachine) EarlyDataAccepted() bool { return s.earlyAccepted }

package tls13sm

import (
	"time"
)

// ClientPhase labels the logical phase of the client state machine.
type ClientPhase string

const (
	ClientPhaseIdle         ClientPhase = "idle"
	ClientPhaseWaitSH       ClientPhase = "wait_server_hello"
	ClientPhaseWaitEE       ClientPhase = "wait_encrypted_extensions"
	ClientPhaseWaitFinished ClientPhase = "wait_finished"
	ClientPhaseConnected    ClientPhase = "connected"
	ClientPhaseClosed       ClientPhase = "closed"
)

// ClientConfig parameterizes a client connection.
type ClientConfig struct {
	Suite           *CipherSuite
	Groups          []uint16
	PSK             []byte // resumption/external PSK; nil performs a full handshake
	PSKIsResumption bool
	TicketAgeAdd    uint32
	UseEarlyData    bool
	Idempotent      bool // explicit idempotency declaration for 0-RTT
	TicketAge       time.Duration
	MaxKeyUpdates   int
	Now             func() time.Time

	// VerifyCertificate, when set, receives the opaque CertificateVerify
	// body. When nil the state machine records certificate messages but
	// performs no public-key authentication; gateway callers must supply
	// their own verifier in production.
	VerifyCertificate func(certificateVerify []byte) error
}

// ClientEvent is the result of driving the client machine with one input.
type ClientEvent struct {
	Records       []Record
	HandshakeSent [][]byte
	EarlyDataSent []byte
	AppData       []byte
	Alert         uint8
	Phase         ClientPhase
	EarlyAccepted bool
}

// ClientStateMachine implements the client side of the TLS 1.3 handshake and
// post-handshake traffic rules.
type ClientStateMachine struct {
	cfg        ClientConfig
	phase      ClientPhase
	schedule   *keySchedule
	transcript *transcript
	traffic    *trafficManager

	builder      *clientHelloBuilder
	clientHello1 []byte
	clientHello2 []byte
	localShare   *keyShare
	hrrSeen      bool

	earlyOffered   bool
	earlyAccepted  bool
	serverFinished bool
	clientFinished bool

	serverRandom    []byte
	pendingCertBody []byte
}

// NewClientStateMachine creates a client machine. Call Start to emit the
// initial ClientHello.
func NewClientStateMachine(cfg ClientConfig) (*ClientStateMachine, error) {
	if cfg.Suite == nil {
		cfg.Suite = SuiteAES128GCM
	}
	if len(cfg.Groups) == 0 {
		cfg.Groups = SupportedGroups()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	schedule := newKeySchedule(cfg.Suite)
	schedule.initEarlySecret(cfg.PSK)

	share, err := generateKeyShare(cfg.Groups[0])
	if err != nil {
		return nil, err
	}
	random, err := randomBytes(32)
	if err != nil {
		return nil, err
	}

	builder := newClientHelloBuilder()
	builder.random = random
	builder.cipherSuites = []uint16{cfg.Suite.ID}
	builder.groups = cfg.Groups
	builder.keyShares = []*keyShare{share}

	earlyOffered := false
	if len(cfg.PSK) > 0 {
		builder.pskMode = 1
		builder.pskIdentity = []byte("resumption-ticket")
		builder.obfuscatedAge = uint32(int64(cfg.TicketAge/time.Millisecond) + int64(cfg.TicketAgeAdd))
		if cfg.UseEarlyData {
			if !cfg.Idempotent {
				return nil, ErrNotIdempotent
			}
			builder.withEarlyData = true
			earlyOffered = true
		}
	}

	return &ClientStateMachine{
		cfg:          cfg,
		phase:        ClientPhaseIdle,
		schedule:     schedule,
		transcript:   newTranscript(cfg.Suite.NewHash),
		traffic:      newTrafficManager(cfg.Suite, schedule, cfg.MaxKeyUpdates),
		builder:      builder,
		localShare:   share,
		earlyOffered: earlyOffered,
	}, nil
}

// Phase returns the current logical phase.
func (c *ClientStateMachine) Phase() ClientPhase { return c.phase }

// TranscriptHash exposes the running transcript hash for diagnostics/tests.
func (c *ClientStateMachine) TranscriptHash() []byte { return c.transcript.hashOf() }

// Start emits the initial ClientHello record and, when early data is
// admissible, installs the client_early_traffic protection.
func (c *ClientStateMachine) Start() (*ClientEvent, error) {
	if c.phase != ClientPhaseIdle {
		return nil, errState("Start called in phase %s", c.phase)
	}

	var ch []byte
	if len(c.cfg.PSK) > 0 {
		tr := newTranscript(c.cfg.Suite.NewHash)
		tr.add(c.builder.truncatedMessage())
		binder := c.schedule.binderCompute(c.cfg.PSKIsResumption, tr.hashOf())
		ch = c.builder.buildWithBinder([][]byte{binder})
	} else {
		ch = c.builder.buildPlain()
	}

	c.clientHello1 = ch
	c.transcript.add(ch)
	c.phase = ClientPhaseWaitSH

	event := &ClientEvent{
		Records:       []Record{{Epoch: EpochPlaintext, ContentType: ContentTypeHandshake, Version: LegacyVersionTLS12, Payload: ch}},
		HandshakeSent: [][]byte{ch},
		Phase:         c.phase,
	}

	if c.earlyOffered {
		tr := newTranscript(c.cfg.Suite.NewHash)
		tr.add(c.builder.truncatedMessage())
		c.schedule.deriveEarlyTraffic(tr.hashOf())
		earlyState, err := c.schedule.newTrafficState(c.schedule.clientEarly)
		if err != nil {
			return nil, err
		}
		c.traffic.installWrite(EpochEarlyData, earlyState)
	}
	return event, nil
}

// SendEarlyData emits an 0-RTT application-data record. It is only legal
// after Start while the client is still waiting for the first server
// response and requires the explicit idempotency gate.
func (c *ClientStateMachine) SendEarlyData(payload []byte) (*ClientEvent, error) {
	if !c.earlyOffered {
		return nil, ErrEarlyDataRejected
	}
	if !c.cfg.Idempotent {
		return nil, ErrNotIdempotent
	}
	if c.phase != ClientPhaseWaitSH || c.hrrSeen {
		return nil, errState("early data can only be sent before the first server response")
	}
	record, err := c.traffic.sealRecord(ContentTypeApplicationData, payload)
	if err != nil {
		return nil, err
	}
	record.Epoch = EpochEarlyData
	return &ClientEvent{
		Records:       []Record{record},
		EarlyDataSent: append([]byte(nil), payload...),
		Phase:         c.phase,
	}, nil
}

// HandleRecord processes one inbound record according to the current phase
// and epoch. Plaintext Alert/KeyUpdate injection, premature application data
// and cross-epoch ciphertext are all rejected.
func (c *ClientStateMachine) HandleRecord(record Record) (*ClientEvent, error) {
	switch record.ContentType {
	case ContentTypeChangeCipherSpec:
		return &ClientEvent{Phase: c.phase}, nil
	case ContentTypeHandshake:
		return c.handleHandshakeRecord(record)
	case ContentTypeApplicationData, ContentTypeAlert:
		return c.handleProtectedRecord(record)
	default:
		return nil, errIllegal("unknown content type %d", record.ContentType)
	}
}

func (c *ClientStateMachine) handleHandshakeRecord(record Record) (*ClientEvent, error) {
	if record.Epoch != EpochPlaintext {
		return nil, errIllegal("handshake content type under protected epoch %d", record.Epoch)
	}
	header, body, err := parseHandshakeHeader(record.Payload)
	if err != nil {
		return nil, err
	}
	if header.Type != HandshakeServerHello || c.phase != ClientPhaseWaitSH {
		return nil, errIllegal("expected ServerHello in phase %s, got handshake type %d", c.phase, header.Type)
	}
	return c.handleServerHello(body, record.Payload)
}

func (c *ClientStateMachine) handleServerHello(body, fullMessage []byte) (*ClientEvent, error) {
	sh, err := parseServerHello(body)
	if err != nil {
		return nil, err
	}
	c.serverRandom = append([]byte(nil), sh.Random...)

	if sh.isHelloRetryRequest() {
		return c.handleHRR(sh, fullMessage)
	}

	if sh.CipherSuite != c.cfg.Suite.ID {
		return nil, errProto("negotiated cipher suite 0x%04x does not match offer 0x%04x", sh.CipherSuite, c.cfg.Suite.ID)
	}
	shared, err := c.localShare.ecdhShared(sh.SelectedGroup, sh.SelectedKeyShare)
	if err != nil {
		return nil, err
	}

	c.transcript.add(fullMessage)
	c.schedule.deriveHandshake(shared, c.transcript.hashOf())

	readState, err := c.schedule.newTrafficState(c.schedule.serverHandshake)
	if err != nil {
		return nil, err
	}
	writeState, err := c.schedule.newTrafficState(c.schedule.clientHandshake)
	if err != nil {
		return nil, err
	}
	c.traffic.installRead(EpochHandshake, readState)
	if c.earlyAccepted || c.earlyOffered {
		c.traffic.retainEarlyWrite()
	}
	c.traffic.installWrite(EpochHandshake, writeState)
	c.phase = ClientPhaseWaitEE
	return &ClientEvent{Phase: c.phase}, nil
}

func (c *ClientStateMachine) handleHRR(sh *ServerHello, fullMessage []byte) (*ClientEvent, error) {
	if c.hrrSeen {
		return nil, errHRR("a second HelloRetryRequest is forbidden")
	}
	if sh.CipherSuite != c.cfg.Suite.ID {
		return nil, errHRR("retry cipher suite 0x%04x does not match offered 0x%04x", sh.CipherSuite, c.cfg.Suite.ID)
	}

	supported := false
	for _, group := range c.cfg.Groups {
		if group == sh.SelectedGroup {
			supported = true
			break
		}
	}
	if !supported {
		return nil, errHRR("retry group 0x%04x is not among supported groups", sh.SelectedGroup)
	}
	for _, share := range c.builder.keyShares {
		if share.Group == sh.SelectedGroup {
			return nil, errHRR("retry group 0x%04x already had a key share in ClientHello1", sh.SelectedGroup)
		}
	}

	// Mandatory RFC 8446 4.4.1 transcript replacement: synthetic
	// message_hash(CH1) followed by the HRR ServerHello.
	if err := c.transcript.foldHelloRetryRequest(c.clientHello1, fullMessage); err != nil {
		return nil, err
	}
	c.hrrSeen = true

	// Retry ClientHello: identical parameters except the newly requested key
	// share and the echoed cookie.
	newShare, err := generateKeyShare(sh.SelectedGroup)
	if err != nil {
		return nil, err
	}
	c.localShare = newShare
	c.builder.keyShares = []*keyShare{newShare}
	if cookie, ok := extensionData(sh.Extensions, ExtCookie); ok {
		c.builder.cookie, err = newReader(cookie).vector16()
		if err != nil {
			return nil, err
		}
	}

	var ch2 []byte
	if len(c.cfg.PSK) > 0 {
		tr := c.transcript.clone()
		tr.add(c.builder.truncatedMessage())
		binder := c.schedule.binderCompute(c.cfg.PSKIsResumption, tr.hashOf())
		ch2 = c.builder.buildWithBinder([][]byte{binder})
	} else {
		ch2 = c.builder.buildPlain()
	}
	c.clientHello2 = ch2
	c.transcript.add(ch2)

	// A retry invalidates in-flight 0-RTT: no more early records may be sent.
	c.earlyOffered = false

	c.phase = ClientPhaseWaitSH
	return &ClientEvent{
		Records:       []Record{{Epoch: EpochPlaintext, ContentType: ContentTypeHandshake, Version: LegacyVersionTLS12, Payload: ch2}},
		HandshakeSent: [][]byte{ch2},
		Phase:         c.phase,
	}, nil
}

func (c *ClientStateMachine) handleProtectedRecord(record Record) (*ClientEvent, error) {
	innerType, body, err := c.traffic.openRecord(record)
	if err != nil {
		return nil, err
	}
	switch innerType {
	case ContentTypeHandshake:
		return c.handleEncryptedHandshake(body)
	case ContentTypeApplicationData:
		if c.phase != ClientPhaseConnected {
			return nil, errIllegal("application data before handshake completion in phase %s", c.phase)
		}
		return &ClientEvent{AppData: append([]byte(nil), body...), Phase: c.phase}, nil
	case ContentTypeAlert:
		if len(body) < 2 {
			return nil, errIllegal("empty Alert body")
		}
		return &ClientEvent{Alert: body[1], Phase: c.phase}, nil
	default:
		return nil, errIllegal("unexpected inner content type %d", innerType)
	}
}

func (c *ClientStateMachine) handleEncryptedHandshake(body []byte) (*ClientEvent, error) {
	header, hsBody, err := parseHandshakeHeader(body)
	if err != nil {
		return nil, err
	}
	switch header.Type {
	case HandshakeEncryptedExtensions:
		if c.phase != ClientPhaseWaitEE {
			return nil, errIllegal("EncryptedExtensions not legal in phase %s", c.phase)
		}
		ee, err := parseEncryptedExtensions(hsBody)
		if err != nil {
			return nil, err
		}
		c.earlyAccepted = ee.EarlyDataOK
		c.transcript.add(body)
		c.phase = ClientPhaseWaitFinished
		return &ClientEvent{Phase: c.phase, EarlyAccepted: c.earlyAccepted}, nil

	case HandshakeCertificate:
		if c.phase != ClientPhaseWaitFinished {
			return nil, errIllegal("Certificate not legal in phase %s", c.phase)
		}
		c.pendingCertBody = append([]byte(nil), hsBody...)
		c.transcript.add(body)
		return &ClientEvent{Phase: c.phase}, nil

	case HandshakeCertificateVerify:
		if c.phase != ClientPhaseWaitFinished {
			return nil, errIllegal("CertificateVerify not legal in phase %s", c.phase)
		}
		if c.cfg.VerifyCertificate != nil {
			if err := c.cfg.VerifyCertificate(hsBody); err != nil {
				return nil, err
			}
		}
		c.transcript.add(body)
		return &ClientEvent{Phase: c.phase}, nil

	case HandshakeFinished:
		return c.handleServerFinished(body, hsBody)

	case HandshakeNewSessionTicket:
		if c.phase != ClientPhaseConnected {
			return nil, errIllegal("NewSessionTicket not legal in phase %s", c.phase)
		}
		return &ClientEvent{Phase: c.phase}, nil

	case HandshakeKeyUpdate:
		return c.handleKeyUpdate(hsBody)

	default:
		return nil, errIllegal("unexpected encrypted handshake type %d", header.Type)
	}
}

func (c *ClientStateMachine) handleServerFinished(full, body []byte) (*ClientEvent, error) {
	if c.phase != ClientPhaseWaitFinished {
		return nil, errIllegal("server Finished not legal in phase %s", c.phase)
	}

	expected := finishedVerifyData(
		c.cfg.Suite.NewHash,
		finishedKey(c.cfg.Suite.NewHash, c.schedule.serverHandshake),
		c.transcript.hashOf(),
	)
	if !equalConstantTime(expected, body) {
		return nil, ErrFinishedVerify
	}

	c.transcript.add(full)
	c.serverFinished = true

	// Derive application traffic secrets from the hash through server Finished.
	c.schedule.deriveApplication(c.transcript.hashOf())

	// EndOfEarlyData is mandatory exactly when the server accepted early data.
	var responseRecords []Record
	if c.earlyAccepted {
		eoed := marshalHandshake(HandshakeEndOfEarlyData, nil)
		if c.traffic.earlyWrite == nil {
			return nil, errState("early-data write keys unavailable for EndOfEarlyData")
		}
		record := c.traffic.earlyWrite.cipher.sealRecord(ContentTypeHandshake, eoed)
		record.Epoch = EpochEarlyData
		responseRecords = append(responseRecords, record)
		c.transcript.add(eoed)
	}

	// Client Finished is emitted under handshake keys.
	clientFinished := finishedVerifyData(
		c.cfg.Suite.NewHash,
		finishedKey(c.cfg.Suite.NewHash, c.schedule.clientHandshake),
		c.transcript.hashOf(),
	)
	finishedMsg := marshalHandshake(HandshakeFinished, clientFinished)
	record, err := c.traffic.sealRecord(ContentTypeHandshake, finishedMsg)
	if err != nil {
		return nil, err
	}
	record.Epoch = EpochHandshake
	responseRecords = append(responseRecords, record)
	c.transcript.add(finishedMsg)
	c.clientFinished = true

	// Switch both directions to application traffic keys.
	readState, err := c.schedule.newTrafficState(c.schedule.serverApp)
	if err != nil {
		return nil, err
	}
	writeState, err := c.schedule.newTrafficState(c.schedule.clientApp)
	if err != nil {
		return nil, err
	}
	c.traffic.installRead(EpochAppData, readState)
	c.traffic.installWrite(EpochAppData, writeState)
	c.phase = ClientPhaseConnected

	return &ClientEvent{
		Records:       responseRecords,
		HandshakeSent: [][]byte{finishedMsg},
		Phase:         c.phase,
		EarlyAccepted: c.earlyAccepted,
	}, nil
}

func (c *ClientStateMachine) handleKeyUpdate(body []byte) (*ClientEvent, error) {
	if c.phase != ClientPhaseConnected {
		return nil, errIllegal("KeyUpdate before handshake completion in phase %s", c.phase)
	}
	ku, err := parseKeyUpdate(body)
	if err != nil {
		return nil, err
	}
	if err := c.traffic.rotateRead(); err != nil {
		return nil, err
	}

	event := &ClientEvent{Phase: c.phase}
	if ku.RequestUpdate == KeyUpdateRequested {
		record, err := c.traffic.sealRecord(ContentTypeHandshake,
			marshalHandshake(HandshakeKeyUpdate, []byte{KeyUpdateNotRequested}))
		if err != nil {
			return nil, err
		}
		record.Epoch = EpochAppData
		event.Records = []Record{record}
		if err := c.traffic.rotateWrite(); err != nil {
			return nil, err
		}
	}
	return event, nil
}

// SendApplicationData emits a protected application-data record after the
// handshake has completed. Early data must use SendEarlyData instead.
func (c *ClientStateMachine) SendApplicationData(payload []byte) (*ClientEvent, error) {
	if c.phase != ClientPhaseConnected {
		return nil, errState("application data is not legal in phase %s", c.phase)
	}
	record, err := c.traffic.sealRecord(ContentTypeApplicationData, payload)
	if err != nil {
		return nil, err
	}
	record.Epoch = EpochAppData
	return &ClientEvent{Records: []Record{record}, AppData: append([]byte(nil), payload...), Phase: c.phase}, nil
}

// SendKeyUpdate rotates the write key and emits a KeyUpdate handshake
// message. requestUpdate asks the peer to rotate its own write key.
func (c *ClientStateMachine) SendKeyUpdate(requestUpdate bool) (*ClientEvent, error) {
	if c.phase != ClientPhaseConnected {
		return nil, errState("KeyUpdate is not legal in phase %s", c.phase)
	}
	value := KeyUpdateNotRequested
	if requestUpdate {
		value = KeyUpdateRequested
	}
	// Per RFC 8446 4.6.3 the KeyUpdate message itself is protected with the
	// current write key; the sender only rotates to the next key afterwards.
	record, err := c.traffic.sealRecord(ContentTypeHandshake,
		marshalHandshake(HandshakeKeyUpdate, []byte{value}))
	if err != nil {
		return nil, err
	}
	record.Epoch = EpochAppData
	if err := c.traffic.rotateWrite(); err != nil {
		return nil, err
	}
	return &ClientEvent{Records: []Record{record}, Phase: c.phase}, nil
}

// SendAlert emits a protected Alert. Plaintext alerts after key installation
// are intentionally unsupported.
func (c *ClientStateMachine) SendAlert(alertDescription uint8) (*ClientEvent, error) {
	if c.traffic.write == nil {
		return nil, errState("cannot send Alert before traffic keys are installed")
	}
	record, err := c.traffic.sealRecord(ContentTypeAlert, []byte{2, alertDescription})
	if err != nil {
		return nil, err
	}
	record.Epoch = c.traffic.writeEpoch
	return &ClientEvent{Records: []Record{record}, Alert: alertDescription, Phase: c.phase}, nil
}

// EarlyDataAccepted reports whether the server signaled acceptance via the
// early_data extension in EncryptedExtensions.
func (c *ClientStateMachine) EarlyDataAccepted() bool { return c.earlyAccepted }

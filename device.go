package netsec

import (
	"bytes"
	"encoding/gob"
	"sync"
)

// DeviceStore is the terminal's ordinary, rollbackable storage: a power
// loss may revert it to any previously committed snapshot.
type DeviceStore interface {
	LoadDeviceState() ([]byte, error)
	CommitDeviceState([]byte) error
}

// TrustedCounter is the terminal's trusted monotonic joinEpoch storage.
// It survives power loss and can never roll back.
type TrustedCounter interface {
	LoadEpoch() (uint64, error)
	CommitEpoch(v uint64) error
}

type pendingRequest struct {
	DevNonce uint16
	MIC      [4]byte
}

// deviceState is the rollbackable persistent terminal state.
type deviceState struct {
	NextDevNonce uint16
	Pending      *pendingRequest
	MaxJoinNonce uint32
	SeenSessions map[[16]byte]bool
	HasSession   bool
	SessionKey   KeyHandle
	SessionEpoch uint64
	SessionGen   uint64
}

func (d *deviceState) clone() *deviceState {
	out := *d
	out.SeenSessions = make(map[[16]byte]bool, len(d.SeenSessions))
	for k := range d.SeenSessions {
		out.SeenSessions[k] = true
	}
	if d.Pending != nil {
		p := *d.Pending
		out.Pending = &p
	}
	return &out
}

func encodeDeviceState(d *deviceState) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeDeviceState(b []byte) (*deviceState, error) {
	d := &deviceState{}
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(d); err != nil {
		return nil, err
	}
	if d.SeenSessions == nil {
		d.SeenSessions = make(map[[16]byte]bool)
	}
	return d, nil
}

// SessionInfo describes the currently committed session.
type SessionInfo struct {
	KeyHandle  KeyHandle
	JoinEpoch  uint64
	SessionGen uint64
}

// Device is the terminal test host.
type Device struct {
	mu      sync.Mutex
	info    *DeviceInfo
	ks      *KeyStore
	st      DeviceStore
	trusted TrustedCounter

	state *deviceState
}

// NewDevice loads rollbackable state from the store (empty on first boot).
func NewDevice(info *DeviceInfo, ks *KeyStore, st DeviceStore, trusted TrustedCounter) (*Device, error) {
	d := &Device{info: info, ks: ks, st: st, trusted: trusted}
	raw, err := st.LoadDeviceState()
	if err != nil {
		return nil, err
	}
	if raw == nil {
		d.state = &deviceState{SeenSessions: make(map[[16]byte]bool)}
	} else {
		d.state, err = decodeDeviceState(raw)
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

// BeginJoin returns the JoinRequest to transmit. If a request is already
// pending (e.g. its response was lost), the same request is returned so
// the server can answer with the original JoinAccept.
func (d *Device) BeginJoin() (*JoinRequest, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if p := d.state.Pending; p != nil {
		return &JoinRequest{
			JoinEUI:  d.info.JoinEUI,
			DevEUI:   d.info.DevEUI,
			DevNonce: p.DevNonce,
			MIC:      p.MIC,
		}, nil
	}

	req := &JoinRequest{
		JoinEUI:  d.info.JoinEUI,
		DevEUI:   d.info.DevEUI,
		DevNonce: d.state.NextDevNonce,
	}
	mic, err := d.info.MIC(req.Canonical())
	if err != nil {
		return nil, err
	}
	req.MIC = mic

	next := d.state.clone()
	next.Pending = &pendingRequest{DevNonce: req.DevNonce, MIC: mic}
	next.NextDevNonce++
	raw, err := encodeDeviceState(next)
	if err != nil {
		return nil, err
	}
	if err := d.st.CommitDeviceState(raw); err != nil {
		return nil, err
	}
	d.state = next
	return req, nil
}

// HandleJoinAccept verifies and commits a JoinAccept. The ciphertext is
// authenticated via the MIC callback before decryption. The terminal then
// checks DevNonce against the pending request, JoinNonce against the last
// accepted value and the sessionID against all seen sessions; only then
// does it atomically commit the session key handle and the trusted
// joinEpoch.
func (d *Device) HandleJoinAccept(acc *JoinAccept) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	p := d.state.Pending
	if p == nil {
		return ErrNoPendingJoin
	}

	tag, err := d.info.MIC(acc.Ciphertext)
	if err != nil {
		return err
	}
	if tag != acc.MIC {
		return ErrAcceptAuthFailed
	}
	plain, err := d.ks.Decrypt(d.info.RootKey, acc.Ciphertext)
	if err != nil {
		return err
	}
	body, err := parseJoinAcceptBody(plain)
	if err != nil {
		return err
	}
	if body.DevEUI != d.info.DevEUI {
		return ErrAcceptWrongDevice
	}
	if body.DevNonce != p.DevNonce {
		return ErrDevNonceMismatch
	}
	if body.JoinNonce <= d.state.MaxJoinNonce {
		return ErrStaleJoinNonce
	}
	if d.state.SeenSessions[body.SessionID] {
		return ErrSessionReplayed
	}

	epoch, err := d.trusted.LoadEpoch()
	if err != nil {
		return err
	}
	epoch++

	// Atomic commit of session key handle and joinEpoch: the trusted
	// epoch is committed first (it can never roll back, so a crash here
	// only burns an epoch), then the key is derived and the rollbackable
	// session record committed. Committing the epoch before deriving
	// guarantees a retried accept can never repeat derivation inputs.
	if err := d.trusted.CommitEpoch(epoch); err != nil {
		return err
	}

	key, err := d.ks.DeriveSessionKey(d.info.RootKey, DerivationInputs{
		Label:     "lw-session",
		JoinEUI:   d.info.JoinEUI,
		DevEUI:    d.info.DevEUI,
		JoinNonce: body.JoinNonce,
		DevNonce:  body.DevNonce,
		SessionID: body.SessionID,
		JoinEpoch: epoch,
	})
	if err != nil {
		return err
	}

	next := d.state.clone()
	next.Pending = nil
	next.MaxJoinNonce = body.JoinNonce
	next.SeenSessions[body.SessionID] = true
	next.HasSession = true
	next.SessionKey = key
	next.SessionEpoch = epoch
	next.SessionGen = body.SessionGen
	raw, err := encodeDeviceState(next)
	if err != nil {
		return err
	}
	if err := d.st.CommitDeviceState(raw); err != nil {
		return err
	}
	d.state = next
	return nil
}

// Session returns the currently committed session, if any.
func (d *Device) Session() (SessionInfo, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.state.HasSession {
		return SessionInfo{}, false
	}
	return SessionInfo{
		KeyHandle:  d.state.SessionKey,
		JoinEpoch:  d.state.SessionEpoch,
		SessionGen: d.state.SessionGen,
	}, true
}

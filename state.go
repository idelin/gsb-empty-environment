package netsec

import (
	"errors"
	"fmt"
	"net"
	"sync"
)

var (
	// ErrReplayCounter is returned when a Message 3 replay counter is not
	// strictly newer than the last accepted counter.
	ErrReplayCounter = errors.New("netsec: EAPOL-Key replay counter did not increase")

	// ErrReplay is an alias for ErrReplayCounter.
	ErrReplay = ErrReplayCounter

	// ErrMIC is an alias for ErrMICVerification.
	ErrMIC = ErrMICVerification

	// ErrInvalidMIC is an alias for ErrMICVerification.
	ErrInvalidMIC = ErrMICVerification

	// ErrNotMessage3 is returned for frames that are not four-way handshake
	// Message 3.
	ErrNotMessage3 = errors.New("netsec: EAPOL-Key frame is not a pairwise Message 3")

	// ErrConfiguration is returned when the state machine has no usable PMK,
	// nonce, addresses, or PTK derivation callback.
	ErrConfiguration = errors.New("netsec: incomplete EAPOL-Key state machine configuration")
)

// PTKDeriver derives a PTK for the supplied ANonce. It may be used instead of
// PMK/SNonce/address-based derivation, for example when the caller owns the
// key derivation implementation.
type PTKDeriver func(anonce []byte) (*PTK, error)

// PTKInstaller is called exactly once for each distinct ANonce after the
// Message 3 MIC has been verified and its Key Data has been unwrapped. It
// must install the pairwise key and any state that belongs to it.
type PTKInstaller func(anonce []byte, ptk *PTK) error

// KeyDataHandler receives decrypted Key Data after MIC verification. It is
// not called for duplicate Message 3 retransmissions.
type KeyDataHandler func(keyData []byte) error

// Config configures a wireless supplicant's EAPOL-Key Message 3 handling.
//
// PMK, SNonce, SupplicantAddr, and AuthenticatorAddr are used to derive the
// PTK from each Message 3 ANonce. Alternatively, DerivePTK can be supplied.
type Config struct {
	PMK               []byte
	SNonce            []byte
	SupplicantAddr    net.HardwareAddr
	AuthenticatorAddr net.HardwareAddr

	// AA and SPA are aliases for AuthenticatorAddr and SupplicantAddr.
	AA  net.HardwareAddr
	SPA net.HardwareAddr

	DerivePTK  PTKDeriver
	PTKDeriver PTKDeriver

	// PTKProvider is an alternative deriver that returns KCK || KEK || TK.
	PTKProvider func(anonce []byte) ([]byte, error)

	InstallPTK PTKInstaller
	Installer  PTKInstaller

	KeyDataHandler KeyDataHandler
	HandleKeyData  KeyDataHandler
}

// ClientConfig is an alias for Config.
type ClientConfig = Config

// SupplicantConfig is an alias for Config.
type SupplicantConfig = Config

// EAPOLKeyConfig is an alias for Config.
type EAPOLKeyConfig = Config

type installedKey struct {
	ptk *PTK
}

// Message3Result describes the outcome of handling one EAPOL-Key frame.
type Message3Result struct {
	// Frame is the parsed input frame.
	Frame *EAPOLKeyFrame
	// Response is Message 4, ready to send back to the authenticator.
	Response *EAPOLKeyFrame
	// Duplicate is true when the frame was a retransmission for the ANonce
	// whose PTK is already installed. In that case Installed is false and
	// KeyData is nil.
	Duplicate bool
	// Installed is true only for the first Message 3 for an ANonce.
	Installed bool
	// PTK is the transient key used for MIC verification.
	PTK *PTK
	// KeyData is the decrypted Key Data from a newly installed Message 3.
	KeyData []byte
	// ReplayCounter is the committed replay counter after handling.
	ReplayCounter uint64
}

// ResponseBytes returns the marshaled Message 4 response, or nil when there
// is no response.
func (r *Message3Result) ResponseBytes() []byte {
	if r == nil || r.Response == nil {
		return nil
	}
	return append([]byte(nil), r.Response.Raw...)
}

// EAPOLKeyStateMachine is a receive-path EAPOL-Key state machine for a WPA2
// supplicant.
//
// Security properties:
//   - The replay counter is committed only after the MIC verifies and only
//     when the value strictly increases.
//   - Encrypted Key Data is unwrapped only after the MIC verifies.
//   - A PTK is installed at most once per ANonce. A retransmitted Message 3
//     is acknowledged with Message 4 but does not reinstall keys, reset the
//     replay counter, or process Key Data again.
type EAPOLKeyStateMachine struct {
	mu sync.Mutex

	config Config

	haveReplay    bool
	replayCounter uint64

	installed     map[[32]byte]*installedKey
	currentANonce [32]byte
	currentPTK    *PTK
	installCount  int
	lastKeyData   []byte
	lastResult    *Message3Result
}

// NewEAPOLKeyStateMachine creates a state machine. The preferred form is a
// single Config or *Config argument. For compatibility, positional byte-slice
// arguments (PMK, SNonce, supplicant address, authenticator address) and
// net.HardwareAddr arguments are also accepted.
func NewEAPOLKeyStateMachine(args ...any) *EAPOLKeyStateMachine {
	s := &EAPOLKeyStateMachine{
		installed: make(map[[32]byte]*installedKey),
	}
	if len(args) == 1 {
		switch v := args[0].(type) {
		case Config:
			s.config = normalizeConfig(cloneConfig(v))
			return s
		case *Config:
			if v != nil {
				s.config = normalizeConfig(cloneConfig(*v))
			}
			return s
		}
	}

	var addresses []net.HardwareAddr
	for _, arg := range args {
		switch v := arg.(type) {
		case Config:
			s.config = normalizeConfig(cloneConfig(v))
		case *Config:
			if v != nil {
				s.config = normalizeConfig(cloneConfig(*v))
			}
		case []byte:
			switch {
			case len(v) == 6:
				addresses = append(addresses, append(net.HardwareAddr(nil), v...))
			case len(s.config.SNonce) == 0 && (len(v) == 16 || len(v) == 32 || len(v) == 64):
				if len(s.config.PMK) == 0 {
					s.config.PMK = append([]byte(nil), v...)
				} else {
					s.config.SNonce = append([]byte(nil), v...)
				}
			}
		case net.HardwareAddr:
			addresses = append(addresses, append(net.HardwareAddr(nil), v...))
		case PTKDeriver:
			s.config.DerivePTK = v
		case func([]byte) (*PTK, error):
			s.config.DerivePTK = PTKDeriver(v)
		case func([]byte) ([]byte, error):
			s.config.PTKProvider = v
		case PTKInstaller:
			s.config.InstallPTK = v
		case func([]byte, *PTK) error:
			s.config.InstallPTK = PTKInstaller(v)
		case KeyDataHandler:
			s.config.KeyDataHandler = v
		case func([]byte) error:
			s.config.KeyDataHandler = KeyDataHandler(v)
		case string:
			if addr, err := net.ParseMAC(v); err == nil {
				addresses = append(addresses, addr)
			}
		}
	}

	if len(s.config.SupplicantAddr) == 0 && len(addresses) > 0 {
		s.config.SupplicantAddr = addresses[0]
	}
	if len(s.config.AuthenticatorAddr) == 0 && len(addresses) > 1 {
		s.config.AuthenticatorAddr = addresses[1]
	}
	s.config = normalizeConfig(s.config)
	return s
}

// NewStateMachine is an alias for NewEAPOLKeyStateMachine.
func NewStateMachine(args ...any) *EAPOLKeyStateMachine {
	return NewEAPOLKeyStateMachine(args...)
}

// NewSupplicant is an alias for NewEAPOLKeyStateMachine.
func NewSupplicant(args ...any) *EAPOLKeyStateMachine {
	return NewEAPOLKeyStateMachine(args...)
}

// StateMachine is an alias for EAPOLKeyStateMachine.
type StateMachine = EAPOLKeyStateMachine

// Supplicant is an alias for EAPOLKeyStateMachine.
type Supplicant = EAPOLKeyStateMachine

func cloneConfig(in Config) Config {
	out := in
	out.PMK = append([]byte(nil), in.PMK...)
	out.SNonce = append([]byte(nil), in.SNonce...)
	out.SupplicantAddr = append(net.HardwareAddr(nil), in.SupplicantAddr...)
	out.AuthenticatorAddr = append(net.HardwareAddr(nil), in.AuthenticatorAddr...)
	out.AA = append(net.HardwareAddr(nil), in.AA...)
	out.SPA = append(net.HardwareAddr(nil), in.SPA...)
	return out
}

func normalizeConfig(in Config) Config {
	if len(in.SupplicantAddr) == 0 {
		in.SupplicantAddr = append(net.HardwareAddr(nil), in.SPA...)
	}
	if len(in.AuthenticatorAddr) == 0 {
		in.AuthenticatorAddr = append(net.HardwareAddr(nil), in.AA...)
	}
	if in.DerivePTK == nil {
		in.DerivePTK = in.PTKDeriver
	}
	if in.InstallPTK == nil {
		in.InstallPTK = in.Installer
	}
	if in.KeyDataHandler == nil {
		in.KeyDataHandler = in.HandleKeyData
	}
	return in
}

func clonePTK(in *PTK) *PTK {
	if in == nil {
		return nil
	}
	out := &PTK{
		KCK: append([]byte(nil), in.KCK...),
		KEK: append([]byte(nil), in.KEK...),
		TK:  append([]byte(nil), in.TK...),
		Key: append([]byte(nil), in.Key...),
	}
	if len(out.Key) == 0 && len(out.KCK)+len(out.KEK)+len(out.TK) > 0 {
		out.Key = append(append(append([]byte(nil), out.KCK...), out.KEK...), out.TK...)
	}
	return out
}

// ProcessMessage3 handles one received EAPOL-Key frame and returns the full
// result, including the Message 4 response.
func (s *EAPOLKeyStateMachine) ProcessMessage3(frame any) (*Message3Result, error) {
	f, err := decodeFrame(frame)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !f.IsMessage3() {
		return nil, ErrNotMessage3
	}
	if f.Version != EAPOLVersion1 && f.Version != EAPOLVersion2 {
		return nil, fmt.Errorf("%w: unsupported EAPOL version %d", ErrInvalidEAPOLKeyFrame, f.Version)
	}

	anonce := f.Nonce
	known, alreadyInstalled := s.installed[anonce]

	var ptk *PTK
	if alreadyInstalled {
		ptk = known.ptk
	} else {
		ptk, err = s.derivePTK(anonce[:])
		if err != nil {
			return nil, err
		}
	}

	// Reject obvious replays before doing cryptographic work. A duplicate
	// retransmission for an already installed ANonce carries the same
	// counter and is handled below. A new ANonce must not roll the global
	// replay counter back.
	if s.haveReplay {
		if f.ReplayCounter < s.replayCounter {
			return nil, fmt.Errorf("%w: received %d, current %d", ErrReplayCounter, f.ReplayCounter, s.replayCounter)
		}
		if f.ReplayCounter == s.replayCounter && !alreadyInstalled {
			return nil, fmt.Errorf("%w: new ANonce requires a higher counter (got %d)", ErrReplayCounter, f.ReplayCounter)
		}
	}

	// MIC verification happens before any key installation, replay-counter
	// update, or Key Data decryption.
	if err := VerifyEAPOLKeyMIC(f.Raw, ptk.KCK); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMICVerification, err)
	}

	if alreadyInstalled {
		// A retransmitted Message 3 is acknowledged but is otherwise inert:
		// no PTK installation, no Key Data decrypt, and no counter update
		// unless the authenticated counter genuinely increased.
		resp, err := buildMessage4(f, ptk)
		if err != nil {
			return nil, err
		}
		if !s.haveReplay || f.ReplayCounter > s.replayCounter {
			s.replayCounter = f.ReplayCounter
			s.haveReplay = true
		}
		result := &Message3Result{
			Frame:         f,
			Response:      resp,
			Duplicate:     true,
			Installed:     false,
			PTK:           ptk,
			ReplayCounter: s.replayCounter,
		}
		s.lastResult = cloneResult(result)
		return result, nil
	}

	// Only authenticated frames reach this point. Decrypting Key Data after
	// the MIC check prevents an attacker from steering plaintext parsing.
	keyData, err := DecryptKeyData(f, ptk.KEK)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyDataDecryption, err)
	}
	if s.config.KeyDataHandler != nil && len(keyData) > 0 {
		if err := s.config.KeyDataHandler(append([]byte(nil), keyData...)); err != nil {
			return nil, fmt.Errorf("netsec: Key Data handler failed: %w", err)
		}
	}
	if s.config.InstallPTK != nil {
		if err := s.config.InstallPTK(anonce[:], ptk); err != nil {
			return nil, fmt.Errorf("netsec: PTK installation failed: %w", err)
		}
	}

	// Commit all state only after MIC, Key Data processing, and key
	// installation have succeeded.
	s.installed[anonce] = &installedKey{ptk: ptk}
	s.currentANonce = anonce
	s.currentPTK = ptk
	s.replayCounter = f.ReplayCounter
	s.haveReplay = true
	s.installCount++
	s.lastKeyData = append([]byte(nil), keyData...)

	resp, err := buildMessage4(f, ptk)
	if err != nil {
		return nil, err
	}
	result := &Message3Result{
		Frame:         f,
		Response:      resp,
		Duplicate:     false,
		Installed:     true,
		PTK:           ptk,
		KeyData:       append([]byte(nil), keyData...),
		ReplayCounter: s.replayCounter,
	}
	s.lastResult = cloneResult(result)
	return result, nil
}

// HandleMessage3 returns the Message 4 response frame for a received
// EAPOL-Key frame.
func (s *EAPOLKeyStateMachine) HandleMessage3(frame any) (*EAPOLKeyFrame, error) {
	result, err := s.ProcessMessage3(frame)
	if err != nil {
		return nil, err
	}
	return result.Response, nil
}

// HandleEAPOLKeyFrame is an alias for HandleMessage3.
func (s *EAPOLKeyStateMachine) HandleEAPOLKeyFrame(frame any) (*EAPOLKeyFrame, error) {
	return s.HandleMessage3(frame)
}

// HandleKeyFrame is an alias for HandleMessage3.
func (s *EAPOLKeyStateMachine) HandleKeyFrame(frame any) (*EAPOLKeyFrame, error) {
	return s.HandleMessage3(frame)
}

// ProcessKeyFrame is an alias for HandleMessage3.
func (s *EAPOLKeyStateMachine) ProcessKeyFrame(frame any) (*EAPOLKeyFrame, error) {
	return s.HandleMessage3(frame)
}

// Receive is an alias for HandleMessage3.
func (s *EAPOLKeyStateMachine) Receive(frame any) (*EAPOLKeyFrame, error) {
	return s.HandleMessage3(frame)
}

// ReceiveMessage3 is an alias for HandleMessage3.
func (s *EAPOLKeyStateMachine) ReceiveMessage3(frame any) (*EAPOLKeyFrame, error) {
	return s.HandleMessage3(frame)
}

// HandleEAPOLKey returns the marshaled Message 4 response for a received
// EAPOL-Key frame.
func (s *EAPOLKeyStateMachine) HandleEAPOLKey(frame any) ([]byte, error) {
	result, err := s.ProcessMessage3(frame)
	if err != nil {
		return nil, err
	}
	return result.ResponseBytes(), nil
}

// Handle is an alias for HandleEAPOLKey.
func (s *EAPOLKeyStateMachine) Handle(frame any) ([]byte, error) {
	return s.HandleEAPOLKey(frame)
}

// Process is an alias for HandleEAPOLKey.
func (s *EAPOLKeyStateMachine) Process(frame any) ([]byte, error) {
	return s.HandleEAPOLKey(frame)
}

// ReceiveEAPOLKey is an alias for HandleEAPOLKey.
func (s *EAPOLKeyStateMachine) ReceiveEAPOLKey(frame any) ([]byte, error) {
	return s.HandleEAPOLKey(frame)
}

// ReplayCounter returns the last committed replay counter.
func (s *EAPOLKeyStateMachine) ReplayCounter() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replayCounter
}

// HasReplayCounter reports whether a replay counter has been committed.
func (s *EAPOLKeyStateMachine) HasReplayCounter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.haveReplay
}

// InstallCount returns the number of successful PTK installations.
func (s *EAPOLKeyStateMachine) InstallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installCount
}

// InstalledANonce returns a copy of the most recently installed ANonce.
func (s *EAPOLKeyStateMachine) InstalledANonce() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.installCount == 0 {
		return nil
	}
	return append([]byte(nil), s.currentANonce[:]...)
}

// InstalledPTK returns a copy of the most recently installed PTK.
func (s *EAPOLKeyStateMachine) InstalledPTK() *PTK {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clonePTK(s.currentPTK)
}

// LastKeyData returns a copy of the most recently decrypted Key Data.
func (s *EAPOLKeyStateMachine) LastKeyData() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.lastKeyData...)
}

// LastResult returns a copy of the most recent handling result.
func (s *EAPOLKeyStateMachine) LastResult() *Message3Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneResult(s.lastResult)
}

// Reset clears replay, installed-key, and Key Data state. The receive path
// should call this only when a new security association begins (for example
// after (re)association), never while handling a retransmitted Message 3.
func (s *EAPOLKeyStateMachine) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.haveReplay = false
	s.replayCounter = 0
	s.installed = make(map[[32]byte]*installedKey)
	s.currentANonce = [32]byte{}
	s.currentPTK = nil
	s.installCount = 0
	s.lastKeyData = nil
	s.lastResult = nil
}

func (s *EAPOLKeyStateMachine) derivePTK(anonce []byte) (*PTK, error) {
	if s.config.DerivePTK != nil {
		ptk, err := s.config.DerivePTK(append([]byte(nil), anonce...))
		if err != nil {
			return nil, err
		}
		if ptk == nil {
			return nil, fmt.Errorf("%w: PTK deriver returned nil", ErrPTKDerivation)
		}
		ptk = clonePTK(ptk)
		if len(ptk.KCK) < 16 || len(ptk.KEK) < 16 {
			return nil, fmt.Errorf("%w: PTK deriver returned a short PTK", ErrPTKDerivation)
		}
		return ptk, nil
	}
	if s.config.PTKProvider != nil {
		raw, err := s.config.PTKProvider(append([]byte(nil), anonce...))
		if err != nil {
			return nil, err
		}
		if len(raw) < 48 {
			return nil, fmt.Errorf("%w: PTK provider returned %d bytes", ErrPTKDerivation, len(raw))
		}
		return &PTK{
			KCK: append([]byte(nil), raw[0:16]...),
			KEK: append([]byte(nil), raw[16:32]...),
			TK:  append([]byte(nil), raw[32:48]...),
			Key: append([]byte(nil), raw[:48]...),
		}, nil
	}

	if len(s.config.PMK) < 16 {
		return nil, fmt.Errorf("%w: PMK is missing", ErrConfiguration)
	}
	if len(s.config.SNonce) != 32 {
		return nil, fmt.Errorf("%w: SNonce must be 32 bytes", ErrConfiguration)
	}
	if len(s.config.SupplicantAddr) != 6 || len(s.config.AuthenticatorAddr) != 6 {
		return nil, fmt.Errorf("%w: station and authenticator addresses are required", ErrConfiguration)
	}
	return DerivePTK(s.config.PMK, anonce, s.config.SNonce, s.config.AuthenticatorAddr, s.config.SupplicantAddr)
}

func buildMessage4(in *EAPOLKeyFrame, ptk *PTK) (*EAPOLKeyFrame, error) {
	if in == nil || ptk == nil || len(ptk.KCK) < 16 {
		return nil, fmt.Errorf("%w: missing Message 3 or PTK", ErrInvalidEAPOLKeyFrame)
	}
	resp := &EAPOLKeyFrame{
		Version:        in.Version,
		DescriptorType: in.DescriptorType,
		KeyInfo:        (in.KeyInfo & KeyInfoVersionMask) | KeyInfoKeyType | KeyInfoMIC | KeyInfoSecure,
		ReplayCounter:  in.ReplayCounter,
	}
	if resp.Version == 0 {
		resp.Version = EAPOLVersion2
	}
	raw, err := resp.Marshal()
	if err != nil {
		return nil, err
	}
	mic, err := ComputeEAPOLKeyMIC(raw, ptk.KCK)
	if err != nil {
		return nil, err
	}
	copy(resp.MIC[:], mic)
	raw, err = resp.Marshal()
	if err != nil {
		return nil, err
	}
	resp.Raw = raw
	return resp, nil
}

func decodeFrame(frame any) (*EAPOLKeyFrame, error) {
	switch v := frame.(type) {
	case *EAPOLKeyFrame:
		if v == nil {
			return nil, fmt.Errorf("%w: nil frame", ErrInvalidEAPOLKeyFrame)
		}
		if len(v.Raw) > 0 {
			return ParseEAPOLKey(v.Raw)
		}
		raw, err := v.Marshal()
		if err != nil {
			return nil, err
		}
		return ParseEAPOLKey(raw)
	case EAPOLKeyFrame:
		return decodeFrame(&v)
	case []byte:
		return ParseEAPOLKey(v)
	default:
		return nil, fmt.Errorf("%w: expected []byte or *EAPOLKeyFrame", ErrInvalidEAPOLKeyFrame)
	}
}

func cloneResult(in *Message3Result) *Message3Result {
	if in == nil {
		return nil
	}
	out := &Message3Result{
		Frame:         in.Frame,
		Response:      in.Response,
		Duplicate:     in.Duplicate,
		Installed:     in.Installed,
		PTK:           clonePTK(in.PTK),
		KeyData:       append([]byte(nil), in.KeyData...),
		ReplayCounter: in.ReplayCounter,
	}
	return out
}

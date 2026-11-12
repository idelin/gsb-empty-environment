package netsec

import (
	"fmt"
	"sync"
	"time"
)

// DefaultReplayWindow is the default interval during which an ephemeral
// public key is remembered.
const DefaultReplayWindow = 5 * time.Minute

type admissionConfig struct {
	replayWindow time.Duration
	now          func() time.Time
}

// Option configures an Admission.
type Option func(*admissionConfig) error

// WithReplayWindow sets the interval during which a successfully admitted
// ephemeral public key is rejected if seen again. Zero selects the default;
// negative values are rejected.
func WithReplayWindow(window time.Duration) Option {
	return func(config *admissionConfig) error {
		if window == 0 {
			config.replayWindow = DefaultReplayWindow
			return nil
		}
		if window < 0 {
			return fmt.Errorf("%w: %s", ErrInvalidReplayWindow, window)
		}
		config.replayWindow = window
		return nil
	}
}

// WithClock injects the clock used for replay-window decisions.
func WithClock(now func() time.Time) Option {
	return func(config *admissionConfig) error {
		if now == nil {
			return ErrNilClock
		}
		config.now = now
		return nil
	}
}

type replayKey struct {
	scheme ProtectionScheme
	epk    string
}

type replayReservation struct {
	key replayKey
	at  time.Time
}

// Admission enforces SUCI protection-scheme, key-generation, MAC-before-
// decryption, and replay policy. It is safe for concurrent use.
type Admission struct {
	keys         *KeyRing
	decryptor    Decryptor
	replayWindow time.Duration
	now          func() time.Time

	mu      sync.Mutex
	seen    map[replayKey]time.Time
	pending map[replayKey]struct{}
}

// NewAdmission constructs an Admission. The decryptor is the injection point
// for the home network's actual SUCI cryptographic implementation.
func NewAdmission(keys *KeyRing, decryptor Decryptor, opts ...Option) (*Admission, error) {
	if keys == nil {
		return nil, ErrNilKeyRing
	}
	if decryptor == nil {
		return nil, ErrNilDecryptor
	}

	config := admissionConfig{
		replayWindow: DefaultReplayWindow,
		now:          time.Now,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&config); err != nil {
			return nil, err
		}
	}

	return &Admission{
		keys:         keys,
		decryptor:    decryptor,
		replayWindow: config.replayWindow,
		now:          config.now,
		seen:         make(map[replayKey]time.Time),
		pending:      make(map[replayKey]struct{}),
	}, nil
}

// Admit validates an inbound SUCI request and de-conceals it. For Profile A
// and Profile B, MAC verification is completed before the decryptor is
// invoked. An ephemeral public key is committed to the replay cache only
// after a fully successful admission.
func (a *Admission) Admit(request Request) (Result, error) {
	if err := validateRoutingIndicator(request.RoutingIndicator); err != nil {
		return Result{}, err
	}

	switch request.ProtectionScheme {
	case SchemeNull:
		if !request.EmergencyRegistration {
			return Result{}, fmt.Errorf(
				"%w: scheme %s is only allowed for emergency registration",
				ErrNullSchemeNotEmergency, request.ProtectionScheme,
			)
		}
		if len(request.SchemeOutput) == 0 {
			return Result{}, ErrEmptyIdentity
		}
		return Result{
			Scheme:                 SchemeNull,
			RoutingIndicator:       request.RoutingIndicator,
			HomeNetworkPublicKeyID: request.HomeNetworkPublicKeyID,
			KeyGeneration:          KeyGenerationNone,
			SUPI:                   cloneBytes(request.SchemeOutput),
		}, nil

	case SchemeProfileA, SchemeProfileB:
		return a.admitEncrypted(request)

	default:
		return Result{}, fmt.Errorf(
			"%w: %d", ErrUnsupportedProtectionScheme, uint8(request.ProtectionScheme),
		)
	}
}

func (a *Admission) admitEncrypted(request Request) (Result, error) {
	key, generation, ok := a.keys.Lookup(request.HomeNetworkPublicKeyID)
	if !ok {
		return Result{}, fmt.Errorf(
			"%w: public key id %d",
			ErrUnknownHomeNetworkKey, request.HomeNetworkPublicKeyID,
		)
	}

	payload, err := parseEncryptedPayload(request.ProtectionScheme, cloneBytes(request.SchemeOutput))
	if err != nil {
		return Result{}, err
	}

	reservation, err := a.reserveReplay(request.ProtectionScheme, payload.EphemeralPublicKey)
	if err != nil {
		return Result{}, err
	}
	committed := false
	defer func() {
		if !committed {
			a.releaseReplay(reservation)
		}
	}()

	if err := a.decryptor.VerifyMAC(request.ProtectionScheme, key, payload); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrMACVerificationFailed, err)
	}

	supi, err := a.decryptor.Decrypt(request.ProtectionScheme, key, payload)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrDecryptionFailed, err)
	}
	if len(supi) == 0 {
		return Result{}, ErrEmptyIdentity
	}

	a.commitReplay(reservation)
	committed = true

	return Result{
		Scheme:                 request.ProtectionScheme,
		RoutingIndicator:       request.RoutingIndicator,
		HomeNetworkPublicKeyID: request.HomeNetworkPublicKeyID,
		KeyGeneration:          generation,
		SUPI:                   cloneBytes(supi),
	}, nil
}

func (a *Admission) reserveReplay(scheme ProtectionScheme, ephemeralPublicKey []byte) (*replayReservation, error) {
	key := replayKey{scheme: scheme, epk: string(ephemeralPublicKey)}
	now := a.now()

	a.mu.Lock()
	defer a.mu.Unlock()

	if seenAt, ok := a.seen[key]; ok && now.Sub(seenAt) < a.replayWindow {
		return nil, fmt.Errorf("%w: %s", ErrReplayDetected, scheme)
	}
	if _, ok := a.pending[key]; ok {
		return nil, fmt.Errorf("%w: %s", ErrReplayDetected, scheme)
	}
	a.pending[key] = struct{}{}
	return &replayReservation{key: key, at: now}, nil
}

func (a *Admission) commitReplay(reservation *replayReservation) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.pending, reservation.key)
	a.seen[reservation.key] = reservation.at
	for key, seenAt := range a.seen {
		if reservation.at.Sub(seenAt) >= a.replayWindow {
			delete(a.seen, key)
		}
	}
}

func (a *Admission) releaseReplay(reservation *replayReservation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, reservation.key)
}

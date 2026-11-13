package netsec

import (
	"crypto/subtle"
	"fmt"
	"hash"
)

// ExportResult is phase-tagged exported key material. Keeping the phase on
// the result prevents a value produced by one exporter from being silently
// re-interpreted as belonging to the other exporter.
type ExportResult struct {
	Phase   Phase
	Label   string
	Context []byte
	Key     []byte
}

// Verify reports whether key matches the result. The result's phase is part
// of the identity of the value only through its provenance; Verify performs a
// constant-time comparison of the exported bytes.
func (r ExportResult) Verify(key []byte) bool {
	if !r.Phase.Valid() || len(r.Key) != len(key) {
		return false
	}
	return subtle.ConstantTimeCompare(r.Key, key) == 1
}

// VerifyKeyingMaterial is the error-returning form of Verify.
func (r ExportResult) VerifyKeyingMaterial(key []byte) error {
	if !r.Verify(key) {
		return ErrVerificationFailed
	}
	return nil
}

// VerifyWithPhase verifies key only if the caller names the result's phase.
func (r ExportResult) VerifyWithPhase(phase Phase, key []byte) bool {
	if phase != r.Phase || !phase.Valid() {
		return false
	}
	return r.Verify(key)
}

type exporterCore struct {
	hash   func() hash.Hash
	secret []byte
}

func (c exporterCore) export(label string, context []byte, length int) ([]byte, error) {
	if len(c.secret) == 0 {
		return nil, ErrInvalidSecret
	}
	if c.hash == nil {
		return nil, fmt.Errorf("netsec: nil hash constructor")
	}

	secret, err := deriveSecretBytes(c.hash, c.secret, label, nil)
	if err != nil {
		return nil, err
	}
	return expandLabel(c.hash, secret, "exporter", hashBytes(c.hash, context), length)
}

// EarlyExporterMasterSecret is the early_exporter_master_secret from RFC 8446,
// Section 7.1. It is intentionally a distinct Go type from
// ExporterMasterSecret: values produced from the early secret and from the
// handshake master secret are not interchangeable.
type EarlyExporterMasterSecret struct {
	secret []byte
	hash   func() hash.Hash
	err    error
}

// ExporterMasterSecret is exporter_master_secret, the post-handshake exporter
// secret from RFC 8446, Section 7.1.
type ExporterMasterSecret struct {
	secret []byte
	hash   func() hash.Hash
	err    error
}

// Aliases that make the phase explicit at use sites.
type (
	PostHandshakeExporterMasterSecret = ExporterMasterSecret
	MainExporterMasterSecret          = ExporterMasterSecret
	EarlyExporterKey                  = EarlyExporterMasterSecret
	PostHandshakeExporterKey          = ExporterMasterSecret
	EarlyExporterSecret               = EarlyExporterMasterSecret
	PostHandshakeExporterSecret       = ExporterMasterSecret
	MainExporterSecret                = ExporterMasterSecret
	KeyExporter                       = Exporter
	TLSExporter                       = Exporter
)

// NewEarlyExporterMasterSecret wraps an already-derived phase-specific
// exporter master secret.
func NewEarlyExporterMasterSecret(args ...any) *EarlyExporterMasterSecret {
	e := newExporter(args, PhaseEarly, true)
	if e.err != nil {
		return &EarlyExporterMasterSecret{err: e.err}
	}
	return &EarlyExporterMasterSecret{secret: cloneBytes(e.secret), hash: chooseHash(e.hash)}
}

// NewPostHandshakeExporterMasterSecret wraps an already-derived
// exporter_master_secret.
func NewPostHandshakeExporterMasterSecret(args ...any) *ExporterMasterSecret {
	e := newExporter(args, PhasePostHandshake, true)
	if e.err != nil {
		return &ExporterMasterSecret{err: e.err}
	}
	return &ExporterMasterSecret{secret: cloneBytes(e.secret), hash: chooseHash(e.hash)}
}

// NewExporterMasterSecret is an alias for
// NewPostHandshakeExporterMasterSecret.
func NewExporterMasterSecret(args ...any) *ExporterMasterSecret {
	return NewPostHandshakeExporterMasterSecret(args...)
}

// Phase reports the phase of the early exporter secret.
func (s *EarlyExporterMasterSecret) Phase() Phase { return PhaseEarly }

// Phase reports the phase of the post-handshake exporter secret.
func (s *ExporterMasterSecret) Phase() Phase { return PhasePostHandshake }

// HashFunc returns the hash constructor used by the secret.
func (s *EarlyExporterMasterSecret) HashFunc() func() hash.Hash {
	if s == nil {
		return nil
	}
	return s.hash
}

// HashFunc returns the hash constructor used by the secret.
func (s *ExporterMasterSecret) HashFunc() func() hash.Hash {
	if s == nil {
		return nil
	}
	return s.hash
}

// Secret returns a copy of the exporter master secret.
func (s *EarlyExporterMasterSecret) Secret() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.secret...)
}

// Secret returns a copy of the exporter master secret.
func (s *ExporterMasterSecret) Secret() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.secret...)
}

// Export derives keying material according to RFC 8446, Section 7.5.
func (s *EarlyExporterMasterSecret) Export(args ...any) ([]byte, error) {
	return exportFromMaster(s, args)
}

// ExportKeyingMaterial is an alias for Export.
func (s *EarlyExporterMasterSecret) ExportKeyingMaterial(args ...any) ([]byte, error) {
	return s.Export(args...)
}

// ExportKey is an alias for Export.
func (s *EarlyExporterMasterSecret) ExportKey(args ...any) ([]byte, error) {
	return s.Export(args...)
}

// Exporter mirrors the tls13 secret API and returns nil on invalid input.
// Use Export when an error is preferred.
func (s *EarlyExporterMasterSecret) Exporter(args ...any) []byte {
	out, err := s.Export(args...)
	if err != nil {
		return nil
	}
	return out
}

// Verify re-exports with this early secret and compares in constant time.
func (s *EarlyExporterMasterSecret) Verify(args ...any) bool {
	return verifyFromMaster(s, args)
}

// VerifyKeyingMaterial is the error-returning form of Verify.
func (s *EarlyExporterMasterSecret) VerifyKeyingMaterial(args ...any) error {
	return verifyKeyingMaterialFromMaster(s, args)
}

// Export derives keying material according to RFC 8446, Section 7.5.
func (s *ExporterMasterSecret) Export(args ...any) ([]byte, error) {
	return exportFromMaster(s, args)
}

// ExportKeyingMaterial is an alias for Export.
func (s *ExporterMasterSecret) ExportKeyingMaterial(args ...any) ([]byte, error) {
	return s.Export(args...)
}

// ExportKey is an alias for Export.
func (s *ExporterMasterSecret) ExportKey(args ...any) ([]byte, error) {
	return s.Export(args...)
}

// Exporter mirrors the tls13 secret API and returns nil on invalid input.
// Use Export when an error is preferred.
func (s *ExporterMasterSecret) Exporter(args ...any) []byte {
	out, err := s.Export(args...)
	if err != nil {
		return nil
	}
	return out
}

// Verify re-exports with this post-handshake secret and compares in constant
// time.
func (s *ExporterMasterSecret) Verify(args ...any) bool {
	return verifyFromMaster(s, args)
}

// VerifyKeyingMaterial is the error-returning form of Verify.
func (s *ExporterMasterSecret) VerifyKeyingMaterial(args ...any) error {
	return verifyKeyingMaterialFromMaster(s, args)
}

func exportFromMaster(s any, args []any) ([]byte, error) {
	p, err := parseExportArgs(args)
	if err != nil {
		return nil, err
	}
	core, phase, err := coreForMaster(s)
	if err != nil {
		return nil, err
	}
	if p.phaseSet && p.phase != phase {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, phase)
	}
	return core.export(p.label, p.context, p.length)
}

func verifyFromMaster(s any, args []any) bool {
	return verifyKeyingMaterialFromMaster(s, args) == nil
}

func verifyKeyingMaterialFromMaster(s any, args []any) error {
	p, err := parseVerifyArgs(args)
	if err != nil {
		return err
	}
	core, phase, err := coreForMaster(s)
	if err != nil {
		return err
	}
	if p.phaseSet && p.phase != phase {
		return fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, phase)
	}
	got, err := core.export(p.label, p.context, p.length)
	if err != nil {
		return err
	}
	if len(got) != len(p.expected) || subtle.ConstantTimeCompare(got, p.expected) != 1 {
		return ErrVerificationFailed
	}
	return nil
}

func coreForMaster(s any) (exporterCore, Phase, error) {
	switch v := s.(type) {
	case *EarlyExporterMasterSecret:
		if v == nil {
			return exporterCore{}, PhaseInvalid, ErrInvalidSecret
		}
		if v.err != nil {
			return exporterCore{}, PhaseInvalid, v.err
		}
		return exporterCore{hash: v.hash, secret: v.secret}, PhaseEarly, nil
	case *ExporterMasterSecret:
		if v == nil {
			return exporterCore{}, PhaseInvalid, ErrInvalidSecret
		}
		if v.err != nil {
			return exporterCore{}, PhaseInvalid, v.err
		}
		return exporterCore{hash: v.hash, secret: v.secret}, PhasePostHandshake, nil
	default:
		return exporterCore{}, PhaseInvalid, ErrInvalidSecret
	}
}

// Exporter is a phase-bound exporter. The phase and the secret are set
// together at construction; Export never substitutes the other phase's
// secret.
type Exporter struct {
	phase       Phase
	hash        func() hash.Hash
	secret      []byte
	earlySecret []byte
	postSecret  []byte
	dual        bool
	err         error
}

// Phase reports the exporter's phase.
func (e *Exporter) Phase() Phase {
	if e == nil {
		return PhaseInvalid
	}
	return e.phase
}

// HashFunc returns the hash constructor used by the exporter.
func (e *Exporter) HashFunc() func() hash.Hash {
	if e == nil {
		return nil
	}
	return e.hash
}

// Secret returns a copy of the phase-specific exporter master secret.
func (e *Exporter) Secret() []byte {
	if e == nil {
		return nil
	}
	return append([]byte(nil), e.secret...)
}

// SecretForPhase returns a copy of the phase-specific exporter master secret.
func (e *Exporter) SecretForPhase(phase Phase) []byte {
	if e == nil {
		return nil
	}
	if e.dual {
		switch phase {
		case PhaseEarly:
			return append([]byte(nil), e.earlySecret...)
		case PhasePostHandshake:
			return append([]byte(nil), e.postSecret...)
		}
		return nil
	}
	if phase != e.phase {
		return nil
	}
	return append([]byte(nil), e.secret...)
}

// NewExporter builds a phase-bound exporter.
//
// The accepted inputs are a phase, a hash constructor, and either:
//   - one []byte: the phase-specific exporter master secret, or
//   - two []byte values: the base secret for the phase and a transcript.
//
// With two byte slices the phase-specific exporter master secret is derived
// with "e exp master" for PhaseEarly or "exp master" for
// PhasePostHandshake. A phase is mandatory for raw byte secrets.
func NewExporter(args ...any) *Exporter {
	return newExporter(args, PhaseInvalid, false)
}

// NewExporterForPhase builds an exporter with an explicitly named phase.
func NewExporterForPhase(phase Phase, args ...any) *Exporter {
	return newExporter(append([]any{phase}, args...), phase, false)
}

// NewPhaseExporter is an alias for NewExporterForPhase.
func NewPhaseExporter(phase Phase, args ...any) *Exporter {
	return NewExporterForPhase(phase, args...)
}

// NewKeyExporter is an alias for NewExporter.
func NewKeyExporter(args ...any) *Exporter {
	return NewExporter(args...)
}

// NewExporterFromSecret is an alias for NewExporter.
func NewExporterFromSecret(args ...any) *Exporter {
	return NewExporter(args...)
}

// NewEarlyExporterFromSecret is an alias for NewEarlyExporter.
func NewEarlyExporterFromSecret(args ...any) *Exporter {
	return NewEarlyExporter(args...)
}

// NewPostHandshakeExporterFromSecret is an alias for
// NewPostHandshakeExporter.
func NewPostHandshakeExporterFromSecret(args ...any) *Exporter {
	return NewPostHandshakeExporter(args...)
}

// NewExporterPair builds an exporter holding both phase-specific exporter
// master secrets. The first byte secret is the early secret and the second is
// the post-handshake secret. Each Export call must name the phase.
func NewExporterPair(args ...any) *Exporter {
	return NewExporter(args...)
}

// NewDualExporter is an alias for NewExporterPair.
func NewDualExporter(args ...any) *Exporter {
	return NewExporterPair(args...)
}

// NewExporterSet is an alias for NewExporterPair.
func NewExporterSet(args ...any) *Exporter {
	return NewExporterPair(args...)
}

// NewExporterE is the error-returning form of NewExporter.
func NewExporterE(args ...any) (*Exporter, error) {
	e := NewExporter(args...)
	if e.err != nil {
		return nil, e.err
	}
	return e, nil
}

// NewEarlyExporter builds an exporter in the early phase.
func NewEarlyExporter(args ...any) *Exporter {
	return newExporter(args, PhaseEarly, false)
}

// NewPostHandshakeExporter builds an exporter in the post-handshake phase.
func NewPostHandshakeExporter(args ...any) *Exporter {
	return newExporter(args, PhasePostHandshake, false)
}

// NewHandshakeExporter is an alias for NewPostHandshakeExporter.
func NewHandshakeExporter(args ...any) *Exporter {
	return NewPostHandshakeExporter(args...)
}

// NewMainExporter is an alias for NewPostHandshakeExporter.
func NewMainExporter(args ...any) *Exporter {
	return NewPostHandshakeExporter(args...)
}

// NewExporterFromMasterSecret builds an exporter from an already-derived,
// phase-specific exporter master secret. A phase is still required.
func NewExporterFromMasterSecret(args ...any) *Exporter {
	return newExporter(args, PhaseInvalid, true)
}

// NewExporterFromBaseSecret builds an exporter by deriving the phase-specific
// exporter master secret from a base secret and transcript.
func NewExporterFromBaseSecret(args ...any) *Exporter {
	return newExporterFromBase(args, PhaseInvalid)
}

// NewEarlyExporterFromBaseSecret derives early_exporter_master_secret from an
// early secret and transcript.
func NewEarlyExporterFromBaseSecret(args ...any) *Exporter {
	return newExporterFromBase(args, PhaseEarly)
}

// NewPostHandshakeExporterFromBaseSecret derives exporter_master_secret from
// the handshake master secret and transcript.
func NewPostHandshakeExporterFromBaseSecret(args ...any) *Exporter {
	return newExporterFromBase(args, PhasePostHandshake)
}

// NewEarlyExporterFromMasterSecret builds an early exporter from an already
// derived early_exporter_master_secret.
func NewEarlyExporterFromMasterSecret(args ...any) *Exporter {
	return newExporter(args, PhaseEarly, true)
}

// NewPostHandshakeExporterFromMasterSecret builds a post-handshake exporter
// from an already derived exporter_master_secret.
func NewPostHandshakeExporterFromMasterSecret(args ...any) *Exporter {
	return newExporter(args, PhasePostHandshake, true)
}

func newExporter(args []any, forced Phase, direct bool) *Exporter {
	e := &Exporter{hash: defaultHash()}
	var phase Phase
	var phaseSet bool
	var slots []sliceSlot
	var transcriptHash hash.Hash
	var typed *Exporter
	var baseTyped bool
	var typedEarly *EarlyExporterMasterSecret
	var typedPost *ExporterMasterSecret

	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			e.hash = h
			continue
		}
		switch v := arg.(type) {
		case *Exporter:
			if v != nil {
				typed = v
			}
		case *EarlyExporterMasterSecret:
			if v == nil {
				e.err = ErrInvalidSecret
				continue
			}
			typedEarly = v
			e.secret = cloneBytes(v.secret)
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhaseEarly, true
		case *ExporterMasterSecret:
			if v == nil {
				e.err = ErrInvalidSecret
				continue
			}
			typedPost = v
			e.secret = cloneBytes(v.secret)
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhasePostHandshake, true
		case *EarlySecret:
			if v == nil {
				e.err = ErrInvalidSecret
				continue
			}
			e.secret = cloneBytes(v.secret)
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhaseEarly, true
			baseTyped = true
		case *MasterSecret:
			if v == nil {
				e.err = ErrInvalidSecret
				continue
			}
			e.secret = cloneBytes(v.secret)
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhasePostHandshake, true
			baseTyped = true
		case Phase:
			if !v.Valid() {
				e.err = ErrInvalidPhase
				continue
			}
			phase, phaseSet = v, true
		case string:
			if ph, err := ParsePhase(v); err == nil {
				phase, phaseSet = ph, true
			}
		case []byte:
			slots = append(slots, sliceSlot{b: v})
		case nil:
			slots = append(slots, sliceSlot{isNil: true})
		case hash.Hash:
			transcriptHash = v
		default:
			if ph, ok := phaseFromValue(v); ok {
				phase, phaseSet = ph, true
			}
		}
	}

	if forced.Valid() {
		if phaseSet && phase != forced {
			e.err = fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, phase, forced)
			e.phase = forced
			return e
		}
		phase, phaseSet = forced, true
	}
	if typedEarly != nil && typedPost != nil && !forced.Valid() {
		e.dual = true
		e.earlySecret = cloneBytes(typedEarly.secret)
		e.postSecret = cloneBytes(typedPost.secret)
		e.hash = chooseHash(typedPost.hash)
		e.phase = PhaseInvalid
		e.secret = nil
		return e
	}
	if typed != nil {
		if typed.err != nil {
			e.err = typed.err
			return e
		}
		if phaseSet && typed.phase.Valid() && phase != typed.phase {
			e.err = fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, typed.phase, phase)
			return e
		}
		e.phase = typed.phase
		e.hash = typed.hash
		e.secret = cloneBytes(typed.secret)
		return e
	}
	if !forced.Valid() && !direct && !phaseSet && !baseTyped && transcriptHash == nil && len(slots) == 2 {
		e.dual = true
		e.earlySecret = cloneBytes(slots[0].b)
		e.postSecret = cloneBytes(slots[1].b)
		return e
	}
	if !phaseSet || !phase.Valid() {
		e.err = ErrInvalidPhase
		return e
	}
	e.phase = phase
	if baseTyped {
		var transcript []byte
		if len(slots) > 0 {
			transcript = slots[0].b
		}
		derived, err := deriveExporterSecretForPhase(e.phase, e.hash, e.secret, transcript, transcriptHash)
		if err != nil {
			e.err = err
			return e
		}
		e.secret = derived
		return e
	}

	if len(slots) == 0 {
		if len(e.secret) == 0 {
			e.err = ErrInvalidSecret
		}
		return e
	}
	e.secret = cloneBytes(slots[0].b)
	derive := !direct && (len(slots) >= 2 || transcriptHash != nil)
	if derive {
		var transcript []byte
		if len(slots) >= 2 {
			transcript = slots[1].b
		}
		derived, err := deriveExporterSecretForPhase(e.phase, e.hash, e.secret, transcript, transcriptHash)
		if err != nil {
			e.err = err
			return e
		}
		e.secret = derived
	}
	return e
}

func newExporterFromBase(args []any, forced Phase) *Exporter {
	e := &Exporter{hash: defaultHash()}
	var phase Phase
	var phaseSet bool
	var slices [][]byte
	var transcriptHash hash.Hash

	for _, arg := range args {
		if h, ok := hashFromValue(arg); ok {
			e.hash = h
			continue
		}
		switch v := arg.(type) {
		case Phase:
			if !v.Valid() {
				e.err = ErrInvalidPhase
				return e
			}
			phase, phaseSet = v, true
		case string:
			if ph, err := ParsePhase(v); err == nil {
				phase, phaseSet = ph, true
			}
		case []byte:
			slices = append(slices, v)
		case nil:
			slices = append(slices, nil)
		case hash.Hash:
			transcriptHash = v
		case *EarlyExporterMasterSecret:
			if v == nil {
				e.err = ErrInvalidSecret
				return e
			}
			slices = append(slices, cloneBytes(v.secret))
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhaseEarly, true
		case *ExporterMasterSecret:
			if v == nil {
				e.err = ErrInvalidSecret
				return e
			}
			slices = append(slices, cloneBytes(v.secret))
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhasePostHandshake, true
		case *EarlySecret:
			if v == nil {
				e.err = ErrInvalidSecret
				return e
			}
			slices = append(slices, cloneBytes(v.secret))
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhaseEarly, true
		case *MasterSecret:
			if v == nil {
				e.err = ErrInvalidSecret
				return e
			}
			slices = append(slices, cloneBytes(v.secret))
			e.hash = chooseHash(v.hash)
			phase, phaseSet = PhasePostHandshake, true
		default:
			if ph, ok := phaseFromValue(v); ok {
				phase, phaseSet = ph, true
			}
		}
	}

	if forced.Valid() {
		if phaseSet && phase != forced {
			e.err = fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, phase, forced)
			return e
		}
		phase, phaseSet = forced, true
	}
	if !phaseSet || !phase.Valid() {
		e.err = ErrInvalidPhase
		return e
	}
	e.phase = phase
	if len(slices) == 0 {
		e.err = ErrInvalidSecret
		return e
	}
	var transcript []byte
	if len(slices) >= 2 {
		transcript = slices[1]
	}
	derived, err := deriveExporterSecretForPhase(e.phase, e.hash, slices[0], transcript, transcriptHash)
	if err != nil {
		e.err = err
		return e
	}
	e.secret = derived
	return e
}

func deriveExporterSecretForPhase(phase Phase, h func() hash.Hash, base, transcript []byte, transcriptHash hash.Hash) ([]byte, error) {
	label := "exp master"
	if phase == PhaseEarly {
		label = "e exp master"
	}
	if transcriptHash != nil {
		return deriveSecretHash(h, base, label, transcriptHash)
	}
	return deriveSecretBytes(h, base, label, transcript)
}

// Export derives keying material using this exporter's phase and secret. A
// caller may repeat the phase as an argument; if it differs from the
// exporter's phase the operation fails rather than switching secrets.
func (e *Exporter) Export(args ...any) ([]byte, error) {
	if e == nil {
		return nil, ErrInvalidSecret
	}
	if e.err != nil {
		return nil, e.err
	}
	p, err := parseExportArgs(args)
	if err != nil {
		return nil, err
	}
	phase := e.phase
	if p.phaseSet {
		phase = p.phase
	}
	if !phase.Valid() {
		return nil, ErrInvalidPhase
	}
	secret := e.secret
	if e.dual {
		switch phase {
		case PhaseEarly:
			secret = e.earlySecret
		case PhasePostHandshake:
			secret = e.postSecret
		}
	} else if phase != e.phase {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, phase, e.phase)
	}
	return (exporterCore{hash: e.hash, secret: secret}).export(p.label, p.context, p.length)
}

// ExportKeyingMaterial is an alias for Export.
func (e *Exporter) ExportKeyingMaterial(args ...any) ([]byte, error) {
	return e.Export(args...)
}

// ExportKeyMaterial is an alias for Export.
func (e *Exporter) ExportKeyMaterial(args ...any) ([]byte, error) {
	return e.Export(args...)
}

// ExportKey is an alias for Export.
func (e *Exporter) ExportKey(args ...any) ([]byte, error) {
	return e.Export(args...)
}

// DeriveKeyingMaterial is an alias for Export.
func (e *Exporter) DeriveKeyingMaterial(args ...any) ([]byte, error) {
	return e.Export(args...)
}

// KeyingMaterial is an alias for Export.
func (e *Exporter) KeyingMaterial(args ...any) ([]byte, error) {
	return e.Export(args...)
}

// Exporter mirrors the tls13 secret API and returns nil on invalid input.
func (e *Exporter) Exporter(args ...any) []byte {
	out, err := e.Export(args...)
	if err != nil {
		return nil
	}
	return out
}

// Verify re-exports with this exporter and compares in constant time. An
// output produced with the other phase will not verify.
func (e *Exporter) Verify(args ...any) bool {
	return e.VerifyKeyingMaterial(args...) == nil
}

// VerifyKeyingMaterial re-exports and compares in constant time.
func (e *Exporter) VerifyKeyingMaterial(args ...any) error {
	if e == nil {
		return ErrInvalidSecret
	}
	if e.err != nil {
		return e.err
	}
	p, err := parseVerifyArgs(args)
	if err != nil {
		return err
	}
	phase := e.phase
	if p.phaseSet {
		phase = p.phase
	}
	if !phase.Valid() {
		return ErrInvalidPhase
	}
	secret := e.secret
	if e.dual {
		switch phase {
		case PhaseEarly:
			secret = e.earlySecret
		case PhasePostHandshake:
			secret = e.postSecret
		}
	} else if phase != e.phase {
		return fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, phase, e.phase)
	}
	got, err := (exporterCore{hash: e.hash, secret: secret}).export(p.label, p.context, p.length)
	if err != nil {
		return err
	}
	if len(got) != len(p.expected) || subtle.ConstantTimeCompare(got, p.expected) != 1 {
		return ErrVerificationFailed
	}
	return nil
}

// VerifyKey is an alias for Verify.
func (e *Exporter) VerifyKey(args ...any) bool {
	return e.Verify(args...)
}

// ExportKeyingMaterial derives exported key material for an explicitly named
// phase. See NewExporter for the accepted argument forms.
//
// The secret may be an Exporter, a phase-typed *EarlyExporterMasterSecret or
// *ExporterMasterSecret, or a raw []byte phase-specific exporter master
// secret. With three byte-slice slots (secret, transcript, context), the
// phase-specific master secret is derived from the base secret and
// transcript first.
func ExportKeyingMaterial(args ...any) ([]byte, error) {
	p, err := parseTopExportArgs(args)
	if err != nil {
		return nil, err
	}
	if p.exporter != nil {
		if p.exporter.dual {
			if !p.phaseSet {
				return nil, ErrInvalidPhase
			}
			return p.exporter.Export(p.phase, p.label, topContextForObject(p), p.length)
		}
		if p.phaseSet && p.exporter.phase != p.phase {
			return nil, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, p.exporter.phase)
		}
		return p.exporter.Export(p.label, topContextForObject(p), p.length)
	}
	if p.earlySecret != nil || p.postSecret != nil {
		if p.earlySecret != nil && p.phase != PhaseEarly {
			return nil, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, PhaseEarly)
		}
		if p.postSecret != nil && p.phase != PhasePostHandshake {
			return nil, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, PhasePostHandshake)
		}
		return exportFromMaster(p.earlySecretOrPost(), []any{p.label, topContextForObject(p), p.length})
	}
	if p.earlyBase != nil || p.masterBase != nil {
		base, h, basePhase, err := baseSecretForExport(p)
		if err != nil {
			return nil, err
		}
		if p.phase != basePhase {
			return nil, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, basePhase)
		}
		transcript, context := baseExportTranscriptAndContext(p)
		derived, err := deriveExporterSecretForPhase(basePhase, h, base, transcript, p.transcriptHash)
		if err != nil {
			return nil, err
		}
		return (exporterCore{hash: h, secret: derived}).export(p.label, context, p.length)
	}
	if !p.phaseSet || !p.phase.Valid() {
		return nil, ErrInvalidPhase
	}
	context := topExportContext(p)
	if topExportDerivesFromBase(p) {
		transcript, transcriptHash := topExportTranscript(p)
		derived, err := deriveExporterSecretForPhase(p.phase, p.hash, p.rawSecret, transcript, transcriptHash)
		if err != nil {
			return nil, err
		}
		return (exporterCore{hash: p.hash, secret: derived}).export(p.label, context, p.length)
	}
	return (exporterCore{hash: p.hash, secret: p.rawSecret}).export(p.label, context, p.length)
}

func (p parsedTopExport) earlySecretOrPost() any {
	if p.earlySecret != nil {
		return p.earlySecret
	}
	return p.postSecret
}

func baseSecretForExport(p parsedTopExport) ([]byte, func() hash.Hash, Phase, error) {
	if p.earlyBase != nil {
		if p.earlyBase.err != nil {
			return nil, nil, PhaseInvalid, p.earlyBase.err
		}
		return cloneBytes(p.earlyBase.secret), chooseHash(p.earlyBase.hash), PhaseEarly, nil
	}
	if p.masterBase != nil {
		if p.masterBase.err != nil {
			return nil, nil, PhaseInvalid, p.masterBase.err
		}
		return cloneBytes(p.masterBase.secret), chooseHash(p.masterBase.hash), PhasePostHandshake, nil
	}
	return nil, nil, PhaseInvalid, ErrInvalidSecret
}

func baseSecretForVerify(p parsedTopVerify) ([]byte, func() hash.Hash, Phase, error) {
	if p.earlyBase != nil {
		if p.earlyBase.err != nil {
			return nil, nil, PhaseInvalid, p.earlyBase.err
		}
		return cloneBytes(p.earlyBase.secret), chooseHash(p.earlyBase.hash), PhaseEarly, nil
	}
	if p.masterBase != nil {
		if p.masterBase.err != nil {
			return nil, nil, PhaseInvalid, p.masterBase.err
		}
		return cloneBytes(p.masterBase.secret), chooseHash(p.masterBase.hash), PhasePostHandshake, nil
	}
	return nil, nil, PhaseInvalid, ErrInvalidSecret
}

func baseExportTranscriptAndContext(p parsedTopExport) ([]byte, []byte) {
	if p.transcriptHash != nil {
		if len(p.slots) > 0 {
			return nil, p.slots[0].b
		}
		return nil, nil
	}
	switch len(p.slots) {
	case 0:
		return nil, nil
	case 1:
		return nil, p.slots[0].b
	default:
		return p.slots[0].b, p.slots[1].b
	}
}

func topContextForObject(p parsedTopExport) []byte {
	if len(p.slots) > 0 {
		return p.slots[0].b
	}
	return nil
}

// Export is an alias for ExportKeyingMaterial.
func Export(args ...any) ([]byte, error) {
	return ExportKeyingMaterial(args...)
}

// ExportKey is an alias for ExportKeyingMaterial.
func ExportKey(args ...any) ([]byte, error) {
	return ExportKeyingMaterial(args...)
}

// DeriveKeyingMaterial is an alias for ExportKeyingMaterial.
func DeriveKeyingMaterial(args ...any) ([]byte, error) {
	return ExportKeyingMaterial(args...)
}

// ExportKeyMaterial is an alias for ExportKeyingMaterial.
func ExportKeyMaterial(args ...any) ([]byte, error) {
	return ExportKeyingMaterial(args...)
}

// ExportForPhase exports with an explicitly named phase.
func ExportForPhase(phase Phase, args ...any) ([]byte, error) {
	if !phase.Valid() {
		return nil, ErrInvalidPhase
	}
	full := append([]any{phase}, args...)
	p, err := parseTopExportArgs(full)
	if err != nil {
		return nil, err
	}
	if p.phaseSet && p.phase != phase {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, phase)
	}
	return ExportKeyingMaterial(full...)
}

// ExportFromSecret treats the supplied byte secret as the phase-specific
// exporter master secret.
func ExportFromSecret(args ...any) ([]byte, error) {
	return ExportKeyingMaterial(args...)
}

// ExportWithTranscript treats the supplied byte secret as the base secret for
// the named phase and derives the phase-specific exporter master secret from
// the transcript before exporting.
func ExportWithTranscript(args ...any) ([]byte, error) {
	return ExportFromBaseSecret(args...)
}

// ExportFromBaseSecret derives the phase-specific exporter master secret from
// a base secret and transcript before exporting. Missing context arguments are
// filled with an empty context.
func ExportFromBaseSecret(args ...any) ([]byte, error) {
	slots := countSliceArgs(args)
	for slots < 3 {
		args = append(args, nil)
		slots++
	}
	return ExportKeyingMaterial(args...)
}

// ExportWithPhaseSecrets selects between the supplied early and
// post-handshake exporter master secrets according to the named phase.
func ExportWithPhaseSecrets(args ...any) ([]byte, error) {
	p, err := parseTopExportArgs(args)
	if err != nil {
		return nil, err
	}
	if !p.phaseSet || !p.phase.Valid() {
		return nil, ErrInvalidPhase
	}
	early, post, context, err := phaseSecretsFromExport(p)
	if err != nil {
		return nil, err
	}
	secret := early
	if p.phase == PhasePostHandshake {
		secret = post
	}
	return (exporterCore{hash: p.hash, secret: secret}).export(p.label, context, p.length)
}

// ExportPhaseSecrets is an alias for ExportWithPhaseSecrets.
func ExportPhaseSecrets(args ...any) ([]byte, error) {
	return ExportWithPhaseSecrets(args...)
}

// ExportKeyingMaterialWithSecrets is an alias for ExportWithPhaseSecrets.
func ExportKeyingMaterialWithSecrets(args ...any) ([]byte, error) {
	return ExportWithPhaseSecrets(args...)
}

// ExportWithPhaseSecretsTyped is the typed form of ExportWithPhaseSecrets.
func ExportWithPhaseSecretsTyped(phase Phase, earlySecret, postSecret []byte, label string, context []byte, length int) ([]byte, error) {
	var secret []byte
	switch phase {
	case PhaseEarly:
		secret = earlySecret
	case PhasePostHandshake:
		secret = postSecret
	default:
		return nil, ErrInvalidPhase
	}
	return (exporterCore{hash: defaultHash(), secret: secret}).export(label, context, length)
}

// VerifyWithPhaseSecrets checks expected using the selected phase secret.
func VerifyWithPhaseSecrets(args ...any) error {
	phase, early, post, label, context, expected, h, err := parsePhaseSecretsVerifyArgs(args)
	if err != nil {
		return err
	}
	var secret []byte
	switch phase {
	case PhaseEarly:
		secret = early
	case PhasePostHandshake:
		secret = post
	default:
		return ErrInvalidPhase
	}
	got, err := (exporterCore{hash: h, secret: secret}).export(label, context, len(expected))
	if err != nil {
		return err
	}
	if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
		return ErrVerificationFailed
	}
	return nil
}

func phaseSecretsFromExport(p parsedTopExport) (early, post, context []byte, err error) {
	if p.earlySecret != nil || p.postSecret != nil {
		if p.earlySecret != nil && p.postSecret != nil {
			return p.earlySecret.Secret(), p.postSecret.Secret(), topContextForObject(p), nil
		}
		return nil, nil, nil, ErrInvalidSecret
	}
	if len(p.slots) < 2 {
		return nil, nil, nil, ErrInvalidSecret
	}
	early, post = p.slots[0].b, p.slots[1].b
	if len(p.slots) >= 3 {
		context = p.slots[2].b
	}
	return early, post, context, nil
}

func countSliceArgs(args []any) int {
	n := 0
	for _, arg := range args {
		switch arg.(type) {
		case []byte, nil, hash.Hash:
			n++
		}
	}
	return n
}

// ExportEarly is an alias for ExportEarlyKeyingMaterial.
func ExportEarly(args ...any) ([]byte, error) {
	return ExportEarlyKeyingMaterial(args...)
}

// ExportEarlyKey is an alias for ExportEarlyKeyingMaterial.
func ExportEarlyKey(args ...any) ([]byte, error) {
	return ExportEarlyKeyingMaterial(args...)
}

// EarlyExportKey is an alias for ExportEarlyKeyingMaterial.
func EarlyExportKey(args ...any) ([]byte, error) {
	return ExportEarlyKeyingMaterial(args...)
}

// ExportPostHandshake is an alias for
// ExportPostHandshakeKeyingMaterial.
func ExportPostHandshake(args ...any) ([]byte, error) {
	return ExportPostHandshakeKeyingMaterial(args...)
}

// ExportPostHandshakeKey is an alias for
// ExportPostHandshakeKeyingMaterial.
func ExportPostHandshakeKey(args ...any) ([]byte, error) {
	return ExportPostHandshakeKeyingMaterial(args...)
}

// PostHandshakeExportKey is an alias for
// ExportPostHandshakeKeyingMaterial.
func PostHandshakeExportKey(args ...any) ([]byte, error) {
	return ExportPostHandshakeKeyingMaterial(args...)
}

// ExportKeyingMaterialEarly is an alias for ExportEarlyKeyingMaterial.
func ExportKeyingMaterialEarly(args ...any) ([]byte, error) {
	return ExportEarlyKeyingMaterial(args...)
}

// ExportKeyingMaterialPostHandshake is an alias for
// ExportPostHandshakeKeyingMaterial.
func ExportKeyingMaterialPostHandshake(args ...any) ([]byte, error) {
	return ExportPostHandshakeKeyingMaterial(args...)
}

// ExportEarlyKeyingMaterial exports from the early phase.
func ExportEarlyKeyingMaterial(args ...any) ([]byte, error) {
	return ExportKeyingMaterial(append([]any{PhaseEarly}, args...)...)
}

// ExportEarlyKeyMaterial is an alias for ExportEarlyKeyingMaterial.
func ExportEarlyKeyMaterial(args ...any) ([]byte, error) {
	return ExportEarlyKeyingMaterial(args...)
}

// ExportPostHandshakeKeyingMaterial exports from the post-handshake phase.
func ExportPostHandshakeKeyingMaterial(args ...any) ([]byte, error) {
	return ExportKeyingMaterial(append([]any{PhasePostHandshake}, args...)...)
}

// ExportPostHandshakeKeyMaterial is an alias for
// ExportPostHandshakeKeyingMaterial.
func ExportPostHandshakeKeyMaterial(args ...any) ([]byte, error) {
	return ExportPostHandshakeKeyingMaterial(args...)
}

// ExportMainKeyingMaterial is an alias for
// ExportPostHandshakeKeyingMaterial.
func ExportMainKeyingMaterial(args ...any) ([]byte, error) {
	return ExportPostHandshakeKeyingMaterial(args...)
}

// VerifyKeyingMaterial checks exported key material for the named phase. A
// value produced by the other phase will not verify, and a phase-typed secret
// used with the wrong phase is rejected before any comparison.
func VerifyKeyingMaterial(args ...any) error {
	p, err := parseTopVerifyArgs(args)
	if err != nil {
		return err
	}
	if p.exporter != nil {
		if p.exporter.dual {
			if !p.phaseSet {
				return ErrInvalidPhase
			}
			return p.exporter.VerifyKeyingMaterial(p.phase, p.label, p.context, p.length, p.expected)
		}
		if p.phaseSet && p.exporter.phase != p.phase {
			return fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, p.exporter.phase)
		}
		return p.exporter.VerifyKeyingMaterial(p.label, p.context, p.length, p.expected)
	}
	if p.earlySecret != nil || p.postSecret != nil {
		if p.earlySecret != nil && p.phase != PhaseEarly {
			return fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, PhaseEarly)
		}
		if p.postSecret != nil && p.phase != PhasePostHandshake {
			return fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, PhasePostHandshake)
		}
		return verifyKeyingMaterialFromMaster(p.earlySecretOrPost(),
			[]any{p.label, p.context, p.length, p.expected})
	}
	if p.earlyBase != nil || p.masterBase != nil {
		base, h, basePhase, err := baseSecretForVerify(p)
		if err != nil {
			return err
		}
		if p.phase != basePhase {
			return fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, basePhase)
		}
		derived, err := deriveExporterSecretForPhase(basePhase, h, base, p.transcript, p.transcriptHash)
		if err != nil {
			return err
		}
		got, err := (exporterCore{hash: h, secret: derived}).export(p.label, p.context, p.length)
		if err != nil {
			return err
		}
		if len(got) != len(p.expected) || subtle.ConstantTimeCompare(got, p.expected) != 1 {
			return ErrVerificationFailed
		}
		return nil
	}
	if !p.phaseSet || !p.phase.Valid() {
		return ErrInvalidPhase
	}
	secret := p.rawSecret
	if p.rawDerives {
		secret, err = deriveExporterSecretForPhase(p.phase, p.hash, p.rawSecret, p.transcript, p.transcriptHash)
		if err != nil {
			return err
		}
	}
	got, err := (exporterCore{hash: p.hash, secret: secret}).export(p.label, p.context, p.length)
	if err != nil {
		return err
	}
	if len(got) != len(p.expected) || subtle.ConstantTimeCompare(got, p.expected) != 1 {
		return ErrVerificationFailed
	}
	return nil
}

// CheckKeyingMaterial is the bool form of VerifyKeyingMaterial.
func CheckKeyingMaterial(args ...any) bool {
	return VerifyKeyingMaterial(args...) == nil
}

// VerifyExport is an alias for VerifyKeyingMaterial.
func VerifyExport(args ...any) error {
	return VerifyKeyingMaterial(args...)
}

// VerifyEarlyKeyingMaterial verifies output from the early phase.
func VerifyEarlyKeyingMaterial(args ...any) error {
	return VerifyKeyingMaterial(append([]any{PhaseEarly}, args...)...)
}

// VerifyPostHandshakeKeyingMaterial verifies output from the post-handshake
// phase.
func VerifyPostHandshakeKeyingMaterial(args ...any) error {
	return VerifyKeyingMaterial(append([]any{PhasePostHandshake}, args...)...)
}

// VerifyEarlyKeyMaterial is an alias for VerifyEarlyKeyingMaterial.
func VerifyEarlyKeyMaterial(args ...any) error {
	return VerifyEarlyKeyingMaterial(args...)
}

// VerifyPostHandshakeKeyMaterial is an alias for
// VerifyPostHandshakeKeyingMaterial.
func VerifyPostHandshakeKeyMaterial(args ...any) error {
	return VerifyPostHandshakeKeyingMaterial(args...)
}

// ExportPhaseKeyingMaterial returns phase-tagged output.
func ExportPhaseKeyingMaterial(phase Phase, args ...any) (ExportResult, error) {
	if !phase.Valid() {
		return ExportResult{}, ErrInvalidPhase
	}
	full := append([]any{phase}, args...)
	p, err := parseTopExportArgs(full)
	if err != nil {
		return ExportResult{}, err
	}
	if p.phaseSet && p.phase != phase {
		return ExportResult{}, fmt.Errorf("%w: got %s, want %s", ErrPhaseMismatch, p.phase, phase)
	}
	out, err := ExportKeyingMaterial(full...)
	if err != nil {
		return ExportResult{}, err
	}
	var context []byte
	switch {
	case p.exporter != nil || p.earlySecret != nil || p.postSecret != nil:
		context = topContextForObject(p)
	case p.earlyBase != nil || p.masterBase != nil:
		_, context = baseExportTranscriptAndContext(p)
	default:
		context = topExportContext(p)
	}
	return ExportResult{Phase: phase, Label: p.label, Context: cloneBytes(context), Key: out}, nil
}

func chooseHash(h func() hash.Hash) func() hash.Hash {
	if h != nil {
		return h
	}
	return defaultHash()
}

func cloneBytes(b []byte) []byte {
	return append([]byte(nil), b...)
}

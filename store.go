package netsec

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"
)

var (
	// ErrInvalidConfig indicates that the local algorithm policy is unusable.
	ErrInvalidConfig = errors.New("netsec: invalid capability configuration")
	// ErrUnknownAlgorithm indicates that an algorithm name is not known to the
	// package and is not a valid dotted OID.
	ErrUnknownAlgorithm = errors.New("netsec: unknown algorithm")
	// ErrInvalidAddress indicates that a From or signer address is malformed.
	ErrInvalidAddress = errors.New("netsec: invalid email address")
	// ErrInvalidCapabilities indicates malformed SMIMECapabilities ASN.1.
	ErrInvalidCapabilities = errors.New("netsec: invalid SMIMECapabilities")
	// ErrNoCompatibleAlgorithm indicates that a recorded capability set has no
	// intersection with the configured local preference list.
	ErrNoCompatibleAlgorithm = errors.New("netsec: no compatible encryption algorithm")
)

// Config defines the local encryption policy.
type Config struct {
	// AllowedAlgorithms is the caller-controlled whitelist. Capabilities not in
	// this list are discarded and are never used for selection.
	AllowedAlgorithms []Algorithm
	// LocalPreferences is ordered from most to least preferred. Entries outside
	// AllowedAlgorithms are ignored.
	LocalPreferences []Algorithm
	// Baseline is selected when no verified capability record exists for a
	// sender. It must be in AllowedAlgorithms so that the no-record path cannot
	// downgrade to a non-whitelisted algorithm.
	Baseline Algorithm
}

// SignedMessage contains the verified-signature facts and the raw
// SMIMECapabilities attribute value obtained while parsing an S/MIME message.
type SignedMessage struct {
	// From is the message From header value. Exactly one mailbox is accepted.
	From string
	// SignatureVerified must be set only after the CMS signature has been
	// cryptographically verified by the caller.
	SignatureVerified bool
	// SignerEmail is the mailbox from the verified signer certificate.
	SignerEmail string
	// SigningTime is the signed signing-time attribute. It is required for a
	// record so replay ordering is meaningful.
	SigningTime time.Time
	// SMIMECapabilities is the DER value of the
	// id-smime-aa-smimecapabilities signed attribute. A nil or empty value means
	// the attribute is absent when Capabilities is also nil.
	SMIMECapabilities []byte
	// Capabilities is an alternative to SMIMECapabilities for callers that have
	// already parsed the attribute with ExtractSMIMECapabilitiesFromSignedAttributes.
	// A nil value means the attribute is absent; a non-nil empty slice means the
	// attribute was present but contained no capabilities. It is an error to
	// provide both this field and SMIMECapabilities.
	Capabilities []Algorithm
}

// RecordStatus describes whether a message changed the capability store.
type RecordStatus string

const (
	RecordStatusRecorded              RecordStatus = "recorded"
	RecordStatusIgnoredUnverified     RecordStatus = "ignored-unverified"
	RecordStatusIgnoredFromMismatch   RecordStatus = "ignored-from-mismatch"
	RecordStatusIgnoredMissingTime    RecordStatus = "ignored-missing-signing-time"
	RecordStatusIgnoredStaleTime      RecordStatus = "ignored-stale-signing-time"
	RecordStatusIgnoredNoCapabilities RecordStatus = "ignored-no-capabilities"
	RecordStatusIgnoredNoAllowedAlg   RecordStatus = "ignored-no-allowed-algorithms"
)

// RecordResult reports the outcome of processing one signed message.
type RecordResult struct {
	Status RecordStatus
	// Algorithms is the filtered capability set that was stored. It is only
	// populated for RecordStatusRecorded.
	Algorithms []Algorithm
}

type capabilityRecord struct {
	signingTime  time.Time
	algorithms   []Algorithm
	algorithmSet map[Algorithm]struct{}
}

// CapabilityStore records SMIMECapabilities from verified, sender-matched
// signatures and selects a response encryption algorithm from local policy.
// It is safe for concurrent use.
type CapabilityStore struct {
	mu       sync.RWMutex
	allowed  map[Algorithm]struct{}
	prefs    []Algorithm
	baseline Algorithm
	lastSeen map[string]time.Time
	records  map[string]capabilityRecord
}

// NewCapabilityStore validates and normalizes local policy. A baseline outside
// the whitelist is rejected rather than treated as an insecure fallback.
func NewCapabilityStore(cfg Config) (*CapabilityStore, error) {
	allowed := make(map[Algorithm]struct{}, len(cfg.AllowedAlgorithms))
	for _, algorithm := range cfg.AllowedAlgorithms {
		normalized, err := normalizeAlgorithm(algorithm)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		allowed[normalized] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("%w: empty algorithm whitelist", ErrInvalidConfig)
	}

	baseline, err := normalizeAlgorithm(cfg.Baseline)
	if err != nil {
		return nil, fmt.Errorf("%w: baseline: %v", ErrInvalidConfig, err)
	}
	if _, ok := allowed[baseline]; !ok {
		return nil, fmt.Errorf("%w: baseline %q is not whitelisted", ErrInvalidConfig, baseline)
	}

	prefs := make([]Algorithm, 0, len(cfg.LocalPreferences))
	seen := make(map[Algorithm]struct{}, len(cfg.LocalPreferences))
	for _, algorithm := range cfg.LocalPreferences {
		normalized, err := normalizeAlgorithm(algorithm)
		if err != nil {
			return nil, fmt.Errorf("%w: local preference: %v", ErrInvalidConfig, err)
		}
		if _, ok := allowed[normalized]; !ok {
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		prefs = append(prefs, normalized)
	}

	return &CapabilityStore{
		allowed:  allowed,
		prefs:    prefs,
		baseline: baseline,
		lastSeen: make(map[string]time.Time),
		records:  make(map[string]capabilityRecord),
	}, nil
}

// Record applies the security gates and, if all pass, records the filtered
// capability list for the sender.
//
// A message only updates the store when:
//   - the caller marked its signature verified;
//   - the verified signer certificate mailbox matches the sole From mailbox;
//   - it carries a non-empty signing-time;
//   - its signing-time is strictly later than the newest verified,
//     sender-matched message seen from that sender;
//   - at least one capability survives the caller's whitelist.
//
// Malformed capabilities are reported as errors and do not change the store.
// A newer verified message with no capabilities, or only non-whitelisted
// capabilities, still advances the replay window while retaining the previous
// usable capability record.
func (s *CapabilityStore) Record(message SignedMessage) (RecordResult, error) {
	if !message.SignatureVerified {
		return RecordResult{Status: RecordStatusIgnoredUnverified}, nil
	}

	from, err := normalizeMailbox(message.From)
	if err != nil {
		return RecordResult{}, fmt.Errorf("%w: From: %v", ErrInvalidAddress, err)
	}
	signer, err := normalizeMailbox(message.SignerEmail)
	if err != nil {
		return RecordResult{}, fmt.Errorf("%w: signer certificate: %v", ErrInvalidAddress, err)
	}
	if from != signer {
		return RecordResult{Status: RecordStatusIgnoredFromMismatch}, nil
	}
	if message.SigningTime.IsZero() {
		return RecordResult{Status: RecordStatusIgnoredMissingTime}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if lastSeen, ok := s.lastSeen[from]; ok && !message.SigningTime.After(lastSeen) {
		return RecordResult{Status: RecordStatusIgnoredStaleTime}, nil
	}

	if len(message.SMIMECapabilities) == 0 && message.Capabilities == nil {
		s.lastSeen[from] = message.SigningTime
		return RecordResult{Status: RecordStatusIgnoredNoCapabilities}, nil
	}
	if len(message.SMIMECapabilities) != 0 && message.Capabilities != nil {
		return RecordResult{}, fmt.Errorf("%w: both raw and parsed capabilities supplied", ErrInvalidCapabilities)
	}

	var capabilities []Algorithm
	if len(message.SMIMECapabilities) != 0 {
		capabilities, err = ParseSMIMECapabilities(message.SMIMECapabilities)
		if err != nil {
			return RecordResult{}, err
		}
	} else {
		capabilities = make([]Algorithm, 0, len(message.Capabilities))
		for _, algorithm := range message.Capabilities {
			normalized, err := normalizeAlgorithm(algorithm)
			if err != nil {
				return RecordResult{}, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
			}
			capabilities = append(capabilities, normalized)
		}
	}
	filtered := s.filterAllowed(capabilities)
	if len(filtered) == 0 {
		s.lastSeen[from] = message.SigningTime
		return RecordResult{Status: RecordStatusIgnoredNoAllowedAlg}, nil
	}

	set := make(map[Algorithm]struct{}, len(filtered))
	for _, algorithm := range filtered {
		set[algorithm] = struct{}{}
	}
	s.records[from] = capabilityRecord{
		signingTime:  message.SigningTime,
		algorithms:   cloneAlgorithms(filtered),
		algorithmSet: set,
	}
	s.lastSeen[from] = message.SigningTime
	return RecordResult{
		Status:     RecordStatusRecorded,
		Algorithms: cloneAlgorithms(filtered),
	}, nil
}

// Select chooses a response encryption algorithm for a sender.
//
// If no verified record exists, it returns the configured baseline. If a
// record exists, it returns the first entry in the local preference list that
// is present in the recorded capability set. An empty intersection is an
// error; it never falls back to the baseline or to a non-whitelisted algorithm.
func (s *CapabilityStore) Select(sender string) (Algorithm, error) {
	mailbox, err := normalizeMailbox(sender)
	if err != nil {
		return "", fmt.Errorf("%w: sender: %v", ErrInvalidAddress, err)
	}

	s.mu.RLock()
	record, recorded := s.records[mailbox]
	prefs := s.prefs
	baseline := s.baseline
	s.mu.RUnlock()

	if !recorded {
		return baseline, nil
	}
	for _, preferred := range prefs {
		if _, ok := record.algorithmSet[preferred]; ok {
			return preferred, nil
		}
	}
	return "", fmt.Errorf("%w for %s", ErrNoCompatibleAlgorithm, mailbox)
}

// Capabilities returns a copy of the recorded, whitelist-filtered capability
// list for a sender. It is intended for diagnostics and policy tests.
func (s *CapabilityStore) Capabilities(sender string) ([]Algorithm, time.Time, bool) {
	mailbox, err := normalizeMailbox(sender)
	if err != nil {
		return nil, time.Time{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[mailbox]
	if !ok {
		return nil, time.Time{}, false
	}
	return cloneAlgorithms(record.algorithms), record.signingTime, true
}

func (s *CapabilityStore) filterAllowed(capabilities []Algorithm) []Algorithm {
	filtered := make([]Algorithm, 0, len(capabilities))
	for _, algorithm := range capabilities {
		if _, ok := s.allowed[algorithm]; ok {
			filtered = append(filtered, algorithm)
		}
	}
	return filtered
}

func normalizeMailbox(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty mailbox")
	}
	addresses, err := mail.ParseAddressList(raw)
	if err != nil {
		return "", err
	}
	if len(addresses) != 1 {
		return "", fmt.Errorf("expected exactly one mailbox, got %d", len(addresses))
	}
	address := strings.TrimSpace(addresses[0].Address)
	if address == "" {
		return "", fmt.Errorf("empty mailbox")
	}
	return strings.ToLower(address), nil
}

func cloneAlgorithms(algorithms []Algorithm) []Algorithm {
	if algorithms == nil {
		return nil
	}
	return append([]Algorithm(nil), algorithms...)
}

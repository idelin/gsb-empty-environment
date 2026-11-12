package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// ExpectedKeyGroup is a caller-registered key group. Each slice holds the
// fingerprints of the master-key entries that must be present in that group.
type ExpectedKeyGroup struct {
	PGP   []string `json:"pgp,omitempty"`
	KMS   []string `json:"kms,omitempty"`
	Age   []string `json:"age,omitempty"`
	Vault []string `json:"vault,omitempty"`
}

// Policy describes the only metadata layout that the loader will accept.
type Policy struct {
	KeyGroups       []ExpectedKeyGroup `json:"key_groups"`
	ShamirThreshold int                `json:"shamir_threshold"`
	MinVersion      string             `json:"min_version"`
}

// Validator holds a compiled policy and is safe for concurrent use.
type Validator struct {
	expectedThreshold int
	expectedGroups    []string
	minimumVersion    semanticVersion
}

// NewValidator validates and compiles a policy. A policy must contain at least
// one key group, a threshold between zero and the group count, and a valid
// minimum version.
func NewValidator(policy Policy) (*Validator, error) {
	if len(policy.KeyGroups) == 0 {
		return nil, fmt.Errorf("%w: no expected key groups", ErrInvalidPolicy)
	}
	if policy.ShamirThreshold < 0 || policy.ShamirThreshold > len(policy.KeyGroups) {
		return nil, fmt.Errorf(
			"%w: threshold %d outside [0,%d]",
			ErrInvalidPolicy,
			policy.ShamirThreshold,
			len(policy.KeyGroups),
		)
	}
	if strings.TrimSpace(policy.MinVersion) == "" {
		return nil, fmt.Errorf("%w: minimum version is empty", ErrInvalidPolicy)
	}
	minimumVersion, err := parseVersion(policy.MinVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: minimum version: %v", ErrInvalidPolicy, err)
	}

	expectedGroups := make([]string, 0, len(policy.KeyGroups))
	for i, group := range policy.KeyGroups {
		signature, err := expectedGroupSignature(group)
		if err != nil {
			return nil, fmt.Errorf("%w: key group %d: %v", ErrInvalidPolicy, i, err)
		}
		expectedGroups = append(expectedGroups, signature)
	}
	sort.Strings(expectedGroups)

	return &Validator{
		expectedThreshold: policy.ShamirThreshold,
		expectedGroups:    expectedGroups,
		minimumVersion:    minimumVersion,
	}, nil
}

// ValidateMetadata checks metadata from an encrypted file before decryption.
func ValidateMetadata(metadata *Metadata, expected []ExpectedKeyGroup, threshold int, minVersion string) error {
	validator, err := NewValidator(Policy{
		KeyGroups:       expected,
		ShamirThreshold: threshold,
		MinVersion:      minVersion,
	})
	if err != nil {
		return err
	}
	return validator.Validate(metadata)
}

// Validate checks key groups, entry fingerprints, threshold, MAC presence, and
// version. The MAC itself is checked after decryption by VerifyMAC.
func (v *Validator) Validate(metadata *Metadata) error {
	if metadata == nil {
		return fmt.Errorf("%w: nil metadata", ErrMalformedMetadata)
	}
	if err := v.validateKeyGroups(metadata.KeyGroups); err != nil {
		return err
	}
	if err := v.validateThreshold(metadata.ShamirThreshold, len(metadata.KeyGroups)); err != nil {
		return err
	}
	if strings.TrimSpace(metadata.MAC) == "" {
		return ErrMissingMAC
	}
	if err := v.validateVersion(metadata.Version); err != nil {
		return err
	}
	return nil
}

// ValidateJSON parses metadata and then applies Validate.
func (v *Validator) ValidateJSON(data []byte) error {
	metadata, err := ParseMetadata(data)
	if err != nil {
		return err
	}
	return v.Validate(metadata)
}

func (v *Validator) validateKeyGroups(groups []KeyGroup) error {
	if len(groups) != len(v.expectedGroups) {
		return fmt.Errorf(
			"%w: got %d key groups, want %d",
			ErrKeyGroupsMismatch,
			len(groups),
			len(v.expectedGroups),
		)
	}

	actualGroups := make([]string, 0, len(groups))
	for i, group := range groups {
		signature, err := actualGroupSignature(group)
		if err != nil {
			return fmt.Errorf("%w: key group %d: %v", ErrKeyGroupsMismatch, i, err)
		}
		actualGroups = append(actualGroups, signature)
	}
	sort.Strings(actualGroups)

	for i := range actualGroups {
		if actualGroups[i] != v.expectedGroups[i] {
			return ErrKeyGroupsMismatch
		}
	}
	return nil
}

func (v *Validator) validateThreshold(actual, groupCount int) error {
	if actual < 0 {
		return fmt.Errorf("%w: negative threshold %d", ErrThresholdViolation, actual)
	}
	if actual < v.expectedThreshold {
		return fmt.Errorf(
			"%w: got %d, want at least %d",
			ErrThresholdViolation,
			actual,
			v.expectedThreshold,
		)
	}
	if actual > groupCount {
		return fmt.Errorf(
			"%w: threshold %d exceeds %d key groups",
			ErrThresholdViolation,
			actual,
			groupCount,
		)
	}
	return nil
}

func (v *Validator) validateVersion(version string) error {
	parsed, err := parseVersion(version)
	if err != nil {
		return fmt.Errorf("%w: metadata version: %v", ErrInvalidVersion, err)
	}
	if parsed.compare(v.minimumVersion) < 0 {
		return fmt.Errorf(
			"%w: got %q, want at least %q",
			ErrVersionViolation,
			version,
			v.minimumVersion.raw,
		)
	}
	return nil
}

func actualGroupSignature(group KeyGroup) (string, error) {
	var fingerprints []string
	for _, entry := range group.PGP {
		fingerprint := normalizePGP(entry.Fingerprint)
		if fingerprint == "" {
			return "", fmt.Errorf("empty pgp fingerprint")
		}
		fingerprints = append(fingerprints, entryToken("pgp", fingerprint))
	}
	for _, entry := range group.KMS {
		fingerprint := normalizePlain(entry.ARN)
		if fingerprint == "" {
			return "", fmt.Errorf("empty kms arn")
		}
		fingerprints = append(fingerprints, entryToken("kms", fingerprint))
	}
	for _, entry := range group.Age {
		fingerprint := normalizeAge(entry.Recipient)
		if fingerprint == "" {
			return "", fmt.Errorf("empty age recipient")
		}
		fingerprints = append(fingerprints, entryToken("age", fingerprint))
	}
	for _, entry := range group.Vault {
		fingerprint := normalizePlain(entry.VaultAddress)
		if fingerprint == "" {
			return "", fmt.Errorf("empty vault address")
		}
		fingerprints = append(fingerprints, entryToken("vault", fingerprint))
	}
	if len(fingerprints) == 0 {
		return "", fmt.Errorf("key group has no master-key entries")
	}
	return joinFingerprints(fingerprints), nil
}

func expectedGroupSignature(group ExpectedKeyGroup) (string, error) {
	var fingerprints []string
	for _, fingerprint := range group.PGP {
		if normalizePGP(fingerprint) == "" {
			return "", fmt.Errorf("empty pgp fingerprint")
		}
		fingerprints = append(fingerprints, entryToken("pgp", normalizePGP(fingerprint)))
	}
	for _, fingerprint := range group.KMS {
		if normalizePlain(fingerprint) == "" {
			return "", fmt.Errorf("empty kms arn")
		}
		fingerprints = append(fingerprints, entryToken("kms", normalizePlain(fingerprint)))
	}
	for _, fingerprint := range group.Age {
		if normalizeAge(fingerprint) == "" {
			return "", fmt.Errorf("empty age recipient")
		}
		fingerprints = append(fingerprints, entryToken("age", normalizeAge(fingerprint)))
	}
	for _, fingerprint := range group.Vault {
		if normalizePlain(fingerprint) == "" {
			return "", fmt.Errorf("empty vault address")
		}
		fingerprints = append(fingerprints, entryToken("vault", normalizePlain(fingerprint)))
	}
	if len(fingerprints) == 0 {
		return "", fmt.Errorf("expected key group has no master-key entries")
	}
	return joinFingerprints(fingerprints), nil
}

func entryToken(kind, fingerprint string) string {
	return fmt.Sprintf("%d:%s:%d:%s", len(kind), kind, len(fingerprint), fingerprint)
}

func joinFingerprints(fingerprints []string) string {
	sort.Strings(fingerprints)
	var signature strings.Builder
	for _, fingerprint := range fingerprints {
		fmt.Fprintf(&signature, "%d:%s", len(fingerprint), fingerprint)
	}
	return signature.String()
}

func normalizePlain(value string) string {
	return strings.TrimSpace(value)
}

func normalizePGP(value string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", ":", "")
	return strings.ToUpper(replacer.Replace(strings.TrimSpace(value)))
}

func normalizeAge(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

package sopsmeta

import (
	"errors"
	"fmt"
	"sort"
)

// Sentinel errors returned by ValidateMetadata and VerifyMAC. Use
// errors.Is to match them.
var (
	ErrMissingMAC       = errors.New("sopsmeta: metadata carries no MAC")
	ErrVersionTooOld    = errors.New("sopsmeta: sops version below minimum")
	ErrThresholdTooLow  = errors.New("sopsmeta: shamir threshold below policy minimum")
	ErrThresholdTooHigh = errors.New("sopsmeta: shamir threshold exceeds key group count")
	ErrGroupCount       = errors.New("sopsmeta: key group count differs from policy")
	ErrGroupMismatch    = errors.New("sopsmeta: key group entries differ from policy")
	ErrMACMismatch      = errors.New("sopsmeta: recomputed MAC does not match metadata MAC")
	ErrMalformedMAC     = errors.New("sopsmeta: metadata MAC is not valid base64")
	ErrInvalidPolicy    = errors.New("sopsmeta: invalid policy")
	ErrInvalidVersion   = errors.New("sopsmeta: unparseable version string")
)

// ValidateMetadata checks the sops metadata of an encrypted file against the
// caller-registered policy. It must be called before any decryption attempt.
//
// The metadata key groups must contain exactly the expected groups with
// exactly the expected entries (extra or missing groups/entries are
// rejected; group order is irrelevant). The Shamir threshold must be at
// least the policy threshold and at most the number of key groups. The
// metadata must carry a MAC and a version not older than the policy minimum.
func ValidateMetadata(meta Metadata, policy Policy) error {
	if err := checkPolicy(policy); err != nil {
		return err
	}
	if meta.MAC == "" {
		return ErrMissingMAC
	}
	cmp, err := CompareVersions(meta.Version, policy.MinVersion)
	if err != nil {
		return err
	}
	if cmp < 0 {
		return fmt.Errorf("%w: file has %q, policy requires >= %q",
			ErrVersionTooOld, meta.Version, policy.MinVersion)
	}
	if meta.ShamirThreshold < policy.Threshold {
		return fmt.Errorf("%w: file has %d, policy requires >= %d",
			ErrThresholdTooLow, meta.ShamirThreshold, policy.Threshold)
	}
	if meta.ShamirThreshold > len(meta.KeyGroups) {
		return fmt.Errorf("%w: threshold %d with %d key groups",
			ErrThresholdTooHigh, meta.ShamirThreshold, len(meta.KeyGroups))
	}
	return matchGroups(meta.KeyGroups, policy.Groups)
}

func checkPolicy(policy Policy) error {
	if len(policy.Groups) == 0 {
		return fmt.Errorf("%w: no expected key groups registered", ErrInvalidPolicy)
	}
	if policy.Threshold < 1 {
		return fmt.Errorf("%w: threshold must be >= 1", ErrInvalidPolicy)
	}
	if policy.Threshold > len(policy.Groups) {
		return fmt.Errorf("%w: threshold %d exceeds %d registered groups",
			ErrInvalidPolicy, policy.Threshold, len(policy.Groups))
	}
	for i, g := range policy.Groups {
		if len(g.Entries) == 0 {
			return fmt.Errorf("%w: expected group %d has no entries", ErrInvalidPolicy, i)
		}
	}
	if _, err := CompareVersions(policy.MinVersion, policy.MinVersion); err != nil {
		return fmt.Errorf("%w: bad min version %q: %v", ErrInvalidPolicy, policy.MinVersion, err)
	}
	return nil
}

// matchGroups verifies that actual and expected describe the same set of
// key groups, ignoring group order. Both sides are canonicalized to sorted
// lists of sorted entry keys and compared as multisets.
func matchGroups(actual []KeyGroup, expected []ExpectedGroup) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%w: file has %d groups, policy expects %d",
			ErrGroupCount, len(actual), len(expected))
	}
	actualKeys := make([]string, len(actual))
	for i, g := range actual {
		actualKeys[i] = canonicalGroup(groupEntries(g))
	}
	expectedKeys := make([]string, len(expected))
	for i, g := range expected {
		keys := make([]string, len(g.Entries))
		for j, e := range g.Entries {
			keys[j] = string(e.Type) + ":" + e.Fingerprint
		}
		expectedKeys[i] = canonicalGroup(keys)
	}
	sort.Strings(actualKeys)
	sort.Strings(expectedKeys)
	for i := range actualKeys {
		if actualKeys[i] != expectedKeys[i] {
			return fmt.Errorf("%w: group %d of file (sorted order) does not match policy",
				ErrGroupMismatch, i)
		}
	}
	return nil
}

// groupEntries flattens a metadata key group into "type:fingerprint" keys.
func groupEntries(g KeyGroup) []string {
	var keys []string
	for _, k := range g.PGP {
		keys = append(keys, string(KeyTypePGP)+":"+k.Fingerprint)
	}
	for _, k := range g.KMS {
		keys = append(keys, string(KeyTypeKMS)+":"+k.ARN)
	}
	for _, k := range g.Age {
		keys = append(keys, string(KeyTypeAge)+":"+k.Recipient)
	}
	for _, k := range g.Vault {
		id := k.VaultAddress + "/" + k.EnginePath + "/" + k.KeyName
		keys = append(keys, string(KeyTypeVault)+":"+id)
	}
	return keys
}

// canonicalGroup sorts the entry keys of one group and joins them into a
// single comparable string.
func canonicalGroup(entries []string) string {
	sorted := make([]string, len(entries))
	copy(sorted, entries)
	sort.Strings(sorted)
	out := ""
	for _, e := range sorted {
		out += "[" + e + "]"
	}
	return out
}

// Package sopsmeta validates SOPS file metadata against a caller-registered
// policy before decryption, and verifies the file MAC after decryption.
//
// SOPS splits the data key across key groups of master keys; an attacker who
// can rewrite the metadata may add their own age/KMS recipient, lower the
// Shamir threshold, or drop a whole key group to gain decryption capability.
// ValidateMetadata rejects any metadata whose key groups, threshold, MAC
// presence, or version deviate from the registered policy.
package sopsmeta

// KeyType identifies the kind of a master key entry.
type KeyType string

const (
	KeyTypePGP   KeyType = "pgp"
	KeyTypeKMS   KeyType = "kms"
	KeyTypeAge   KeyType = "age"
	KeyTypeVault KeyType = "hc_vault"
)

// PGPKey is a PGP master key entry from the sops metadata.
type PGPKey struct {
	Fingerprint  string
	EncryptedKey string
	CreatedAt    string
}

// KMSKey is an AWS KMS master key entry from the sops metadata.
type KMSKey struct {
	ARN          string
	Role         string
	Context      map[string]string
	EncryptedKey string
	CreatedAt    string
}

// AgeKey is an age master key entry from the sops metadata.
type AgeKey struct {
	Recipient    string
	EncryptedKey string
}

// VaultKey is a HashiCorp Vault transit master key entry.
type VaultKey struct {
	VaultAddress string
	EnginePath   string
	KeyName      string
	EncryptedKey string
	CreatedAt    string
}

// KeyGroup is one SOPS key group: the set of master keys of which at least
// one must be available to recover this group's share of the data key.
type KeyGroup struct {
	PGP   []PGPKey
	KMS   []KMSKey
	Age   []AgeKey
	Vault []VaultKey
}

// Metadata is the sops section of an encrypted file.
type Metadata struct {
	KeyGroups       []KeyGroup
	ShamirThreshold int
	MAC             string
	LastModified    string
	Version         string
}

// ExpectedEntry identifies one expected master key by type and fingerprint.
// The fingerprint is the stable public identifier of the key: the PGP
// fingerprint, the KMS key ARN, the age recipient, or the Vault transit
// key identifier ("vaultAddress/enginePath/keyName").
type ExpectedEntry struct {
	Type        KeyType
	Fingerprint string
}

// ExpectedGroup is the expected set of master key entries of one key group.
type ExpectedGroup struct {
	Entries []ExpectedEntry
}

// Policy is the caller-registered expectation the metadata must satisfy.
type Policy struct {
	// Groups is the exact set of expected key groups. Metadata must contain
	// exactly these groups with exactly these entries; extra or missing
	// groups or entries are rejected. Group order is irrelevant.
	Groups []ExpectedGroup
	// Threshold is the minimum acceptable Shamir threshold.
	Threshold int
	// MinVersion is the minimum acceptable sops version (e.g. "3.7.3").
	MinVersion string
}

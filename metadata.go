package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Metadata is the security-relevant portion of a SOPS encrypted file.
type Metadata struct {
	KeyGroups       []KeyGroup `json:"key_groups"`
	ShamirThreshold int        `json:"shamir_threshold"`
	MAC             string     `json:"mac"`
	LastModified    string     `json:"lastmodified"`
	Version         string     `json:"version"`
}

// KeyGroup contains the master-key entries that wrap one Shamir share.
type KeyGroup struct {
	PGP   []PGPEntry   `json:"pgp,omitempty"`
	KMS   []KMSEntry   `json:"kms,omitempty"`
	Age   []AgeEntry   `json:"age,omitempty"`
	Vault []VaultEntry `json:"vault,omitempty"`
}

// PGPEntry is a PGP master-key entry.
type PGPEntry struct {
	Fingerprint string `json:"fp"`
	Encrypted   string `json:"enc,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// KMSEntry is an AWS KMS master-key entry.
type KMSEntry struct {
	ARN        string `json:"arn"`
	Encrypted  string `json:"enc,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	AWSProfile string `json:"aws_profile,omitempty"`
}

// AgeEntry is an age master-key entry.
type AgeEntry struct {
	Recipient string `json:"recipient"`
	Encrypted string `json:"enc,omitempty"`
}

// VaultEntry is a HashiCorp Vault master-key entry.
type VaultEntry struct {
	VaultAddress string `json:"vault_address"`
	Encrypted    string `json:"enc,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// Aliases kept for callers that prefer SOPS's "key" terminology.
type (
	PGPKey   = PGPEntry
	KMSKey   = KMSEntry
	AgeKey   = AgeEntry
	VaultKey = VaultEntry
)

// UnmarshalJSON rejects key-group members other than pgp, kms, age, and vault.
func (g *KeyGroup) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*g = KeyGroup{}
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedMetadata, err)
	}

	var next KeyGroup
	for name, raw := range fields {
		var err error
		switch name {
		case "pgp":
			err = json.Unmarshal(raw, &next.PGP)
		case "kms":
			err = json.Unmarshal(raw, &next.KMS)
		case "age":
			err = json.Unmarshal(raw, &next.Age)
		case "vault":
			err = json.Unmarshal(raw, &next.Vault)
		default:
			return fmt.Errorf("%w: %q", ErrUnsupportedEntry, name)
		}
		if err != nil {
			return fmt.Errorf("%w: decode %s: %v", ErrMalformedMetadata, name, err)
		}
	}
	*g = next
	return nil
}

// ParseMetadata parses either a metadata object or a complete encrypted file
// whose metadata is nested under the "sops" member.
func ParseMetadata(data []byte) (*Metadata, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMetadata, err)
	}

	metadataData := data
	if raw, ok := envelope["sops"]; ok {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil, fmt.Errorf("%w: decode sops: %v", ErrMalformedMetadata, err)
		}
		metadataData = raw
	}

	var metadata Metadata
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		return nil, fmt.Errorf("decode sops metadata: %w", err)
	}
	return &metadata, nil
}

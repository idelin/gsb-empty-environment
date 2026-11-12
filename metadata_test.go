package netsec

import (
	"errors"
	"strings"
	"testing"
)

func validFixture() (*Metadata, []ExpectedKeyGroup) {
	expected := []ExpectedKeyGroup{
		{
			PGP: []string{"aabbccddeeff00112233445566778899aabbccdd"},
			KMS: []string{"arn:aws:kms:us-east-1:111122223333:key/alpha"},
			Age: []string{"age1alpha"},
		},
		{
			Vault: []string{"https://vault.example/v1/transit/keys/service"},
			Age:   []string{"age1beta"},
		},
	}

	metadata := &Metadata{
		KeyGroups: []KeyGroup{
			{
				Age:   []AgeEntry{{Recipient: "age1beta", Encrypted: "wrapped-beta"}},
				Vault: []VaultEntry{{VaultAddress: "https://vault.example/v1/transit/keys/service"}},
			},
			{
				KMS: []KMSEntry{{ARN: "arn:aws:kms:us-east-1:111122223333:key/alpha"}},
				Age: []AgeEntry{{Recipient: "age1alpha", Encrypted: "wrapped-alpha"}},
				PGP: []PGPEntry{{Fingerprint: "AABB CCDD EEFF 0011 2233 4455 6677 8899 AABB CCDD"}},
			},
		},
		ShamirThreshold: 2,
		MAC:             "ENC[AES256_GCM,data:c2h5,iv:bm9wZQ==,tag:dGFn,type:str]",
		LastModified:    "2026-11-12T12:00:00Z",
		Version:         "3.9.2",
	}
	return metadata, expected
}

func TestValidateMetadataRequiredScenarios(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Metadata)
		wantErr error
	}{
		{
			name: "compliant file",
		},
		{
			name: "extra age entry",
			mutate: func(metadata *Metadata) {
				metadata.KeyGroups[0].Age = append(
					metadata.KeyGroups[0].Age,
					AgeEntry{Recipient: "age1attacker", Encrypted: "attacker-wrap"},
				)
			},
			wantErr: ErrKeyGroupsMismatch,
		},
		{
			name: "lowered threshold",
			mutate: func(metadata *Metadata) {
				metadata.ShamirThreshold = 1
			},
			wantErr: ErrThresholdViolation,
		},
		{
			name: "deleted group",
			mutate: func(metadata *Metadata) {
				metadata.KeyGroups = metadata.KeyGroups[:1]
			},
			wantErr: ErrKeyGroupsMismatch,
		},
		{
			name: "missing mac",
			mutate: func(metadata *Metadata) {
				metadata.MAC = "  "
			},
			wantErr: ErrMissingMAC,
		},
		{
			name: "below minimum version",
			mutate: func(metadata *Metadata) {
				metadata.Version = "3.8.1"
			},
			wantErr: ErrVersionViolation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata, expected := validFixture()
			if test.mutate != nil {
				test.mutate(metadata)
			}

			err := ValidateMetadata(metadata, expected, 2, "3.8.2")
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateMetadata() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateMetadata() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateMetadataRejectsEntryKindReplacement(t *testing.T) {
	metadata, expected := validFixture()
	metadata.KeyGroups[0].Age = nil
	metadata.KeyGroups[0].KMS = []KMSEntry{{
		ARN: "arn:aws:kms:us-east-1:111122223333:key/attacker",
	}}

	err := ValidateMetadata(metadata, expected, 2, "3.8.0")
	if !errors.Is(err, ErrKeyGroupsMismatch) {
		t.Fatalf("ValidateMetadata() error = %v, want %v", err, ErrKeyGroupsMismatch)
	}
}

func TestValidateMetadataRejectsDuplicateEntry(t *testing.T) {
	metadata, expected := validFixture()
	metadata.KeyGroups[1].Age = append(metadata.KeyGroups[1].Age, metadata.KeyGroups[1].Age[0])

	err := ValidateMetadata(metadata, expected, 2, "3.8.0")
	if !errors.Is(err, ErrKeyGroupsMismatch) {
		t.Fatalf("ValidateMetadata() error = %v, want %v", err, ErrKeyGroupsMismatch)
	}
}

func TestThresholdMayRiseButNotExceedGroupCount(t *testing.T) {
	metadata, expected := validFixture()
	metadata.ShamirThreshold = 2
	if err := ValidateMetadata(metadata, expected, 2, "3.8.0"); err != nil {
		t.Fatalf("equal threshold was rejected: %v", err)
	}

	metadata.ShamirThreshold = 3
	if !errors.Is(ValidateMetadata(metadata, expected, 2, "3.8.0"), ErrThresholdViolation) {
		t.Fatal("threshold above the group count was accepted")
	}
}

func TestVersionComparison(t *testing.T) {
	metadata, expected := validFixture()
	metadata.Version = "3.10.0"
	if err := ValidateMetadata(metadata, expected, 2, "3.8.0"); err != nil {
		t.Fatalf("newer version was rejected: %v", err)
	}

	metadata.Version = "3.8.0-rc.1"
	if !errors.Is(ValidateMetadata(metadata, expected, 2, "3.8.0"), ErrVersionViolation) {
		t.Fatal("prerelease version was treated as final")
	}

	metadata.Version = "not-a-version"
	if !errors.Is(ValidateMetadata(metadata, expected, 2, "3.8.0"), ErrInvalidVersion) {
		t.Fatal("invalid metadata version was accepted")
	}
}

func TestParseMetadataAndValidateJSON(t *testing.T) {
	raw := `{
		"data": "ENC[AES256_GCM,data:ZGF0YQ==,iv:aXY=,tag:dGFn,type:str]",
		"sops": {
			"key_groups": [
				{"age": [{"recipient": "age1one", "enc": "one"}]},
				{"kms": [{"arn": "arn:aws:kms:eu-west-1:111122223333:key/two"}]}
			],
			"shamir_threshold": 2,
			"mac": "ENC[AES256_GCM,data:bWFj,iv:aXY=,tag:dGFn,type:str]",
			"lastmodified": "2026-11-12T12:00:00Z",
			"version": "3.9.0"
		}
	}`
	policy := Policy{
		KeyGroups: []ExpectedKeyGroup{
			{Age: []string{"age1one"}},
			{KMS: []string{"arn:aws:kms:eu-west-1:111122223333:key/two"}},
		},
		ShamirThreshold: 2,
		MinVersion:      "3.8.0",
	}
	validator, err := NewValidator(policy)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	if err := validator.ValidateJSON([]byte(raw)); err != nil {
		t.Fatalf("ValidateJSON() error = %v", err)
	}

	metadata, err := ParseMetadata([]byte(raw))
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if metadata.LastModified != "2026-11-12T12:00:00Z" {
		t.Fatalf("lastmodified = %q", metadata.LastModified)
	}
}

func TestParseMetadataRejectsUnknownKeyGroupEntryType(t *testing.T) {
	raw := `{"key_groups":[{"gcp_kms":[]}],"mac":"present","version":"3.9.0"}`
	_, err := ParseMetadata([]byte(raw))
	if err == nil || !errors.Is(err, ErrUnsupportedEntry) || !strings.Contains(err.Error(), "gcp_kms") {
		t.Fatalf("ParseMetadata() error = %v, want unsupported gcp_kms", err)
	}
}

func TestInvalidPolicy(t *testing.T) {
	if _, err := NewValidator(Policy{
		KeyGroups:       []ExpectedKeyGroup{{Age: []string{"age1one"}}},
		ShamirThreshold: 2,
		MinVersion:      "3.8.0",
	}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("threshold above expected groups: error = %v", err)
	}
	if _, err := NewValidator(Policy{
		KeyGroups:       []ExpectedKeyGroup{{}},
		ShamirThreshold: 1,
		MinVersion:      "3.8.0",
	}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("empty expected group: error = %v", err)
	}
}

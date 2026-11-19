package sopsmeta

import (
	"errors"
	"testing"
)

var testDataKey = []byte("0123456789abcdef0123456789abcdef")

func testPolicy() Policy {
	return Policy{
		Groups: []ExpectedGroup{
			{Entries: []ExpectedEntry{
				{Type: KeyTypePGP, Fingerprint: "AABBCCDDEEFF00112233445566778899AABBCCDD"},
				{Type: KeyTypeKMS, Fingerprint: "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"},
			}},
			{Entries: []ExpectedEntry{
				{Type: KeyTypeAge, Fingerprint: "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqp8flkm"},
			}},
			{Entries: []ExpectedEntry{
				{Type: KeyTypeVault, Fingerprint: "https://vault.example.com:8200/sops/keys/main"},
			}},
		},
		Threshold:  2,
		MinVersion: "3.7.0",
	}
}

func compliantMetadata() Metadata {
	return Metadata{
		KeyGroups: []KeyGroup{
			{
				PGP: []PGPKey{{Fingerprint: "AABBCCDDEEFF00112233445566778899AABBCCDD", EncryptedKey: "enc1"}},
				KMS: []KMSKey{{ARN: "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555", EncryptedKey: "enc2"}},
			},
			{
				Age: []AgeKey{{Recipient: "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqp8flkm", EncryptedKey: "enc3"}},
			},
			{
				Vault: []VaultKey{{VaultAddress: "https://vault.example.com:8200", EnginePath: "sops", KeyName: "keys/main", EncryptedKey: "enc4"}},
			},
		},
		ShamirThreshold: 2,
		LastModified:    "2026-11-19T00:00:00Z",
		Version:         "3.9.0",
	}
}

func testData() map[string]any {
	return map[string]any{
		"db_password": "ENC[AES256_GCM,data:secret]",
		"replicas":    3,
		"nested": map[string]any{
			"enabled": true,
			"names":   []any{"a", "b"},
		},
	}
}

func withMAC(meta Metadata, data map[string]any) Metadata {
	meta.MAC = ComputeMAC(data, meta.LastModified, testDataKey)
	return meta
}

func TestValidateMetadataCompliant(t *testing.T) {
	meta := withMAC(compliantMetadata(), testData())
	if err := ValidateMetadata(meta, testPolicy()); err != nil {
		t.Fatalf("compliant metadata rejected: %v", err)
	}
}

func TestValidateMetadataGroupOrderIrrelevant(t *testing.T) {
	meta := compliantMetadata()
	meta.KeyGroups[0], meta.KeyGroups[2] = meta.KeyGroups[2], meta.KeyGroups[0]
	meta = withMAC(meta, testData())
	if err := ValidateMetadata(meta, testPolicy()); err != nil {
		t.Fatalf("reordered groups rejected: %v", err)
	}
}

func TestValidateMetadataExtraAgeEntry(t *testing.T) {
	meta := compliantMetadata()
	meta.KeyGroups[1].Age = append(meta.KeyGroups[1].Age, AgeKey{
		Recipient:    "age1attackercontrolledrecipient0000000000000000000000000000000",
		EncryptedKey: "evil",
	})
	meta = withMAC(meta, testData())
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrGroupMismatch) {
		t.Fatalf("extra age entry: got %v, want ErrGroupMismatch", err)
	}
}

func TestValidateMetadataMissingEntry(t *testing.T) {
	meta := compliantMetadata()
	meta.KeyGroups[0].KMS = nil
	meta = withMAC(meta, testData())
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrGroupMismatch) {
		t.Fatalf("missing KMS entry: got %v, want ErrGroupMismatch", err)
	}
}

func TestValidateMetadataLoweredThreshold(t *testing.T) {
	meta := compliantMetadata()
	meta.ShamirThreshold = 1
	meta = withMAC(meta, testData())
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrThresholdTooLow) {
		t.Fatalf("lowered threshold: got %v, want ErrThresholdTooLow", err)
	}
}

func TestValidateMetadataThresholdAboveGroupCount(t *testing.T) {
	meta := compliantMetadata()
	meta.ShamirThreshold = 4
	meta = withMAC(meta, testData())
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrThresholdTooHigh) {
		t.Fatalf("threshold above group count: got %v, want ErrThresholdTooHigh", err)
	}
}

func TestValidateMetadataDeletedGroup(t *testing.T) {
	meta := compliantMetadata()
	meta.KeyGroups = meta.KeyGroups[:2]
	meta = withMAC(meta, testData())
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrGroupCount) {
		t.Fatalf("deleted group: got %v, want ErrGroupCount", err)
	}
}

func TestValidateMetadataExtraGroup(t *testing.T) {
	meta := compliantMetadata()
	meta.KeyGroups = append(meta.KeyGroups, KeyGroup{
		Age: []AgeKey{{Recipient: "age1attacker000000000000000000000000000000000000000000000000"}},
	})
	meta = withMAC(meta, testData())
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrGroupCount) {
		t.Fatalf("extra group: got %v, want ErrGroupCount", err)
	}
}

func TestValidateMetadataMissingMAC(t *testing.T) {
	meta := compliantMetadata()
	meta.MAC = ""
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrMissingMAC) {
		t.Fatalf("missing MAC: got %v, want ErrMissingMAC", err)
	}
}

func TestValidateMetadataVersionTooOld(t *testing.T) {
	meta := compliantMetadata()
	meta.Version = "3.6.1"
	meta = withMAC(meta, testData())
	err := ValidateMetadata(meta, testPolicy())
	if !errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("old version: got %v, want ErrVersionTooOld", err)
	}
}

func TestVerifyMACCompliant(t *testing.T) {
	data := testData()
	meta := withMAC(compliantMetadata(), data)
	if err := VerifyMAC(meta, data, testDataKey); err != nil {
		t.Fatalf("valid MAC rejected: %v", err)
	}
}

func TestVerifyMACMissing(t *testing.T) {
	meta := compliantMetadata()
	if err := VerifyMAC(meta, testData(), testDataKey); !errors.Is(err, ErrMissingMAC) {
		t.Fatalf("missing MAC: got %v, want ErrMissingMAC", err)
	}
}

func TestVerifyMACTamperedData(t *testing.T) {
	data := testData()
	meta := withMAC(compliantMetadata(), data)
	data["db_password"] = "ENC[AES256_GCM,data:altered]"
	if err := VerifyMAC(meta, data, testDataKey); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("tampered data: got %v, want ErrMACMismatch", err)
	}
}

func TestVerifyMACTamperedLastModified(t *testing.T) {
	data := testData()
	meta := withMAC(compliantMetadata(), data)
	meta.LastModified = "2020-01-01T00:00:00Z"
	if err := VerifyMAC(meta, data, testDataKey); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("tampered lastmodified: got %v, want ErrMACMismatch", err)
	}
}

func TestVerifyMACWrongDataKey(t *testing.T) {
	data := testData()
	meta := withMAC(compliantMetadata(), data)
	wrongKey := []byte("ffffffffffffffffffffffffffffffff")
	if err := VerifyMAC(meta, data, wrongKey); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("wrong data key: got %v, want ErrMACMismatch", err)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"3.9.0", "3.7.0", 1},
		{"3.7.0", "3.7.0", 0},
		{"3.6.1", "3.7.0", -1},
		{"3.10.0", "3.9.0", 1},
		{"v3.8.0", "3.8.0", 0},
		{"3.9", "3.9.0", 0},
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if _, err := CompareVersions("not-a-version", "3.7.0"); !errors.Is(err, ErrInvalidVersion) {
		t.Errorf("bad version: got %v, want ErrInvalidVersion", err)
	}
}

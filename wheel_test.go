package netsec

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testWheelName    = "demo_pkg-1.0-py3-none-any.whl"
	testDistInfo     = "demo.pkg-1.0.dist-info"
	testMetadataName = "Demo_Pkg"
	testVersion      = "1.0"
)

type testEntry struct {
	name string
	body []byte
}

func TestValidateWheelAcceptsValidWheel(t *testing.T) {
	path := writeWheelFile(t, testWheelName, buildWheel(t, validEntries()))
	if err := ValidateWheelFile(path); err != nil {
		t.Fatalf("ValidateWheelFile: %v", err)
	}
	if err := ValidateWheel(path); err != nil {
		t.Fatalf("ValidateWheel(path): %v", err)
	}
}

func TestValidateWheelBytes(t *testing.T) {
	data := buildWheel(t, validEntries())
	if err := ValidateWheel(data, testWheelName); err != nil {
		t.Fatalf("ValidateWheel(bytes): %v", err)
	}
	if err := ValidateWheelBytes(data, testWheelName); err != nil {
		t.Fatalf("ValidateWheelBytes: %v", err)
	}
}

func TestValidateWheelAcceptsNormalizedNameVariants(t *testing.T) {
	entries := validEntriesWithDistInfo("Demo__Pkg-1.0.dist-info")
	data := buildWheel(t, entries)
	if err := ValidateWheel(data, "Demo..Pkg-1.0-py3-none-any.whl"); err != nil {
		t.Fatalf("normalized names should match: %v", err)
	}
}

func TestValidateWheelAcceptsBuildTag(t *testing.T) {
	data := buildWheel(t, validEntries())
	if err := ValidateWheel(data, "demo_pkg-1.0-7-py3-none-any.whl"); err != nil {
		t.Fatalf("build tag should be accepted: %v", err)
	}
}

func TestValidateWheelRejectsDuplicateEntry(t *testing.T) {
	entries := validEntries()
	entries = append(entries, testEntry{name: "demo_pkg/__init__.py", body: []byte("duplicate")})
	err := ValidateWheel(buildWheel(t, entries), testWheelName)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate-entry error, got %v", err)
	}
}

func TestValidateWheelRejectsTraversalEntry(t *testing.T) {
	entries := validEntries()
	entries = append(entries, testEntry{name: "../evil.py", body: []byte("evil")})
	err := ValidateWheel(buildWheel(t, entries), testWheelName)
	if err == nil || !strings.Contains(err.Error(), "parent-directory") {
		t.Fatalf("want traversal error, got %v", err)
	}
}

func TestValidateWheelRejectsAbsoluteEntry(t *testing.T) {
	entries := validEntries()
	entries = append(entries, testEntry{name: "/tmp/evil.py", body: []byte("evil")})
	err := ValidateWheel(buildWheel(t, entries), testWheelName)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("want absolute-path error, got %v", err)
	}
}

func TestValidateWheelRejectsFileMissingFromRecord(t *testing.T) {
	recorded := validEntries()
	archived := append(validEntries(), testEntry{name: "demo_pkg/omitted.py", body: []byte("not recorded")})
	data := buildWheelFrom(t, archived, recorded, nil)
	err := ValidateWheel(data, testWheelName)
	if err == nil || !strings.Contains(err.Error(), "not listed in RECORD") {
		t.Fatalf("want missing-from-RECORD error, got %v", err)
	}
}

func TestValidateWheelRejectsRecordEntryMissingFromArchive(t *testing.T) {
	archived := validEntries()
	recorded := append(validEntries(), testEntry{name: "demo_pkg/missing.py", body: []byte("x")})
	data := buildWheelFrom(t, archived, recorded, nil)
	err := ValidateWheel(data, testWheelName)
	if err == nil || !strings.Contains(err.Error(), "missing archive entry") {
		t.Fatalf("want RECORD entry missing from archive error, got %v", err)
	}
}

func TestValidateWheelRejectsDistInfoNameMismatch(t *testing.T) {
	entries := validEntriesWithDistInfo("other-1.0.dist-info")
	err := ValidateWheel(buildWheel(t, entries), testWheelName)
	if err == nil || !strings.Contains(err.Error(), "does not match METADATA Name") {
		t.Fatalf("want dist-info name mismatch, got %v", err)
	}
}

func TestValidateWheelRejectsDistInfoVersionMismatch(t *testing.T) {
	entries := validEntriesWithDistInfo("demo.pkg-2.0.dist-info")
	err := ValidateWheel(buildWheel(t, entries), "demo_pkg-2.0-py3-none-any.whl")
	if err == nil || !strings.Contains(err.Error(), "METADATA version") {
		t.Fatalf("want dist-info version mismatch, got %v", err)
	}
}

func TestValidateWheelRejectsFilenameVersionMismatch(t *testing.T) {
	data := buildWheel(t, validEntries())
	err := ValidateWheel(data, "demo_pkg-2.0-py3-none-any.whl")
	if err == nil || !strings.Contains(err.Error(), "filename version") {
		t.Fatalf("want filename version mismatch, got %v", err)
	}
}

func TestValidateWheelRejectsFilenameNameMismatch(t *testing.T) {
	data := buildWheel(t, validEntries())
	err := ValidateWheel(data, "other_pkg-1.0-py3-none-any.whl")
	if err == nil || !strings.Contains(err.Error(), "filename name") {
		t.Fatalf("want filename name mismatch, got %v", err)
	}
}

func TestValidateWheelRejectsFilenameTagMismatch(t *testing.T) {
	data := buildWheel(t, validEntries())
	err := ValidateWheel(data, "demo_pkg-1.0-py3-none-linux_x86_64.whl")
	if err == nil || !strings.Contains(err.Error(), "tags") {
		t.Fatalf("want tag mismatch, got %v", err)
	}
}

func TestValidateWheelRejectsTamperedHash(t *testing.T) {
	recorded := validEntries()
	archived := validEntries()
	archived[0].body = []byte("tampered")
	err := ValidateWheel(buildWheelFrom(t, archived, recorded, nil), testWheelName)
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("want hash mismatch, got %v", err)
	}
}

func TestValidateWheelRejectsPaddedHash(t *testing.T) {
	entries := validEntries()
	data := buildWheelWithRecord(t, entries, func(name, hash, size string) (string, string) {
		if name == "demo_pkg/__init__.py" {
			return hash + "=", size
		}
		return hash, size
	})
	err := ValidateWheel(data, testWheelName)
	if err == nil || !strings.Contains(err.Error(), "padded") {
		t.Fatalf("want padded-hash error, got %v", err)
	}
}

func TestValidateWheelRejectsWrongSize(t *testing.T) {
	entries := validEntries()
	data := buildWheelWithRecord(t, entries, func(name, hash, size string) (string, string) {
		if name == "demo_pkg/__init__.py" {
			return hash, "999"
		}
		return hash, size
	})
	err := ValidateWheel(data, testWheelName)
	if err == nil || !strings.Contains(err.Error(), "RECORD says") {
		t.Fatalf("want size mismatch, got %v", err)
	}
}

func TestValidateWheelRejectsNonSHA256Hash(t *testing.T) {
	entries := validEntries()
	data := buildWheelWithRecord(t, entries, func(name, hash, size string) (string, string) {
		if name == "demo_pkg/__init__.py" {
			return strings.Replace(hash, "sha256=", "sha512=", 1), size
		}
		return hash, size
	})
	err := ValidateWheel(data, testWheelName)
	if err == nil || !strings.Contains(err.Error(), "missing sha256") {
		t.Fatalf("want non-sha256 error, got %v", err)
	}
}

func TestValidateWheelAllowsSignatureFilesOutsideRecord(t *testing.T) {
	entries := validEntries()
	entries = append(entries, testEntry{name: testDistInfo + "/RECORD.jws", body: []byte("sig")})
	if err := ValidateWheel(buildWheel(t, entries), testWheelName); err != nil {
		t.Fatalf("signature should be exempt from RECORD: %v", err)
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"Demo_Pkg":       "demo-pkg",
		"demo--..__pkg":  "demo-pkg",
		"Zope.Interface": "zope-interface",
		"already-normal": "already-normal",
		"UPPER":          "upper",
	}
	for input, want := range cases {
		if got := NormalizeName(input); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", input, got, want)
		}
	}
}

func validEntries() []testEntry {
	return validEntriesWithDistInfo(testDistInfo)
}

func validEntriesWithDistInfo(distInfo string) []testEntry {
	return []testEntry{
		{name: "demo_pkg/__init__.py", body: []byte("VALUE = 1\n")},
		{name: distInfo + "/METADATA", body: []byte(
			"Metadata-Version: 2.1\n" +
				"Name: " + testMetadataName + "\n" +
				"Version: " + testVersion + "\n\n" +
				"Demo package.\n")},
		{name: distInfo + "/WHEEL", body: []byte(
			"Wheel-Version: 1.0\n" +
				"Generator: test\n" +
				"Tag: py3-none-any\n")},
	}
}

func buildWheel(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	return buildWheelWithRecord(t, entries, nil)
}

func buildWheelWithRecord(t *testing.T, entries []testEntry, mutate func(name, hash, size string) (string, string)) []byte {
	t.Helper()
	return buildWheelFrom(t, entries, entries, mutate)
}

func buildWheelFrom(t *testing.T, archived, recorded []testEntry, mutate func(name, hash, size string) (string, string)) []byte {
	t.Helper()
	record := make([]byte, 0)
	for _, entry := range recorded {
		if strings.HasSuffix(entry.name, "/") {
			continue
		}
		sum := sha256.Sum256(entry.body)
		hash := "sha256=" + base64.RawURLEncoding.EncodeToString(sum[:])
		size := fmt.Sprintf("%d", len(entry.body))
		if mutate != nil {
			hash, size = mutate(entry.name, hash, size)
		}
		record = append(record, []byte(fmt.Sprintf("%s,%s,%s\n", entry.name, hash, size))...)
	}

	all := make([]testEntry, 0, len(archived)+1)
	all = append(all, archived...)
	all = append(all, testEntry{name: distInfoOf(recorded) + "/RECORD", body: record})
	return zipBytes(t, all)
}

func distInfoOf(entries []testEntry) string {
	for _, entry := range entries {
		first, _, ok := strings.Cut(entry.name, "/")
		if ok && strings.HasSuffix(first, ".dist-info") {
			return first
		}
	}
	return testDistInfo
}

func zipBytes(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", entry.name, err)
		}
		if _, err := file.Write(entry.body); err != nil {
			t.Fatalf("write zip entry %q: %v", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

func writeWheelFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write wheel: %v", err)
	}
	return path
}

package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Manifest describes the contents of one version directory.
type Manifest struct {
	Files map[string]string `json:"files"` // file name -> hex-encoded SHA-256
}

func writeManifest(dir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileSync(filepath.Join(dir, manifestFileName), data, 0o644)
}

func loadManifest(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, manifestFileName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("invalid manifest: %w", err)
	}
	return m, nil
}

// validateVersionDir checks that dir contains exactly the files listed in
// its manifest and that every checksum matches.
func validateVersionDir(dir string) error {
	m, err := loadManifest(dir)
	if err != nil {
		return err
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("manifest lists no files")
	}
	for name, want := range m.Files {
		if err := checkFileName(name); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("manifest file %q: %w", name, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			return fmt.Errorf("checksum mismatch for %q", name)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			return fmt.Errorf("unexpected subdirectory %q", e.Name())
		}
		if e.Name() == manifestFileName {
			continue
		}
		if _, ok := m.Files[e.Name()]; !ok {
			return fmt.Errorf("unexpected file %q not in manifest", e.Name())
		}
	}
	return nil
}

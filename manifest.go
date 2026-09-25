package pagebackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
)

// Manifest types.
const (
	ManifestFull = "full"
	ManifestIncr = "incr"
)

const manifestName = "manifest.json"

// Manifest describes one backup. It is the trust anchor of a restore:
// it carries the baseline/target version, the sha256 of every page
// payload, the digest of the whole database at Target, and a checksum
// over the manifest itself.
type Manifest struct {
	Type     string            `json:"type"`     // ManifestFull or ManifestIncr
	Base     uint64            `json:"base"`     // version the incremental applies to (0 for full)
	Target   uint64            `json:"target"`   // version reached after applying this backup
	Pages    map[uint32]string `json:"pages"`    // page id -> sha256 hex of payload
	Digest   string            `json:"digest"`   // digest of the whole DB at Target
	Checksum string            `json:"checksum"` // sha256 of the manifest with Checksum cleared
}

func (m *Manifest) checksum() string {
	saved := m.Checksum
	m.Checksum = ""
	data, _ := json.Marshal(m) // deterministic: Go sorts map keys
	m.Checksum = saved
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeManifest(dir string, m *Manifest) error {
	m.Checksum = m.checksum()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, manifestName), data); err != nil {
		return err
	}
	return fsyncDir(dir)
}

func loadManifest(dir string) (*Manifest, error) {
	data, err := ioutil.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("load manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest corrupt: %v", err)
	}
	if m.Checksum == "" || m.Checksum != m.checksum() {
		return nil, fmt.Errorf("manifest checksum mismatch (tampered or truncated)")
	}
	if m.Type != ManifestFull && m.Type != ManifestIncr {
		return nil, fmt.Errorf("unknown manifest type %q", m.Type)
	}
	return &m, nil
}

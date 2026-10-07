package exporter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

const manifestName = "manifest.json"

// PageEntry describes one exported page file.
type PageEntry struct {
	Index  int    `json:"index"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Count  int    `json:"count"`
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
}

// Manifest records which snapshot was exported, the page ranges, and a
// digest per page so resume can verify reuse instead of trusting offsets.
type Manifest struct {
	SnapshotID string      `json:"snapshot_id"`
	PageSize   int         `json:"page_size"`
	Pages      []PageEntry `json:"pages"`
	Total      int         `json:"total"`
	Complete   bool        `json:"complete"`
	// SetSHA256 is a digest over all page digests, guarding the full set.
	SetSHA256 string `json:"set_sha256"`
}

func pageFileName(index int) string {
	return fmt.Sprintf("page-%06d.json", index)
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// setDigest folds page digests into a single digest of the exported set.
func setDigest(pages []PageEntry) string {
	h := sha256.New()
	for _, p := range pages {
		h.Write([]byte(p.SHA256))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func loadManifest(dir string) (*Manifest, error) {
	data, err := ioutil.ReadFile(filepath.Join(dir, manifestName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		// A corrupt manifest must not be trusted for reuse.
		return nil, nil
	}
	return &m, nil
}

func writeFileAtomic(dir, name string, data []byte) error {
	tmp, err := ioutil.TempFile(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func (m *Manifest) save(dir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(dir, manifestName, data)
}

// pageReusable reports whether the on-disk page file matches the manifest
// entry exactly: same snapshot-pinned range and a verified content digest.
func pageReusable(dir string, e PageEntry) bool {
	if e.File != pageFileName(e.Index) {
		return false
	}
	data, err := ioutil.ReadFile(filepath.Join(dir, e.File))
	if err != nil {
		return false
	}
	return digestBytes(data) == e.SHA256
}

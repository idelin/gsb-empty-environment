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

// PageEntry is the manifest record for one exported page file.
type PageEntry struct {
	Index    int    `json:"index"`
	File     string `json:"file"`
	StartKey string `json:"start_key"` // first key in the page ("" if empty page)
	EndKey   string `json:"end_key"`   // last key in the page ("" if empty page)
	Count    int    `json:"count"`
	SHA256   string `json:"sha256"` // hex digest of the page file
}

// Manifest describes an in-progress or completed export of one snapshot.
type Manifest struct {
	SnapshotID string      `json:"snapshot_id"`
	PageSize   int         `json:"page_size"`
	NextToken  string      `json:"next_token"` // resume cursor: token for the next page to read
	Done       bool        `json:"done"`
	Pages      []PageEntry `json:"pages"`
}

// Exporter streams a snapshot from a Source into page files plus a manifest.
type Exporter struct {
	Source   Source
	Dir      string
	PageSize int
	// MaxPagesPerRun stops the run after N newly exported pages (0 = no
	// limit). It simulates a crash/interruption between resume runs.
	MaxPagesPerRun int
}

func pageFileName(index int) string {
	return fmt.Sprintf("page-%06d.json", index)
}

// Export runs (or resumes) the export. It returns the manifest describing the
// pages that are valid on disk after this run.
func (e *Exporter) Export() (*Manifest, error) {
	snapID := e.Source.SnapshotID()
	m, err := e.loadOrInit(snapID)
	if err != nil {
		return nil, err
	}
	exported := 0
	for !m.Done {
		if e.MaxPagesPerRun > 0 && exported >= e.MaxPagesPerRun {
			break
		}
		page, err := e.Source.ReadPage(snapID, m.NextToken, e.PageSize)
		if err != nil {
			return nil, err
		}
		entry, err := e.writePage(len(m.Pages), page)
		if err != nil {
			return nil, err
		}
		m.Pages = append(m.Pages, *entry)
		m.NextToken = page.NextToken
		m.Done = page.NextToken == ""
		if err := e.saveManifest(m); err != nil {
			return nil, err
		}
		exported++
	}
	return m, nil
}

// loadOrInit returns a manifest whose listed pages are all verified on disk.
// A snapshot mismatch discards the old state entirely; corrupt or missing
// page files truncate the manifest back to the last good page so those pages
// are re-read from the source instead of trusted by offset.
func (e *Exporter) loadOrInit(snapID string) (*Manifest, error) {
	fresh := &Manifest{SnapshotID: snapID, PageSize: e.PageSize}
	path := filepath.Join(e.Dir, manifestName)
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		return fresh, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		// Unreadable manifest: nothing can be trusted, start over.
		return fresh, nil
	}
	if m.SnapshotID != snapID || m.PageSize != e.PageSize {
		return fresh, nil
	}
	good := 0
	for _, p := range m.Pages {
		if e.verifyPage(p) != nil {
			break
		}
		good++
	}
	if good < len(m.Pages) {
		m.Pages = m.Pages[:good]
		m.Done = false
		if good == 0 {
			m.NextToken = ""
		} else {
			m.NextToken = m.Pages[good-1].EndKey
		}
	}
	return &m, nil
}

// verifyPage checks a page file against its manifest entry.
func (e *Exporter) verifyPage(p PageEntry) error {
	data, err := ioutil.ReadFile(filepath.Join(e.Dir, p.File))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != p.SHA256 {
		return fmt.Errorf("exporter: checksum mismatch for %s", p.File)
	}
	return nil
}

func (e *Exporter) writePage(index int, page Page) (*PageEntry, error) {
	payload, err := json.Marshal(page.Records)
	if err != nil {
		return nil, err
	}
	name := pageFileName(index)
	if err := writeFileAtomic(filepath.Join(e.Dir, name), payload); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	entry := &PageEntry{
		Index:  index,
		File:   name,
		Count:  len(page.Records),
		SHA256: hex.EncodeToString(sum[:]),
	}
	if len(page.Records) > 0 {
		entry.StartKey = page.Records[0].Key
		entry.EndKey = page.Records[len(page.Records)-1].Key
	}
	return entry, nil
}

func (e *Exporter) saveManifest(m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(e.Dir, manifestName), data)
}

// writeFileAtomic writes via a temp file + rename so a crash never leaves a
// half-written page or manifest behind.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Collect reads every verified page file and returns the full exported
// record set in key order.
func (e *Exporter) Collect(m *Manifest) ([]Record, error) {
	var out []Record
	for _, p := range m.Pages {
		if err := e.verifyPage(p); err != nil {
			return nil, err
		}
		data, err := ioutil.ReadFile(filepath.Join(e.Dir, p.File))
		if err != nil {
			return nil, err
		}
		var recs []Record
		if err := json.Unmarshal(data, &recs); err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

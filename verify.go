package exporter

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
)

// Verify loads the manifest and reassembles the full exported set, checking
// every page file against its recorded digest and the set digest as a whole.
// It never trusts offsets or counts without verifying content.
func Verify(dir string) ([]Record, error) {
	m, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("no manifest in %s", dir)
	}
	if !m.Complete {
		return nil, fmt.Errorf("manifest is incomplete")
	}
	var all []Record
	for _, p := range m.Pages {
		data, err := ioutil.ReadFile(filepath.Join(dir, p.File))
		if err != nil {
			return nil, fmt.Errorf("page %d: %v", p.Index, err)
		}
		if digestBytes(data) != p.SHA256 {
			return nil, fmt.Errorf("page %d: digest mismatch", p.Index)
		}
		var recs []Record
		if err := json.Unmarshal(data, &recs); err != nil {
			return nil, fmt.Errorf("page %d: %v", p.Index, err)
		}
		if len(recs) != p.Count {
			return nil, fmt.Errorf("page %d: count mismatch", p.Index)
		}
		all = append(all, recs...)
	}
	if len(all) != m.Total {
		return nil, fmt.Errorf("total mismatch: got %d, manifest says %d", len(all), m.Total)
	}
	if setDigest(m.Pages) != m.SetSHA256 {
		return nil, fmt.Errorf("set digest mismatch")
	}
	return all, nil
}

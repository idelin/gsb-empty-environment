package exporter

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

// Stats reports what a single Export run did.
type Stats struct {
	SnapshotID   string
	PagesWritten int
	PagesReused  int
	Total        int
}

// Export pages through the source pinned to one snapshot, writing page files
// and a manifest into dir. On re-run it resumes: pages whose manifest entry
// matches the current snapshot and whose file digest verifies are reused;
// anything else (snapshot drift, corrupt or missing page files, stale
// offsets) is re-exported page by page.
func Export(src Source, dir string, pageSize int) (*Stats, error) {
	if pageSize <= 0 {
		return nil, fmt.Errorf("pageSize must be positive")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	snap := src.SnapshotID()
	stats := &Stats{SnapshotID: snap}

	old, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}
	reusable := map[int]PageEntry{}
	// Reuse is only allowed from a manifest pinned to the same snapshot and
	// the same page geometry; otherwise offsets are meaningless.
	if old != nil && old.SnapshotID == snap && old.PageSize == pageSize {
		for _, e := range old.Pages {
			reusable[e.Index] = e
		}
	}

	m := &Manifest{SnapshotID: snap, PageSize: pageSize}
	offset := 0
	for index := 0; ; index++ {
		recs, err := src.Page(snap, offset, pageSize)
		if err != nil {
			return nil, fmt.Errorf("page %d: %v", index, err)
		}
		entry := PageEntry{
			Index:  index,
			Offset: offset,
			Limit:  pageSize,
			Count:  len(recs),
			File:   pageFileName(index),
		}
		if prev, ok := reusable[index]; ok &&
			prev.Offset == entry.Offset && prev.Limit == entry.Limit &&
			prev.Count == entry.Count && pageReusable(dir, prev) {
			entry.SHA256 = prev.SHA256
			stats.PagesReused++
		} else {
			data, err := json.MarshalIndent(recs, "", "  ")
			if err != nil {
				return nil, err
			}
			if err := writeFileAtomic(dir, entry.File, data); err != nil {
				return nil, err
			}
			entry.SHA256 = digestBytes(data)
			stats.PagesWritten++
		}
		m.Pages = append(m.Pages, entry)
		offset += len(recs)
		if len(recs) < pageSize {
			break
		}
	}
	m.Total = offset
	m.Complete = true
	m.SetSHA256 = setDigest(m.Pages)
	stats.Total = offset

	if err := m.save(dir); err != nil {
		return nil, err
	}
	// Remove stale page files left over from a previously longer export.
	keep := map[string]bool{manifestName: true}
	for _, p := range m.Pages {
		keep[p.File] = true
	}
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !keep[e.Name()] && filepath.Ext(e.Name()) == ".json" {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return stats, nil
}

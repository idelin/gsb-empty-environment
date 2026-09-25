package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type Issue struct {
	Code    string `json:"code"`
	Segment string `json:"segment,omitempty"`
	Key     string `json:"key,omitempty"`
	Detail  string `json:"detail"`
}

const (
	ActionUnrecoverable = "unrecoverable"
	ActionRemoveOrphan  = "remove-orphan"
	ActionRebuildIndex  = "rebuild-index"
	ActionSyncManifest  = "sync-manifest"
)

type Action struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Segment string `json:"segment,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type Plan struct {
	Version      int      `json:"version"`
	StateSummary string   `json:"state_summary"`
	Issues       []Issue  `json:"issues"`
	Actions      []Action `json:"actions"`
}

type Report struct {
	Dir          string `json:"dir"`
	StateSummary string `json:"state_summary"`
	Issues       []Issue
	Plan         Plan
}

func (r *Report) Clean() bool { return len(r.Issues) == 0 }

// Audit is strictly read-only: it never opens any file in dir for writing.
func Audit(dir string) (*Report, error) {
	rep := &Report{Dir: dir}
	summary, err := StateSummary(dir)
	if err != nil {
		return nil, err
	}
	rep.StateSummary = summary

	add := func(code, segment, detail string) {
		rep.Issues = append(rep.Issues, Issue{Code: code, Segment: segment, Detail: detail})
	}

	manifest, merr := readManifest(dir)
	if merr != nil {
		add("manifest-unreadable", "", merr.Error()+"; cannot fabricate a manifest")
		return finalizeReport(rep, nil, false, "", true, false), nil
	}

	segBroken := false
	var validNames []string
	validSet := map[string]bool{}
	segData := map[string][]byte{}
	for _, sm := range manifest.Segments {
		data, err := os.ReadFile(segmentPath(dir, sm.Name))
		if os.IsNotExist(err) {
			add("segment-missing", sm.Name, "listed in manifest but absent on disk")
			segBroken = true
			continue
		}
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != sm.Size {
			add("segment-size-mismatch", sm.Name,
				fmt.Sprintf("manifest size %d, disk size %d", sm.Size, len(data)))
			segBroken = true
			continue
		}
		if got := sha256Hex(data); got != sm.SHA256 {
			add("segment-checksum-mismatch", sm.Name,
				"content differs from manifest sha256; original bytes cannot be fabricated")
			segBroken = true
			continue
		}
		validSet[sm.Name] = true
		validNames = append(validNames, sm.Name)
		segData[sm.Name] = data
	}

	inManifest := map[string]bool{}
	for _, sm := range manifest.Segments {
		inManifest[sm.Name] = true
	}
	onDisk, err := listSegmentFiles(dir)
	if err != nil {
		return nil, err
	}
	var orphans []string
	for _, name := range onDisk {
		if !inManifest[name] {
			add("segment-orphan", name, "on disk but not listed in manifest")
			orphans = append(orphans, name)
		}
	}

	indexBroken := false
	indexReason := ""
	idxData, err := os.ReadFile(indexPath(dir))
	switch {
	case os.IsNotExist(err):
		indexBroken = true
		indexReason = "index-missing"
		add("index-missing", "", "index.json absent; rebuildable from intact segments")
	case err != nil:
		return nil, err
	default:
		if manifest.Index != nil && sha256Hex(idxData) != manifest.Index.SHA256 {
			indexBroken = true
			indexReason = "index-hash-mismatch"
			add("index-hash-mismatch", "", "index content differs from manifest record")
		}
		var idx Index
		if jerr := json.Unmarshal(idxData, &idx); jerr != nil {
			indexBroken = true
			indexReason = "index-corrupt"
			add("index-corrupt", "", "index.json is not valid JSON: "+jerr.Error())
		} else if !indexBroken {
			if badKey, verr := verifyIndexEntries(&idx, validSet, segData); verr != nil {
				indexBroken = true
				indexReason = "index-entry-invalid"
				add("index-entry-invalid", "", fmt.Sprintf("key %q: %s", badKey, verr))
			} else if !indexesEqual(&idx, buildIndex(segData, validNames)) {
				indexBroken = true
				indexReason = "index-stale"
				add("index-stale", "", "index entries differ from a fresh scan of the segments")
			}
		}
	}

	if indexBroken && segBroken {
		add("index-rebuild-withheld", "",
			"index is broken but some segments are unrecoverable; rebuilding would cement data loss")
	}
	return finalizeReport(rep, orphans, indexBroken, indexReason, segBroken, true), nil
}

func verifyIndexEntries(idx *Index, validSet map[string]bool, segData map[string][]byte) (string, error) {
	keys := make([]string, 0, len(idx.Entries))
	for k := range idx.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		e := idx.Entries[key]
		data, ok := segData[e.Segment]
		if !ok || !validSet[e.Segment] {
			return key, fmt.Errorf("references segment %q which is missing or invalid", e.Segment)
		}
		if e.Offset < 0 || e.Length < 0 || e.Offset+e.Length > int64(len(data)) {
			return key, fmt.Errorf("offset %d length %d out of bounds for segment %q", e.Offset, e.Length, e.Segment)
		}
		recs, tail := scanSegment(data[e.Offset : e.Offset+e.Length])
		if tail || len(recs) != 1 || recs[0].Key != key || recs[0].Length != e.Length {
			return key, fmt.Errorf("record at offset %d does not decode to this key", e.Offset)
		}
	}
	return "", nil
}

func indexesEqual(a, b *Index) bool {
	if len(a.Entries) != len(b.Entries) {
		return false
	}
	for k, ea := range a.Entries {
		eb, ok := b.Entries[k]
		if !ok || ea != eb {
			return false
		}
	}
	return true
}

func finalizeReport(rep *Report, orphans []string, indexBroken bool, indexReason string, segBroken, manifestOK bool) *Report {
	plan := Plan{Version: 1, StateSummary: rep.StateSummary, Issues: rep.Issues}
	n := 0
	nextID := func() string { n++; return fmt.Sprintf("action-%d", n) }
	for _, is := range rep.Issues {
		switch is.Code {
		case "segment-missing", "segment-size-mismatch", "segment-checksum-mismatch":
			plan.Actions = append(plan.Actions, Action{
				ID: nextID(), Type: ActionUnrecoverable, Segment: is.Segment,
				Reason: is.Code + ": original bytes cannot be fabricated; restore from backup",
			})
		case "manifest-unreadable":
			plan.Actions = append(plan.Actions, Action{
				ID: nextID(), Type: ActionUnrecoverable,
				Reason: is.Code + ": cannot fabricate a manifest",
			})
		}
	}
	for _, name := range orphans {
		plan.Actions = append(plan.Actions, Action{
			ID: nextID(), Type: ActionRemoveOrphan, Segment: name,
			Reason: "not referenced by manifest",
		})
	}
	if indexBroken && !segBroken && manifestOK {
		plan.Actions = append(plan.Actions,
			Action{ID: nextID(), Type: ActionRebuildIndex, Reason: indexReason},
			Action{ID: nextID(), Type: ActionSyncManifest, Reason: "record rebuilt index digest in manifest"},
		)
	}
	rep.Plan = plan
	return rep
}

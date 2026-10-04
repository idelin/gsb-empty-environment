package pagezip

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Ledger operations.
const (
	opDictAdd = "dict_add"
	opDictDel = "dict_del"
	opSetRef  = "setref"
	opDelPage = "delpage"
)

// ledgerEntry is one JSON-lines record in the reference ledger.
type ledgerEntry struct {
	Op     string `json:"op"`
	Page   string `json:"page,omitempty"`
	DictID string `json:"dict,omitempty"`
	Digest string `json:"digest,omitempty"`
}

func appendEntry(w io.Writer, e ledgerEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// ReplayLedger folds the ledger into page->DictRef assignments and per
// dictionary-version reference counts. It is exported so the ledger can be
// replayed independently of a live Store for auditing and recovery checks.
//
// headers holds the committed on-disk page headers and present the set of
// page files that exist. A page file is the commit record of a dictionary
// switch (it is written before its ledger entry), so a committed header
// overrides the ledger, and a ledger reference to a page whose file is gone
// is dropped. A page file that exists but fails to decode keeps its ledger
// reference, so reclamation stays conservative.
//
// A malformed line stops the replay; this tolerates a torn tail write from
// a crash.
func ReplayLedger(r io.Reader, headers map[string]DictRef, present map[string]bool) (map[string]DictRef, map[DictRef]int, error) {
	pageRefs := make(map[string]DictRef)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var e ledgerEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			break // torn tail from an interrupted append
		}
		switch e.Op {
		case opDictAdd, opDictDel:
			if _, err := ParseDigest(e.Digest); err != nil {
				return nil, nil, fmt.Errorf("pagezip: ledger line %d: %v", line, err)
			}
		case opSetRef:
			d, err := ParseDigest(e.Digest)
			if err != nil {
				return nil, nil, fmt.Errorf("pagezip: ledger line %d: %v", line, err)
			}
			pageRefs[e.Page] = DictRef{ID: e.DictID, Digest: d}
		case opDelPage:
			delete(pageRefs, e.Page)
		default:
			return nil, nil, fmt.Errorf("pagezip: ledger line %d: unknown op %q", line, e.Op)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	for page := range pageRefs {
		if !present[page] {
			delete(pageRefs, page)
		}
	}
	for page, ref := range headers {
		pageRefs[page] = ref
	}
	counts := make(map[DictRef]int)
	for _, ref := range pageRefs {
		counts[ref]++
	}
	return pageRefs, counts, nil
}

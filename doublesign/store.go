package doublesign

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Store is an append-only durable log. Epoch sets and verified shares are
// appended as JSON lines; convictions are written as immutable, hash-named
// evidence files. Reopening a directory replays the log so no conflict that
// was observable before a crash can be missed afterward.
type Store struct {
	dir       string
	epochFile *os.File
	msgFile   *os.File
	encEpoch  *json.Encoder
	encMsg    *json.Encoder
}

type storedEpoch struct {
	Set    EpochSet        `json:"set"`
	Anchor AuthorityAnchor `json:"anchor"`
}

// OpenStore opens (or creates) a persistence directory.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "evidence"), 0o755); err != nil {
		return nil, err
	}
	ef, err := os.OpenFile(filepath.Join(dir, "epochs.jsonl"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	mf, err := os.OpenFile(filepath.Join(dir, "messages.jsonl"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		_ = ef.Close()
		return nil, err
	}
	return &Store{
		dir:       dir,
		epochFile: ef,
		msgFile:   mf,
		encEpoch:  json.NewEncoder(ef),
		encMsg:    json.NewEncoder(mf),
	}, nil
}

// Close flushes and closes the underlying files.
func (s *Store) Close() error {
	var err error
	if e := s.epochFile.Close(); e != nil {
		err = e
	}
	if e := s.msgFile.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

func (s *Store) appendEpoch(set EpochSet, anchor AuthorityAnchor) error {
	if err := s.encEpoch.Encode(storedEpoch{Set: set, Anchor: anchor}); err != nil {
		return err
	}
	return s.epochFile.Sync()
}

func (s *Store) appendMessage(m Message) error {
	if err := s.encMsg.Encode(m); err != nil {
		return err
	}
	return s.msgFile.Sync()
}

func (s *Store) appendEvidence(ev Evidence) error {
	name := fmt.Sprintf("%d-%s-%x.json", ev.EpochSet.Epoch, ev.MemberID,
		Root(ev.EpochSet)[:8])
	path := filepath.Join(s.dir, "evidence", name)
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WithStore attaches the store and replays its log into the auditor. Replay is
// deterministic: epochs are registered first, then shares are re-fed in the
// exact original arrival order without re-appending, which reconstructs both
// the active state and any freeze.
func (a *Auditor) WithStore(s *Store) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	epochs, err := loadEpochs(s.dir)
	if err != nil {
		return err
	}
	if len(a.authPub) == 0 && len(epochs) > 0 {
		return errors.New("cannot recover: authority public key must be registered first")
	}
	for _, se := range epochs {
		if err := se.Anchor.Verify(se.Set, a.authPub); err != nil {
			return err
		}
		if _, ok := a.epochs[se.Set.Epoch]; ok {
			continue
		}
		if err := se.Set.Validate(); err != nil {
			return err
		}
		a.epochs[se.Set.Epoch] = se.Set
		a.anchors[se.Set.Epoch] = se.Anchor
		for _, m := range se.Set.Members {
			if _, ok := a.members[m.ID]; !ok {
				a.members[m.ID] = &memberState{heights: map[uint64]*heightState{}}
			}
		}
	}

	msgs, err := loadMessages(s.dir)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		r := a.observeLocked(m, false)
		// A bad signature on replay means the append log was tampered with.
		// Membership rejections are legitimate: a member removed in a later
		// epoch still has valid shares from the epoch in which it served.
		if r.Verdict == VerdictRejected && r.Reason == "signature verification failed" {
			return errors.New("corrupt log: " + r.Reason)
		}
	}

	// Pre-mark members already convicted by evidence files even if their
	// offending messages were somehow truncated from the append log.
	evs, err := LoadEvidenceDir(filepath.Join(s.dir, "evidence"))
	if err != nil {
		return err
	}
	for _, ev := range evs {
		if ms, ok := a.members[ev.MemberID]; ok {
			ms.frozen = true
			a.dropStaged(ev.MemberID)
		}
	}

	a.store = s
	return nil
}

func loadEpochs(dir string) ([]storedEpoch, error) {
	data, err := os.ReadFile(filepath.Join(dir, "epochs.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []storedEpoch
	if err := decodeJSONLines(data, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Set.Epoch < out[j].Set.Epoch })
	return out, nil
}

func loadMessages(dir string) ([]Message, error) {
	data, err := os.ReadFile(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Message
	if err := decodeJSONLines(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeJSONLines(data []byte, out interface{}) error {
	return json.Unmarshal(append([]byte("["), appendLines(data)...), out)
}

// appendLines turns newline-delimited JSON into a JSON array body.
func appendLines(data []byte) []byte {
	var body []byte
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			line := data[start:i]
			start = i + 1
			if len(line) == 0 {
				continue
			}
			if len(body) > 0 {
				body = append(body, ',')
			}
			body = append(body, line...)
		}
	}
	return append(body, ']')
}

// LoadEvidenceDir reads all evidence artifacts from a directory.
func LoadEvidenceDir(dir string) ([]Evidence, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Evidence
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var ev Evidence
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

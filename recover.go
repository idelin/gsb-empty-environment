package netsec

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// proofStage tracks how far an authorization upgrade has progressed.
type proofStage int

const (
	stageNone proofStage = iota
	stageProofReceived
	stageVerified
)

// State is the recovered authorization snapshot handed to the Go state
// host. A permission is present only if a complete, checksum-valid commit
// was replayed and no later revoke removed it.
type State struct {
	grants  map[string]map[string]struct{}
	pending map[string]proofStage
}

func newState() *State {
	return &State{
		grants:  make(map[string]map[string]struct{}),
		pending: make(map[string]proofStage),
	}
}

func grantKey(subject, permission string) string {
	return subject + "\x00" + permission
}

func proofKey(subject, permission, proof string) string {
	return subject + "\x00" + permission + "\x00" + proof
}

// Allows reports whether subject currently holds permission.
func (s *State) Allows(subject, permission string) bool {
	perms, ok := s.grants[subject]
	if !ok {
		return false
	}
	_, ok = perms[permission]
	return ok
}

// Permissions returns the sorted permissions held by subject.
func (s *State) Permissions(subject string) []string {
	var out []string
	for p := range s.grants[subject] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (s *State) apply(e Event) {
	switch e.Type {
	case EventProofReceived:
		s.pending[proofKey(e.Subject, e.Permission, e.Proof)] = stageProofReceived
	case EventVerified:
		k := proofKey(e.Subject, e.Permission, e.Proof)
		if s.pending[k] == stageProofReceived {
			s.pending[k] = stageVerified
		}
	case EventCommit:
		// A commit restores the permission only when the matching
		// proof was received and verified beforehand; the commit
		// record itself already survived the checksum gate.
		k := proofKey(e.Subject, e.Permission, e.Proof)
		if s.pending[k] != stageVerified {
			return
		}
		perms, ok := s.grants[e.Subject]
		if !ok {
			perms = make(map[string]struct{})
			s.grants[e.Subject] = perms
		}
		perms[e.Permission] = struct{}{}
	case EventRevoke:
		if perms, ok := s.grants[e.Subject]; ok {
			delete(perms, e.Permission)
			if len(perms) == 0 {
				delete(s.grants, e.Subject)
			}
		}
		prefix := grantKey(e.Subject, e.Permission) + "\x00"
		for k := range s.pending {
			if strings.HasPrefix(k, prefix) {
				delete(s.pending, k)
			}
		}
	}
}

// Recover replays the log at path and returns the restored State.
//
// A torn tail (power loss mid-write) truncates only the uncommitted
// suffix: the file is cut back to the end of the last complete record and
// recovery proceeds. If a checksum-invalid record is followed by further
// valid records, the damage is in the middle of the log and recovery is
// refused with ErrCorrupt.
func Recover(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newState(), nil
	}
	if err != nil {
		return nil, err
	}
	st := newState()
	off := 0
	for off < len(data) {
		e, _, n, derr := decodeRecordAt(data, off)
		if derr != nil {
			if hasValidRecord(data, off) {
				return nil, fmt.Errorf("%w: invalid record at offset %d with valid records beyond", ErrCorrupt, off)
			}
			if terr := os.Truncate(path, int64(off)); terr != nil {
				return nil, fmt.Errorf("netsec: truncate torn tail: %w", terr)
			}
			break
		}
		st.apply(e)
		off += n
	}
	return st, nil
}

// hasValidRecord reports whether any well-formed, checksum-valid record
// starts at or after from. A resync hit means the damage at from is not a
// torn tail but mid-log corruption.
func hasValidRecord(data []byte, from int) bool {
	for i := from; i+recordHdrSize+recordCRCSize <= len(data); i++ {
		if _, _, _, err := decodeRecordAt(data, i); err == nil {
			return true
		}
	}
	return false
}

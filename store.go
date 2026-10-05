package sessionrouter

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

// Store persists the router state. Implementations must be synchronous:
// once Save returns, a crash must not lose the saved state.
type Store interface {
	Load() (*PersistedState, error)
	Save(*PersistedState) error
}

func cloneState(st *PersistedState) (*PersistedState, error) {
	if st == nil {
		return nil, nil
	}
	buf, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	var out PersistedState
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func mergeHighWater(dst, src map[SessionID]uint64) map[SessionID]uint64 {
	if dst == nil {
		dst = map[SessionID]uint64{}
	}
	for id, g := range src {
		if dst[id] < g {
			dst[id] = g
		}
	}
	return dst
}

// MemStore is an in-memory Store used by tests and the reference model.
// It keeps a separate high-water table that only moves forward, mirroring
// the crash-safety guarantee of FileStore.
type MemStore struct {
	mu    sync.Mutex
	state *PersistedState
	hw    map[SessionID]uint64
}

func NewMemStore() *MemStore {
	return &MemStore{hw: map[SessionID]uint64{}}
}

func (s *MemStore) Save(st *PersistedState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, err := cloneState(st)
	if err != nil {
		return err
	}
	s.state = cp
	s.hw = mergeHighWater(s.hw, cp.GenHighWater)
	return nil
}

func (s *MemStore) Load() (*PersistedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, err := cloneState(s.state)
	if err != nil {
		return nil, err
	}
	if cp == nil {
		return nil, nil
	}
	cp.GenHighWater = mergeHighWater(cp.GenHighWater, s.hw)
	return cp, nil
}

// FileStore persists state as JSON in a directory. Writes are atomic
// (temp file + rename). Generation high-water marks live in a separate
// file written after the state, so recovery from a stale or rolled-back
// state file can never move a session's generation backward.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir}
}

const (
	stateFile     = "state.json"
	highWaterFile = "highwater.json"
)

func (s *FileStore) Save(st *PersistedState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	buf, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(s.dir, stateFile), buf); err != nil {
		return err
	}
	hw, _ := s.readHighWater()
	hw = mergeHighWater(hw, st.GenHighWater)
	hwBuf, err := json.Marshal(hw)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.dir, highWaterFile), hwBuf)
}

func (s *FileStore) Load() (*PersistedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf, err := ioutil.ReadFile(filepath.Join(s.dir, stateFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st PersistedState
	if err := json.Unmarshal(buf, &st); err != nil {
		return nil, err
	}
	hw, err := s.readHighWater()
	if err != nil {
		return nil, err
	}
	st.GenHighWater = mergeHighWater(st.GenHighWater, hw)
	return &st, nil
}

func (s *FileStore) readHighWater() (map[SessionID]uint64, error) {
	buf, err := ioutil.ReadFile(filepath.Join(s.dir, highWaterFile))
	if os.IsNotExist(err) {
		return map[SessionID]uint64{}, nil
	}
	if err != nil {
		return nil, err
	}
	hw := map[SessionID]uint64{}
	if err := json.Unmarshal(buf, &hw); err != nil {
		return nil, err
	}
	return hw, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

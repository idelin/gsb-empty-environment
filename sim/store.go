package sim

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrTransactionClosed = errors.New("transaction is closed")

type Store struct {
	data map[string]json.RawMessage
}

func NewStore() *Store {
	return &Store{data: make(map[string]json.RawMessage)}
}

func (s *Store) Begin() *TX {
	working := make(map[string]json.RawMessage, len(s.data))
	for key, value := range s.data {
		working[key] = append(json.RawMessage(nil), value...)
	}
	return &TX{store: s, working: working}
}

func (s *Store) KeysPrefix(prefix string) []string {
	keys := make([]string, 0)
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

type TX struct {
	store    *Store
	working  map[string]json.RawMessage
	commits  []func()
	closed   bool
	finished bool
}

func (tx *TX) Put(key string, value any) error {
	if tx.finished {
		return ErrTransactionClosed
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tx.working[key] = json.RawMessage(encoded)
	return nil
}

func (tx *TX) Get(key string, target any) (bool, error) {
	if tx.finished {
		return false, ErrTransactionClosed
	}
	encoded, ok := tx.working[key]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return false, err
	}
	return true, nil
}

func (tx *TX) Has(key string) (bool, error) {
	if tx.finished {
		return false, ErrTransactionClosed
	}
	_, ok := tx.working[key]
	return ok, nil
}

func (tx *TX) KeysPrefix(prefix string) []string {
	keys := make([]string, 0)
	for key := range tx.working {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

func (tx *TX) OnCommit(action func()) {
	if !tx.finished {
		tx.commits = append(tx.commits, action)
	}
}

func (tx *TX) Commit() error {
	if tx.finished {
		return ErrTransactionClosed
	}
	tx.finished = true
	for key, value := range tx.working {
		tx.store.data[key] = append(json.RawMessage(nil), value...)
	}
	actions := tx.commits
	tx.working = nil
	tx.commits = nil
	for _, action := range actions {
		action()
	}
	return nil
}

func (tx *TX) Abort() error {
	if tx.finished {
		return ErrTransactionClosed
	}
	tx.finished = true
	tx.working = nil
	tx.commits = nil
	return nil
}

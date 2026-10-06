package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
)

// Store simulates durable storage (a file). Crash = drop the in-memory
// object; Recover = reload from the Store.
type Store struct {
	path string
}

type persistentState struct {
	NextFence uint64        `json:"next_fence,omitempty"` // lease manager: next token to hand out
	LastFence uint64        `json:"last_fence,omitempty"` // resource: highest accepted fence
	Data      string        `json:"data,omitempty"`       // resource: committed value
	History   []WriteRecord `json:"history,omitempty"`    // resource: accepted write log
}

func NewStore(dir, name string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, name)}, nil
}

func (s *Store) Save(st persistentState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	// write-then-rename for atomic durability
	tmp := s.path + ".tmp"
	if err := ioutil.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Load() (persistentState, error) {
	var st persistentState
	b, err := ioutil.ReadFile(s.path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

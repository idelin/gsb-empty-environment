package verifier

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// State layout inside the verifier's own directory (inputs are never
// touched; everything the verifier writes lives here):
//
//	<dir>/staging/<version>/<name-key>.bin   chunk payload
//	<dir>/staging/<version>/<name-key>.json  chunk meta {name, digest, size}
//	<dir>/sealed/<version-key>.json          sealed manifest + report
//
// All writes go through a temp file + rename so a crash never leaves a
// half-written record; recovery simply re-scans the directories.

type chunkMeta struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type sealedRecord struct {
	ManifestDigest string   `json:"manifestDigest"`
	Manifest       Manifest `json:"manifest"`
	Report         Report   `json:"report"`
}

func keyOf(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func (v *Verifier) stagingDir(version string) string {
	return filepath.Join(v.dir, "staging", keyOf(version))
}

func (v *Verifier) sealedDir() string {
	return filepath.Join(v.dir, "sealed")
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (v *Verifier) persistChunk(version string, meta chunkMeta, data []byte) error {
	dir := v.stagingDir(version)
	key := keyOf(meta.Name)
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, key+".bin"), data); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, key+".json"), raw)
}

func (v *Verifier) loadChunk(version, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(v.stagingDir(version), keyOf(name)+".bin"))
}

func (v *Verifier) persistSealed(rec sealedRecord) error {
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(v.sealedDir(), keyOf(rec.Manifest.Version)+".json"), raw)
}

func (v *Verifier) dropStaging(version string) error {
	return os.RemoveAll(v.stagingDir(version))
}

// recover scans the state directory and rebuilds the in-memory index.
func (v *Verifier) recover() error {
	stagingRoot := filepath.Join(v.dir, "staging")
	_ = filepath.WalkDir(stagingRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var meta chunkMeta
		if json.Unmarshal(raw, &meta) != nil {
			return nil
		}
		versionBytes, err := base64.RawURLEncoding.DecodeString(filepath.Base(filepath.Dir(path)))
		if err != nil {
			return nil
		}
		version := string(versionBytes)
		if v.staged[version] == nil {
			v.staged[version] = map[string]chunkMeta{}
		}
		v.staged[version][meta.Name] = meta
		return nil
	})

	sealedRoot := v.sealedDir()
	entries, err := os.ReadDir(sealedRoot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sealedRoot, e.Name()))
		if err != nil {
			continue
		}
		var rec sealedRecord
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		v.sealed[rec.Manifest.Version] = rec
	}
	v.emit(EventRecovered, "", "",
		fmt.Sprintf("recovered %d staged version(s), %d sealed version(s)", len(v.staged), len(v.sealed)))
	return nil
}

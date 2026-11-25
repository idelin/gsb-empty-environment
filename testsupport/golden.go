package testsupport

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"

	"prescreen.local/ddd-10561-v4/zoneauth"
)

// goldenEnvelope is the on-disk shape. Bytes (hashes, signatures, keys) are
// base64 via encoding/json, and map keys are emitted in sorted order, so the
// file is byte-stable across runs given the fixed seeds.
type goldenFile struct {
	Name       string                 `json:"name"`
	ZoneName   string                 `json:"zone_name"`
	Patch      *zoneauth.Patch        `json:"patch"`
	OldRoot    *zoneauth.RootEnvelope `json:"old_root"`
	NewRoot    *zoneauth.RootEnvelope `json:"new_root"`
	OldKey     []byte                 `json:"old_key"`
	NewKey     []byte                 `json:"new_key"`
	Cached     []goldenCached         `json:"cached"`
	ExpectKind map[string]string      `json:"expect_kind"`
}

type goldenCached struct {
	Query  zoneauth.Name    `json:"query"`
	Answer *zoneauth.Answer `json:"answer"`
	Proof  *zoneauth.Proof  `json:"proof"`
}

// MarshalGolden renders a fixture to deterministic indented JSON.
func MarshalGolden(f *Fixture) ([]byte, error) {
	gf := goldenFile{
		Name:       f.Name,
		ZoneName:   f.ZoneName,
		Patch:      f.Patch,
		OldRoot:    f.OldRoot,
		NewRoot:    f.NewRoot,
		OldKey:     append([]byte(nil), f.Keys.OldPriv...),
		NewKey:     append([]byte(nil), f.Keys.NewPriv...),
		ExpectKind: map[string]string{},
	}
	for _, entry := range f.Cached {
		gf.Cached = append(gf.Cached, goldenCached{
			Query:  entry.Query,
			Answer: entry.Answer,
			Proof:  entry.Proof,
		})
	}
	for key, kind := range f.ExpectKind {
		gf.ExpectKind[key] = string(kind)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(&gf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalGolden parses a golden file back into a runnable fixture.
func UnmarshalGolden(raw []byte) (*Fixture, error) {
	var gf goldenFile
	if err := json.Unmarshal(raw, &gf); err != nil {
		return nil, err
	}
	oldPriv := ed25519.PrivateKey(append(ed25519.PrivateKey(nil), gf.OldKey...))
	newPriv := ed25519.PrivateKey(append(ed25519.PrivateKey(nil), gf.NewKey...))
	f := &Fixture{
		Name:     gf.Name,
		ZoneName: gf.ZoneName,
		Patch:    gf.Patch,
		OldRoot:  gf.OldRoot,
		NewRoot:  gf.NewRoot,
		Keys: &Keys{
			OldPriv: oldPriv,
			OldPub:  oldPriv.Public().(ed25519.PublicKey),
			NewPriv: newPriv,
			NewPub:  newPriv.Public().(ed25519.PublicKey),
		},
		ExpectKind: map[string]zoneauth.VerdictKind{},
	}
	for _, entry := range gf.Cached {
		f.Cached = append(f.Cached, QueryCase{
			Query:  entry.Query,
			Answer: entry.Answer,
			Proof:  entry.Proof,
		})
	}
	for key, kind := range gf.ExpectKind {
		f.ExpectKind[key] = zoneauth.VerdictKind(kind)
	}
	return f, nil
}

// GoldenPath is the standard testdata location for a named fixture.
func GoldenPath(dir, name string) string {
	return filepath.Join(dir, "testdata", name+".golden.json")
}

// WriteGolden writes one fixture file.
func WriteGolden(dir string, f *Fixture) error {
	raw, err := MarshalGolden(f)
	if err != nil {
		return err
	}
	path := GoldenPath(dir, f.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// ReadGolden reads one fixture file.
func ReadGolden(dir, name string) (*Fixture, error) {
	raw, err := os.ReadFile(GoldenPath(dir, name))
	if err != nil {
		return nil, err
	}
	return UnmarshalGolden(raw)
}

package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// MaterialKind identifies what was exported for a wallet node.
type MaterialKind string

const (
	// ExtendedPublicKey exports the public key and chain code. It is safe on
	// its own, but together with a non-hardened child private key it reveals
	// the node private key.
	ExtendedPublicKey MaterialKind = "xpub"
	// ExtendedPrivateKey exports the private key and chain code, exposing the
	// node and every descendant.
	ExtendedPrivateKey MaterialKind = "xprv"
	// PrivateKey exports a non-extended private key. Per the export policy,
	// this also exposes every descendant.
	PrivateKey MaterialKind = "priv"
)

// Material is one exported wallet item. The key bytes are intentionally not
// part of the audit model: an xpub is assumed to carry its chain code, an xprv
// is assumed to carry its private key and chain code, and a leaf private key is
// the scalar at Path.
type Material struct {
	Kind MaterialKind `json:"kind"`
	Path Path         `json:"path"`
}

// NewMaterial validates and constructs a material.
func NewMaterial(kind MaterialKind, path Path) (Material, error) {
	material := Material{Kind: kind, Path: path.Clone()}
	return material.normalized()
}

// ParseMaterial parses "xpub:m/0", "xprv m/0", or "priv=m/0".
func ParseMaterial(text string) (Material, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Material{}, fmt.Errorf("empty material")
	}

	separator := -1
	for _, candidate := range []byte{':', '=', ' ', '\t'} {
		if index := strings.IndexByte(text, candidate); index >= 0 && (separator < 0 || index < separator) {
			separator = index
		}
	}
	if separator < 0 {
		return Material{}, fmt.Errorf("material %q must contain a kind and path", text)
	}

	kind, err := parseMaterialKind(text[:separator])
	if err != nil {
		return Material{}, err
	}
	path, err := ParsePath(strings.TrimSpace(text[separator+1:]))
	if err != nil {
		return Material{}, err
	}
	return Material{Kind: kind, Path: path}, nil
}

// ParseMaterialKind normalizes a material kind.
func ParseMaterialKind(text string) (MaterialKind, error) {
	return parseMaterialKind(text)
}

func parseMaterialKind(text string) (MaterialKind, error) {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.NewReplacer("_", "-", " ", "-").Replace(normalized)
	switch normalized {
	case "xpub", "extended-public-key", "extended-public":
		return ExtendedPublicKey, nil
	case "xprv", "extended-private-key", "extended-private":
		return ExtendedPrivateKey, nil
	case "priv", "private", "private-key", "leaf-private-key", "leaf":
		return PrivateKey, nil
	default:
		return "", fmt.Errorf("unknown material kind %q", text)
	}
}

func (m Material) normalized() (Material, error) {
	kind, err := parseMaterialKind(string(m.Kind))
	if err != nil {
		return Material{}, err
	}
	m.Kind = kind
	m.Path = m.Path.Clone()
	return m, nil
}

// Validate checks the kind and path syntax.
func (m Material) Validate() error {
	_, err := m.normalized()
	return err
}

// ReadMaterials imports a JSON array of materials or an object with a
// "materials" array. Paths use the string form, for example:
//
//	[{"kind":"xpub","path":"m/44'/0'/0'"},{"kind":"priv","path":"m/44'/0'/0'/0/7"}]
func ReadMaterials(reader io.Reader) ([]Material, error) {
	if reader == nil {
		return nil, fmt.Errorf("nil material reader")
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty material document")
	}

	var materials []Material
	if data[0] == '[' {
		if err := json.Unmarshal(data, &materials); err != nil {
			return nil, fmt.Errorf("decode materials: %w", err)
		}
	} else {
		var document struct {
			Materials []Material `json:"materials"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("decode material document: %w", err)
		}
		materials = document.Materials
	}

	normalized := make([]Material, len(materials))
	for i, material := range materials {
		normalized[i], err = material.normalized()
		if err != nil {
			return nil, fmt.Errorf("material %d: %w", i, err)
		}
	}
	return normalized, nil
}

package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// Manifest is the immutable, ordered list of individual input identifiers that
// a training task is allowed to read. The digest is fixed when a task starts so
// a later task cannot silently swap its inputs and claim an old result.
type Manifest struct {
	inputs []string
}

// NewManifest builds a Manifest from the supplied individual input IDs.
// The IDs are copied, deduplicated and sorted so manifests containing the same
// set of inputs always produce the same digest regardless of caller ordering.
func NewManifest(inputs ...string) Manifest {
	seen := make(map[string]struct{}, len(inputs))
	unique := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input == "" {
			continue
		}
		if _, ok := seen[input]; ok {
			continue
		}
		seen[input] = struct{}{}
		unique = append(unique, input)
	}
	sort.Strings(unique)
	return Manifest{inputs: unique}
}

// Inputs returns a copy of the manifest's input identifiers.
func (m Manifest) Inputs() []string {
	out := make([]string, len(m.inputs))
	copy(out, m.inputs)
	return out
}

// Contains reports whether individual id is part of the manifest.
func (m Manifest) Contains(id string) bool {
	idx := sort.SearchStrings(m.inputs, id)
	return idx < len(m.inputs) && m.inputs[idx] == id
}

// Digest returns the canonical SHA-256 digest of the manifest contents.
// Each length-prefixed input ID is fed to the hash so IDs can never be
// confused through concatenation, and the digest changes whenever the set of
// inputs changes.
func (m Manifest) Digest() string {
	sum := sha256.New()
	var length [4]byte
	for _, input := range m.inputs {
		length[0] = byte(len(input) >> 24)
		length[1] = byte(len(input) >> 16)
		length[2] = byte(len(input) >> 8)
		length[3] = byte(len(input))
		sum.Write(length[:])
		sum.Write([]byte(input))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

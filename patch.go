package netsec

import (
	"encoding/json"
	"fmt"
)

// PatchApplier turns an expected-old metadata value and a serialized patch
// into the new metadata value.  Host verifies the resulting digest against
// NewMetadataDigest recorded in the transaction.
type PatchApplier interface {
	Apply(old, patch []byte) ([]byte, error)
}

// PatchApplierFunc adapts a function to PatchApplier.
type PatchApplierFunc func(old, patch []byte) ([]byte, error)

func (f PatchApplierFunc) Apply(old, patch []byte) ([]byte, error) {
	return f(old, patch)
}

// ReplacePatch treats the patch bytes as the complete new metadata value.
// This is the default and has no hidden parsing behavior.
type ReplacePatch struct{}

func (ReplacePatch) Apply(_ []byte, patch []byte) ([]byte, error) {
	return append([]byte(nil), patch...), nil
}

// JSONMergePatch implements RFC 7396-style JSON object merge.  A nil or empty
// patch leaves the old metadata unchanged.  Non-object patch values replace
// the old metadata entirely.
type JSONMergePatch struct{}

func (JSONMergePatch) Apply(old, patch []byte) ([]byte, error) {
	if len(patch) == 0 {
		return append([]byte(nil), old...), nil
	}
	var patchValue any
	if err := json.Unmarshal(patch, &patchValue); err != nil {
		return nil, fmt.Errorf("netsec: decode metadata patch: %w", err)
	}
	patchObject, ok := patchValue.(map[string]any)
	if !ok {
		return append([]byte(nil), patch...), nil
	}
	oldObject := make(map[string]any)
	if len(old) > 0 {
		if err := json.Unmarshal(old, &oldObject); err != nil {
			return nil, fmt.Errorf("netsec: decode old metadata: %w", err)
		}
	}
	mergeJSONObject(oldObject, patchObject)
	return json.Marshal(oldObject)
}

func mergeJSONObject(dst, patch map[string]any) {
	for key, value := range patch {
		if value == nil {
			delete(dst, key)
			continue
		}
		patchChild, patchIsObject := value.(map[string]any)
		if !patchIsObject {
			dst[key] = value
			continue
		}
		dstChild, _ := dst[key].(map[string]any)
		if dstChild == nil {
			dstChild = make(map[string]any)
		}
		mergeJSONObject(dstChild, patchChild)
		dst[key] = dstChild
	}
}

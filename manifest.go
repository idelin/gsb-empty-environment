package netsec

import (
	"encoding/json"
	"reflect"
)

// ManifestFile binds a location to the identity of the opened file.
type ManifestFile struct {
	Location string       `json:"location"`
	Size     int64        `json:"size"`
	Identity FileIdentity `json:"identity"`
}

// ManifestTensor binds one tensor ID to its frozen file, byte range, digest,
// dtype and shape.
type ManifestTensor struct {
	TensorID string       `json:"tensor_id"`
	Location string       `json:"location"`
	Offset   int64        `json:"offset"`
	Length   int64        `json:"length"`
	SHA256   Digest       `json:"sha256"`
	Dtype    DType        `json:"dtype"`
	Shape    []int64      `json:"shape"`
	Shared   bool         `json:"shared"`
	Identity FileIdentity `json:"file_identity"`
}

// WeightManifest is the frozen, self-contained description produced after all
// initializers have been opened and verified.
type WeightManifest struct {
	GraphDigest Digest           `json:"graph_sha256"`
	Files       []ManifestFile   `json:"files"`
	Tensors     []ManifestTensor `json:"tensors"`
}

// Digest returns a SHA-256 over the canonical JSON encoding of the manifest.
// Files and tensors are stored in deterministic order by Freeze.
func (m WeightManifest) Digest() Digest {
	encoded, err := json.Marshal(m)
	if err != nil {
		// The manifest contains only JSON-safe fixed types and slices.
		panic(err)
	}
	return SumDigest(encoded)
}

func cloneManifest(m WeightManifest) WeightManifest {
	out := WeightManifest{
		GraphDigest: m.GraphDigest,
		Files:       append([]ManifestFile(nil), m.Files...),
		Tensors:     append([]ManifestTensor(nil), m.Tensors...),
	}
	for i := range out.Tensors {
		out.Tensors[i].Shape = append([]int64(nil), out.Tensors[i].Shape...)
	}
	return out
}

func manifestsEqual(a, b WeightManifest) bool {
	return reflect.DeepEqual(a, b)
}

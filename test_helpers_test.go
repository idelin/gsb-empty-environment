package netsec_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"

	netsec "example.org/netsec/q0150"
)

const (
	baseDigestA = netsec.Digest("base-model-A-v1")
	baseDigestB = netsec.Digest("base-model-B-v1")
)

type hmacSigner struct {
	key []byte
}

func (s *hmacSigner) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func (s *hmacSigner) Verify(payload, signature []byte) error {
	if !hmac.Equal(s.sign(payload), signature) {
		return fmt.Errorf("bad HMAC")
	}
	return nil
}

type tensorStore struct {
	mu      sync.RWMutex
	tensors map[netsec.Digest]netsec.TensorHandle
}

func newTensorStore() *tensorStore {
	return &tensorStore{tensors: make(map[netsec.Digest]netsec.TensorHandle)}
}

func (s *tensorStore) OpenTensor(digest netsec.Digest) (netsec.TensorHandle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	handle, ok := s.tensors[digest]
	if !ok {
		return nil, fmt.Errorf("tensor %s not found", digest)
	}
	return handle, nil
}

func (s *tensorStore) add(data []byte, shape netsec.TensorShape, dtype netsec.DType) netsec.Digest {
	digest := netsec.DigestOf(data)
	s.set(digest, netsec.NewReadOnlyTensor(data, shape, dtype))
	return digest
}

func (s *tensorStore) set(digest netsec.Digest, handle netsec.TensorHandle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tensors[digest] = handle
}

func baseManifest(digest netsec.Digest) netsec.BaseModelManifest {
	return netsec.BaseModelManifest{
		ModelDigest: digest,
		Layers: []netsec.BaseLayer{
			{
				ID:           "attn.q",
				WeightDigest: "weight-q-v1",
				InputDim:     4,
				OutputDim:    3,
				DType:        netsec.DTypeFloat32,
			},
			{
				ID:               "safety.gate",
				WeightDigest:     "weight-gate-v1",
				InputDim:         2,
				OutputDim:        2,
				DType:            netsec.DTypeFloat32,
				SecurityCritical: true,
			},
		},
	}
}

func newManager(
	t *testing.T,
	store netsec.TensorProvider,
	verifier netsec.SignatureVerifier,
	policy netsec.SecurityPolicy,
	baseDigest netsec.Digest,
) *netsec.Manager {
	t.Helper()
	manager, err := netsec.New(netsec.Config{
		Base:                baseManifest(baseDigest),
		Verifier:            verifier,
		Tensors:             store,
		Policy:              policy,
		MaxRank:             4,
		AllowedDTypes:       []netsec.DType{netsec.DTypeFloat16, netsec.DTypeBFloat16, netsec.DTypeFloat32},
		MaxScaleNumerator:   100,
		MaxScaleDenominator: 100,
		MaxTensorBytes:      1 << 20,
		MaxManifestBytes:    1 << 20,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func target(
	t *testing.T,
	store *tensorStore,
	layerID string,
	rank, inputDim, outputDim int,
	dtype netsec.DType,
	scale netsec.Rational,
	a, b []byte,
) netsec.AdapterTarget {
	t.Helper()
	aDigest := store.add(a, netsec.TensorShape{Rows: rank, Cols: inputDim}, dtype)
	bDigest := store.add(b, netsec.TensorShape{Rows: outputDim, Cols: rank}, dtype)
	return netsec.AdapterTarget{
		LayerID: layerID,
		ADigest: aDigest,
		BDigest: bDigest,
		DType:   dtype,
		Scale:   scale,
		Rank:    rank,
	}
}

func adapter(
	t *testing.T,
	signer *hmacSigner,
	adapterDigest, baseDigest netsec.Digest,
	targets ...netsec.AdapterTarget,
) netsec.AdapterEnvelope {
	t.Helper()
	manifest := netsec.AdapterManifest{
		AdapterDigest:   adapterDigest,
		BaseModelDigest: baseDigest,
		Version:         "v1",
		Targets:         targets,
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return netsec.AdapterEnvelope{Manifest: payload, Signature: signer.sign(payload)}
}

func float32Bytes(values ...float32) []byte {
	data := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return data
}

func float16Bytes(values ...uint16) []byte {
	data := make([]byte, 2*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	return data
}

func mutateJSON(t *testing.T, payload []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	mutate(object)
	updated, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal mutated manifest: %v", err)
	}
	return updated
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func replaceOnce(t *testing.T, data, old, replacement []byte) []byte {
	t.Helper()
	if bytes.Count(data, old) != 1 {
		t.Fatalf("expected exactly one occurrence of %q", old)
	}
	return bytes.Replace(data, old, replacement, 1)
}

type descriptorHandle struct {
	netsec.TensorHandle
	descriptor netsec.TensorDescriptor
}

func (h descriptorHandle) Descriptor() netsec.TensorDescriptor {
	return h.descriptor
}

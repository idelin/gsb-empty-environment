package netsec_test

import (
	"errors"
	"math"
	"testing"

	netsec "example.org/netsec/q0150"
)

func TestRejectsSameNamedLayerFromDifferentBaseLayout(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)

	bad := target(
		t, store, "attn.q", 2, 4, 3, netsec.DTypeFloat32, netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4, 5, 6, 7, 8),
		float32Bytes(1, 2, 3, 4, 5, 6),
	)
	envelope := adapter(t, signer, "adapter-foreign", baseDigestB, bad)

	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrBaseDigestMismatch) {
		t.Fatalf("Load error = %v, want ErrBaseDigestMismatch", err)
	}
	if _, ok := manager.Current(); ok {
		t.Fatal("foreign adapter was published")
	}
}

func TestBaseDigestComparisonIsExact(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)

	bad := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32, netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4),
		float32Bytes(1, 2, 3),
	)
	envelope := adapter(t, signer, "adapter-case", netsec.Digest("BASE-MODEL-A-V1"), bad)
	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrBaseDigestMismatch) {
		t.Fatalf("Load error = %v, want exact digest mismatch", err)
	}
}

func TestRejectsSwappedFactors(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)

	valid := target(
		t, store, "attn.q", 2, 4, 3, netsec.DTypeFloat32, netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4, 5, 6, 7, 8),
		float32Bytes(1, 2, 3, 4, 5, 6),
	)
	swapped := valid
	swapped.ADigest, swapped.BDigest = valid.BDigest, valid.ADigest
	envelope := adapter(t, signer, "adapter-swapped", baseDigestA, swapped)

	_, err := manager.Load(envelope)
	if !errors.Is(err, netsec.ErrTensorLength) && !errors.Is(err, netsec.ErrShapeMismatch) {
		t.Fatalf("Load error = %v, want a factor shape/length rejection", err)
	}
}

func TestRankBounds(t *testing.T) {
	for _, rank := range []int{0, 5, -1} {
		t.Run("rank", func(t *testing.T) {
			store := newTensorStore()
			signer := &hmacSigner{key: []byte("test-key")}
			manager := newManager(t, store, signer, nil, baseDigestA)
			target := netsec.AdapterTarget{
				LayerID: "attn.q",
				ADigest: "a-digest",
				BDigest: "b-digest",
				DType:   netsec.DTypeFloat32,
				Scale:   netsec.Rational{Numerator: 1, Denominator: 1},
				Rank:    rank,
			}
			envelope := adapter(t, signer, "adapter-rank", baseDigestA, target)
			if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrRankOutOfRange) {
				t.Fatalf("rank %d error = %v, want ErrRankOutOfRange", rank, err)
			}
		})
	}
}

func TestScaleMustBeReducedAndBounded(t *testing.T) {
	tests := []struct {
		name  string
		scale netsec.Rational
		want  error
	}{
		{name: "zero denominator", scale: netsec.Rational{Numerator: 1, Denominator: 0}, want: netsec.ErrScaleOutOfRange},
		{name: "negative denominator", scale: netsec.Rational{Numerator: 1, Denominator: -2}, want: netsec.ErrScaleOutOfRange},
		{name: "unreduced", scale: netsec.Rational{Numerator: 2, Denominator: 4}, want: netsec.ErrScaleNotReduced},
		{name: "unreduced zero", scale: netsec.Rational{Numerator: 0, Denominator: 2}, want: netsec.ErrScaleNotReduced},
		{name: "numerator bound", scale: netsec.Rational{Numerator: 101, Denominator: 1}, want: netsec.ErrScaleOutOfRange},
		{name: "denominator bound", scale: netsec.Rational{Numerator: 1, Denominator: 101}, want: netsec.ErrScaleOutOfRange},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTensorStore()
			signer := &hmacSigner{key: []byte("test-key")}
			manager := newManager(t, store, signer, nil, baseDigestA)
			target := netsec.AdapterTarget{
				LayerID: "attn.q",
				ADigest: "a-digest",
				BDigest: "b-digest",
				DType:   netsec.DTypeFloat32,
				Scale:   test.scale,
				Rank:    1,
			}
			envelope := adapter(t, signer, "adapter-scale", baseDigestA, target)
			if _, err := manager.Load(envelope); !errors.Is(err, test.want) {
				t.Fatalf("Load error = %v, want %v", err, test.want)
			}
		})
	}

	t.Run("valid negative reduced scale", func(t *testing.T) {
		store := newTensorStore()
		signer := &hmacSigner{key: []byte("test-key")}
		manager := newManager(t, store, signer, nil, baseDigestA)
		target := target(
			t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
			netsec.Rational{Numerator: -3, Denominator: 4},
			float32Bytes(1, 2, 3, 4),
			float32Bytes(1, 2, 3),
		)
		envelope := adapter(t, signer, "adapter-scale-valid", baseDigestA, target)
		if _, err := manager.Load(envelope); err != nil {
			t.Fatalf("Load: %v", err)
		}
	})
}

func TestDTypeMustMatchByteLength(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)

	a := float32Bytes(1, 2, 3, 4, 5, 6, 7, 8)
	b := float16Bytes(1, 2, 3, 4, 5, 6)
	aDigest := store.add(a, netsec.TensorShape{Rows: 2, Cols: 4}, netsec.DTypeFloat16)
	bDigest := store.add(b, netsec.TensorShape{Rows: 3, Cols: 2}, netsec.DTypeFloat16)
	target := netsec.AdapterTarget{
		LayerID: "attn.q",
		ADigest: aDigest,
		BDigest: bDigest,
		DType:   netsec.DTypeFloat16,
		Scale:   netsec.Rational{Numerator: 1, Denominator: 1},
		Rank:    2,
	}
	envelope := adapter(t, signer, "adapter-length", baseDigestA, target)
	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrTensorLength) {
		t.Fatalf("Load error = %v, want ErrTensorLength", err)
	}
}

func TestDescriptorDTypeMismatch(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)

	a := float32Bytes(1, 2, 3, 4)
	b := float32Bytes(1, 2, 3)
	aDigest := store.add(a, netsec.TensorShape{Rows: 1, Cols: 4}, netsec.DTypeFloat32)
	bDigest := store.add(b, netsec.TensorShape{Rows: 3, Cols: 1}, netsec.DTypeFloat32)
	store.set(aDigest, descriptorHandle{
		TensorHandle: store.tensors[aDigest],
		descriptor: netsec.TensorDescriptor{
			Shape: netsec.TensorShape{Rows: 1, Cols: 4},
			DType: netsec.DTypeFloat16,
		},
	})
	target := netsec.AdapterTarget{
		LayerID: "attn.q",
		ADigest: aDigest,
		BDigest: bDigest,
		DType:   netsec.DTypeFloat32,
		Scale:   netsec.Rational{Numerator: 1, Denominator: 1},
		Rank:    1,
	}
	envelope := adapter(t, signer, "adapter-descriptor", baseDigestA, target)
	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrDTypeMismatch) {
		t.Fatalf("Load error = %v, want ErrDTypeMismatch", err)
	}
}

func TestRejectsNonFiniteFloat32Elements(t *testing.T) {
	tests := []struct {
		name  string
		value float32
	}{
		{name: "NaN", value: float32(math.NaN())},
		{name: "positive infinity", value: float32(math.Inf(1))},
		{name: "negative infinity", value: float32(math.Inf(-1))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTensorStore()
			signer := &hmacSigner{key: []byte("test-key")}
			manager := newManager(t, store, signer, nil, baseDigestA)
			aValues := []float32{1, 2, 3, 4, 5, 6, 7, 8}
			aValues[3] = test.value
			target := target(
				t, store, "attn.q", 2, 4, 3, netsec.DTypeFloat32,
				netsec.Rational{Numerator: 1, Denominator: 1},
				float32Bytes(aValues...),
				float32Bytes(1, 2, 3, 4, 5, 6),
			)
			envelope := adapter(t, signer, "adapter-nonfinite", baseDigestA, target)
			if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrNonFinite) {
				t.Fatalf("Load error = %v, want ErrNonFinite", err)
			}
		})
	}
}

func TestRejectsNonFiniteFloat16Elements(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)

	target := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat16,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float16Bytes(0x3c00, 0x7e00, 0x3c00, 0x3c00),
		float16Bytes(0x3c00, 0x3c00, 0x3c00),
	)
	envelope := adapter(t, signer, "adapter-nonfinite-f16", baseDigestA, target)
	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrNonFinite) {
		t.Fatalf("Load error = %v, want ErrNonFinite", err)
	}
}

func TestSignatureIsBoundToFullManifest(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)
	target := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4),
		float32Bytes(1, 2, 3),
	)
	envelope := adapter(t, signer, "adapter-signed", baseDigestA, target)

	rebound := envelope
	rebound.Manifest = mutateJSON(t, envelope.Manifest, func(object map[string]any) {
		object["baseModelDigest"] = string(baseDigestB)
	})
	if _, err := manager.Load(rebound); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("rebound base digest error = %v, want ErrInvalidSignature", err)
	}

	rebound.Manifest = mutateJSON(t, envelope.Manifest, func(object map[string]any) {
		object["adapterDigest"] = "adapter-other"
	})
	if _, err := manager.Load(rebound); !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("rebound adapter digest error = %v, want ErrInvalidSignature", err)
	}
}

func TestRejectsDuplicateJSONKeys(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)
	target := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4),
		float32Bytes(1, 2, 3),
	)
	envelope := adapter(t, signer, "adapter-duplicate-json", baseDigestA, target)
	envelope.Manifest = replaceOnce(
		t,
		envelope.Manifest,
		[]byte(`"baseModelDigest":"base-model-A-v1"`),
		[]byte(`"baseModelDigest":"base-model-A-v1","baseModelDigest":"base-model-B-v1"`),
	)
	envelope.Signature = signer.sign(envelope.Manifest)

	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrInvalidManifest) {
		t.Fatalf("Load error = %v, want ErrInvalidManifest", err)
	}
}

func TestSecurityCriticalLayerRequiresDigestSpecificPolicy(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	securityTarget := target(
		t, store, "safety.gate", 1, 2, 2, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2),
		float32Bytes(3, 4),
	)
	envelope := adapter(t, signer, "adapter-secure", baseDigestA, securityTarget)

	t.Run("denied by default", func(t *testing.T) {
		manager := newManager(t, store, signer, nil, baseDigestA)
		if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrSecurityCritical) {
			t.Fatalf("Load error = %v, want ErrSecurityCritical", err)
		}
	})

	t.Run("different digest is denied", func(t *testing.T) {
		policy := netsec.SecurityPolicySet{"adapter-other": {}}
		manager := newManager(t, store, signer, policy, baseDigestA)
		if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrSecurityCritical) {
			t.Fatalf("Load error = %v, want ErrSecurityCritical", err)
		}
	})

	t.Run("listed digest is allowed", func(t *testing.T) {
		policy := netsec.SecurityPolicySet{"adapter-secure": {}}
		manager := newManager(t, store, signer, policy, baseDigestA)
		generation, err := manager.Load(envelope)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if generation.LayerCount() != 1 {
			t.Fatalf("layer count = %d, want 1", generation.LayerCount())
		}
	})
}

func TestDuplicateAndUnknownTargetsAreRejected(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)
	valid := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4),
		float32Bytes(1, 2, 3),
	)

	envelope := adapter(t, signer, "adapter-duplicate-target", baseDigestA, valid, valid)
	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrDuplicateLayer) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateLayer", err)
	}

	unknown := valid
	unknown.LayerID = "missing.layer"
	envelope = adapter(t, signer, "adapter-unknown-target", baseDigestA, unknown)
	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrLayerNotFound) {
		t.Fatalf("unknown error = %v, want ErrLayerNotFound", err)
	}
}

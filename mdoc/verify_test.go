package mdoc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"hash"
	"testing"
	"time"
)

const (
	testNamespace  = "org.iso.18013-5.2.1"
	testNamespace2 = "org.example.foo"
	testDocType    = "org.iso.18013-5.2.1.mDL"
	testDigestAlg  = "SHA-256"
)

type fixture struct {
	key         *ecdsa.PrivateKey
	transcript1 []byte
	transcript2 []byte
	requested   RequestedItems
	now         time.Time
	validFrom   time.Time
	validUntil  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}
	st1, err := Encode([]any{"SessionTranscript", uint64(1)})
	if err != nil {
		t.Fatalf("encode transcript 1: %v", err)
	}
	st2, err := Encode([]any{"SessionTranscript", uint64(2)})
	if err != nil {
		t.Fatalf("encode transcript 2: %v", err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	return &fixture{
		key:         key,
		transcript1: st1,
		transcript2: st2,
		requested: NewRequestedItems(
			[2]string{testNamespace, "family_name"},
			[2]string{testNamespace, "birth_date"},
			[2]string{testNamespace2, "score"},
		),
		now:        now,
		validFrom:  now.Add(-180 * 24 * time.Hour),
		validUntil: now.Add(180 * 24 * time.Hour),
	}
}

func testHash(algorithm string, data []byte) ([]byte, error) {
	var h hash.Hash
	switch algorithm {
	case "SHA-256":
		h = sha256.New()
	case "SHA-384":
		h = sha512.New384()
	case "SHA-512":
		h = sha512.New()
	default:
		return nil, errors.New("unknown algorithm " + algorithm)
	}
	h.Write(data)
	return h.Sum(nil), nil
}

func (f *fixture) verifier() Verifier {
	pub := &f.key.PublicKey
	return Verifier{
		Hash: testHash,
		VerifyDeviceSignature: func(authBytes []byte, sig DeviceSignature) error {
			sigStructure := []any{
				"Signature1",
				sig.Protected,
				[]byte{},
				authBytes,
			}
			encoded, err := Encode(sigStructure)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(encoded)
			if !ecdsa.VerifyASN1(pub, sum[:], sig.Signature) {
				return errors.New("invalid ECDSA signature")
			}
			return nil
		},
		Now: func() time.Time { return f.now },
	}
}

func (f *fixture) sign(authBytes []byte) DeviceSignature {
	protected := []byte{}
	sigStructure := []any{"Signature1", protected, []byte{}, authBytes}
	encoded, err := Encode(sigStructure)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	signature, err := ecdsa.SignASN1(rand.Reader, f.key, sum[:])
	if err != nil {
		panic(err)
	}
	return DeviceSignature{
		Protected:   protected,
		Unprotected: map[int64]any{1: int64(-2)},
		Signature:   signature,
	}
}

func issuerItems() map[string][]IssuerSignedItem {
	return map[string][]IssuerSignedItem{
		testNamespace: {
			{
				DigestID:          0,
				Random:            []byte("0123456789abcdef"),
				ElementIdentifier: "family_name",
				ElementValue:      "Smith",
			},
			{
				DigestID:          1,
				Random:            []byte("fedcba9876543210"),
				ElementIdentifier: "birth_date",
				ElementValue:      "1990-01-01",
			},
		},
		testNamespace2: {
			{
				DigestID:          0,
				Random:            []byte("1111222233334444"),
				ElementIdentifier: "score",
				ElementValue:      int64(42),
			},
		},
	}
}

func buildMSO(t *testing.T, f *fixture, items map[string][]IssuerSignedItem, algorithm, docType string) MSO {
	t.Helper()
	digests := map[string]map[DigestID][]byte{}
	for namespace, namespaceItems := range items {
		digests[namespace] = map[DigestID][]byte{}
		for _, item := range namespaceItems {
			digest, err := issuerItemDigest(testHash, algorithm, item)
			if err != nil {
				t.Fatalf("build MSO digest: %v", err)
			}
			digests[namespace][item.DigestID] = digest
		}
	}
	return MSO{
		DigestAlgorithm: algorithm,
		ValueDigests:    digests,
		ValidityInfo: ValidityInfo{
			Signed:     f.validFrom,
			ValidFrom:  f.validFrom,
			ValidUntil: f.validUntil,
		},
		DocType: docType,
	}
}

func buildResponse(t *testing.T, f *fixture, transcript []byte) *Response {
	t.Helper()
	items := issuerItems()
	response := &Response{
		DocType: testDocType,
		IssuerSigned: IssuerSigned{
			MSO:        buildMSO(t, f, items, testDigestAlg, testDocType),
			NameSpaces: items,
		},
		DeviceSigned: DeviceSigned{
			DeviceNameSpaces: map[string][]DeviceSignedItem{
				testNamespace: {
					{ElementIdentifier: "device_engaged", ElementValue: true},
				},
			},
		},
	}
	authBytes := mustDeviceAuthBytes(t, response, transcript)
	response.DeviceSignature = f.sign(authBytes)
	return response
}

func mustDeviceAuthBytes(t *testing.T, response *Response, transcript []byte) []byte {
	t.Helper()
	nameSpacesPlain, err := encodeDeviceNameSpaces(response.DeviceNameSpaces)
	if err != nil {
		t.Fatalf("encode device name spaces: %v", err)
	}
	nameSpacesTagged, err := Encode(Tag{ID: 24, Content: nameSpacesPlain})
	if err != nil {
		t.Fatalf("tag device name spaces: %v", err)
	}
	authBytes, err := Encode([]any{
		"DeviceAuthentication",
		RawCBOR(transcript),
		response.DocType,
		RawCBOR(nameSpacesTagged),
	})
	if err != nil {
		t.Fatalf("encode DeviceAuthentication: %v", err)
	}
	return authBytes
}

func cloneResponse(in *Response) *Response {
	out := *in
	out.NameSpaces = map[string][]IssuerSignedItem{}
	for namespace, items := range in.NameSpaces {
		copied := make([]IssuerSignedItem, len(items))
		copy(copied, items)
		out.NameSpaces[namespace] = copied
	}
	digestsCopy := map[string]map[DigestID][]byte{}
	for namespace, digests := range in.MSO.ValueDigests {
		digestsCopy[namespace] = map[DigestID][]byte{}
		for id, digest := range digests {
			dup := make([]byte, len(digest))
			copy(dup, digest)
			digestsCopy[namespace][id] = dup
		}
	}
	out.MSO.ValueDigests = digestsCopy
	out.DeviceNameSpaces = map[string][]DeviceSignedItem{}
	for namespace, items := range in.DeviceNameSpaces {
		copied := make([]DeviceSignedItem, len(items))
		copy(copied, items)
		out.DeviceNameSpaces[namespace] = copied
	}
	return &out
}

func TestVerifyScenarios(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name       string
		transcript func(f *fixture) []byte
		mutate     func(t *testing.T, f *fixture, response *Response)
		want       error
	}{
		{
			name:       "valid response",
			transcript: func(f *fixture) []byte { return f.transcript1 },
		},
		{
			name:       "relayed response from another verifier session",
			transcript: func(f *fixture) []byte { return f.transcript2 },
			want:       ErrDeviceAuthentication,
		},
		{
			name:       "injected item without MSO digest",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				response.NameSpaces[testNamespace] = append(
					response.NameSpaces[testNamespace],
					IssuerSignedItem{
						DigestID:          99,
						Random:            []byte("abcdefghijklmnop"),
						ElementIdentifier: "family_name",
						ElementValue:      "Attacker",
					},
				)
			},
			want: ErrDigestNotFound,
		},
		{
			name:       "injected item with forged digest entry",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				forged := IssuerSignedItem{
					DigestID:          7,
					Random:            []byte("abcdefghijklmnop"),
					ElementIdentifier: "family_name",
					ElementValue:      "Attacker",
				}
				response.NameSpaces[testNamespace] = append(
					response.NameSpaces[testNamespace], forged)
				response.MSO.ValueDigests[testNamespace][7] = []byte("never-the-right-digest-value!!")
			},
			want: ErrDigestMismatch,
		},
		{
			name:       "tampered element value breaks digest",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				response.NameSpaces[testNamespace][0].ElementValue = "Tampered"
			},
			want: ErrDigestMismatch,
		},
		{
			name:       "returned item outside request set",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				extra := IssuerSignedItem{
					DigestID:          5,
					Random:            []byte("abcdefghijklmnop"),
					ElementIdentifier: "portrait_capture_date",
					ElementValue:      "2026-01-01",
				}
				response.NameSpaces[testNamespace] = append(
					response.NameSpaces[testNamespace], extra)
				digest, err := issuerItemDigest(testHash, testDigestAlg, extra)
				if err != nil {
					t.Fatal(err)
				}
				response.MSO.ValueDigests[testNamespace][5] = digest
			},
			want: ErrNotRequested,
		},
		{
			name:       "expired MSO after validUntil",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				f.now = f.validUntil.Add(time.Second)
			},
			want: ErrExpired,
		},
		{
			name:       "not yet valid MSO before validFrom",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				f.now = f.validFrom.Add(-time.Second)
			},
			want: ErrExpired,
		},
		{
			name:       "duplicate digestID within namespace",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				dup := response.NameSpaces[testNamespace][0]
				dup.ElementIdentifier = "birth_date"
				response.NameSpaces[testNamespace] = append(
					response.NameSpaces[testNamespace], dup)
			},
			want: ErrDuplicateDigestID,
		},
		{
			name:       "random shorter than 16 bytes",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				response.NameSpaces[testNamespace][0].Random = []byte("short")
			},
			want: ErrShortRandom,
		},
		{
			name:       "DocType mismatch between response and MSO",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				response.MSO.DocType = "org.example.other"
			},
			want: ErrDocTypeMismatch,
		},
		{
			name:       "unsupported digest algorithm",
			transcript: func(f *fixture) []byte { return f.transcript1 },
			mutate: func(t *testing.T, f *fixture, response *Response) {
				response.MSO.DigestAlgorithm = "SHA-1024"
			},
			want: ErrHashCallback,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local := *f
			response := buildResponse(t, &local, local.transcript1)
			if tc.mutate != nil {
				tc.mutate(t, &local, response)
			}
			err := local.verifier().Verify(response, tc.transcript(&local), local.requested)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Verify returned unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verify error = %v, want errors.Is(%v)", err, tc.want)
			}
		})
	}
}

func TestVerifyBoundariesAndInputs(t *testing.T) {
	f := newFixture(t)

	t.Run("validity window boundaries are inclusive", func(t *testing.T) {
		local := *f
		response := buildResponse(t, &local, local.transcript1)
		local.now = local.validFrom
		if err := local.verifier().Verify(response, local.transcript1, local.requested); err != nil {
			t.Fatalf("validFrom boundary rejected: %v", err)
		}

		local.now = local.validUntil
		response = buildResponse(t, &local, local.transcript1)
		if err := local.verifier().Verify(response, local.transcript1, local.requested); err != nil {
			t.Fatalf("validUntil boundary rejected: %v", err)
		}
	})

	t.Run("nil response", func(t *testing.T) {
		err := f.verifier().Verify(nil, f.transcript1, f.requested)
		if !errors.Is(err, ErrEmptyResponse) {
			t.Fatalf("err = %v, want ErrEmptyResponse", err)
		}
	})

	t.Run("missing session transcript", func(t *testing.T) {
		response := buildResponse(t, f, f.transcript1)
		err := f.verifier().Verify(response, nil, f.requested)
		if !errors.Is(err, ErrInvalidSessionTranscript) {
			t.Fatalf("err = %v, want ErrInvalidSessionTranscript", err)
		}
	})

	t.Run("malformed session transcript", func(t *testing.T) {
		response := buildResponse(t, f, f.transcript1)
		err := f.verifier().Verify(response, []byte{0x1f}, f.requested)
		if !errors.Is(err, ErrInvalidSessionTranscript) {
			t.Fatalf("err = %v, want ErrInvalidSessionTranscript", err)
		}
	})

	t.Run("unconfigured callbacks", func(t *testing.T) {
		response := buildResponse(t, f, f.transcript1)
		if err := (Verifier{}).Verify(response, f.transcript1, f.requested); err == nil {
			t.Fatal("expected error for unconfigured verifier")
		}
	})

	t.Run("device signature over wrong payload rejected", func(t *testing.T) {
		response := buildResponse(t, f, f.transcript1)
		response.DeviceSignature = f.sign([]byte("forged-payload"))
		err := f.verifier().Verify(response, f.transcript1, f.requested)
		if !errors.Is(err, ErrDeviceAuthentication) {
			t.Fatalf("err = %v, want ErrDeviceAuthentication", err)
		}
	})

	t.Run("unrequested namespace rejected", func(t *testing.T) {
		local := *f
		response := buildResponse(t, &local, local.transcript1)
		response.NameSpaces["org.iso.secret"] = []IssuerSignedItem{{
			DigestID:          0,
			Random:            []byte("abcdefghijklmnop"),
			ElementIdentifier: "secret",
			ElementValue:      "value",
		}}
		err := local.verifier().Verify(response, local.transcript1, local.requested)
		if !errors.Is(err, ErrNotRequested) {
			t.Fatalf("err = %v, want ErrNotRequested", err)
		}
	})

	t.Run("same digestID in different namespaces allowed", func(t *testing.T) {
		response := buildResponse(t, f, f.transcript1)
		if err := f.verifier().Verify(response, f.transcript1, f.requested); err != nil {
			t.Fatalf("cross-namespace digestID reuse rejected: %v", err)
		}
	})

	t.Run("SHA-384 digest algorithm supported through callback", func(t *testing.T) {
		local := *f
		items := issuerItems()
		response := &Response{
			DocType: testDocType,
			IssuerSigned: IssuerSigned{
				MSO:        buildMSO(t, &local, items, "SHA-384", testDocType),
				NameSpaces: items,
			},
			DeviceSigned: DeviceSigned{
				DeviceNameSpaces: map[string][]DeviceSignedItem{
					testNamespace: {{ElementIdentifier: "x", ElementValue: int64(1)}},
				},
			},
		}
		response.DeviceSignature = local.sign(mustDeviceAuthBytes(t, response, local.transcript1))
		if err := local.verifier().Verify(response, local.transcript1, local.requested); err != nil {
			t.Fatalf("SHA-384 response rejected: %v", err)
		}
	})
}

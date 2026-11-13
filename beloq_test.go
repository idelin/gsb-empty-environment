package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"sync"
	"testing"
)

func testHashToGroup(input []byte) (*ecdsa.PublicKey, error) {
	h := sha256.New()
	h.Write([]byte("test host HashToGroup"))
	var size [4]byte
	size[0] = byte(len(input) >> 24)
	size[1] = byte(len(input) >> 16)
	size[2] = byte(len(input) >> 8)
	size[3] = byte(len(input))
	h.Write(size[:])
	h.Write(input)
	digest := h.Sum(nil)

	scalar := new(big.Int).SetBytes(digest)
	scalar.Mod(scalar, p256Params.N)
	if scalar.Sign() == 0 {
		scalar.SetInt64(1)
	}
	x, y := p256.ScalarBaseMult(scalar.Bytes())
	return pointFromCoordinates(x, y)
}

func newTestParty(t *testing.T) (*Server, *Client, *ecdsa.PrivateKey) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(p256, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(privateKey, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(testHashToGroup, rand.Reader, server.Key())
	if err != nil {
		t.Fatal(err)
	}
	return server, client, privateKey
}

func issue(t *testing.T, client *Client, server *Server, input, context []byte) (*BlindRequest, *ClientState, *BlindResponse) {
	t.Helper()
	request, state, err := client.Blind(input, context)
	if err != nil {
		t.Fatalf("Blind: %v", err)
	}
	response, err := server.Evaluate(request, context)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return request, state, response
}

func cloneResponse(response *BlindResponse) *BlindResponse {
	if response == nil {
		return nil
	}
	return &BlindResponse{
		E: clonePoint(response.E),
		DLEQProof: DLEQProof{
			C: new(big.Int).Set(response.C),
			S: new(big.Int).Set(response.S),
		},
	}
}

func referenceDigest(t *testing.T, context, input []byte, privateKey *ecdsa.PrivateKey) []byte {
	t.Helper()
	point, err := testHashToGroup(input)
	if err != nil {
		t.Fatalf("HashToGroup: %v", err)
	}
	z := scalarMult(point, privateKey.D)
	if z == nil {
		t.Fatal("unblinded point is infinity")
	}
	encodedZ, err := encodePoint(z)
	if err != nil {
		t.Fatalf("encode Z: %v", err)
	}
	h := sha256.New()
	h.Write([]byte(CompletionLabel))
	h.Write(context)
	h.Write(input)
	h.Write(encodedZ)
	return h.Sum(nil)
}

func TestRealServerClientRoundTrip(t *testing.T) {
	server, client, privateKey := newTestParty(t)
	input := []byte("alice@example.test:record-42")
	context := []byte("deployment=prod;query-epoch=7")

	request, state, response := issue(t, client, server, input, context)
	if request == nil || state == nil || response == nil {
		t.Fatal("missing request, state, or response")
	}
	if err := VerifyDLEQ(server.Key(), request.B, response.E, response.C, response.S, context); err != nil {
		t.Fatalf("server proof does not verify: %v", err)
	}

	got, err := state.Complete(response)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := referenceDigest(t, context, input, privateKey)
	if !bytes.Equal(got, want) {
		t.Fatalf("completion digest mismatch\n got %x\nwant %x", got, want)
	}

	if _, err := state.Complete(response); !errors.Is(err, ErrStateConsumed) {
		t.Fatalf("second response: got %v, want ErrStateConsumed", err)
	}
}

func TestDLEQChallengeTranscript(t *testing.T) {
	points := []*ecdsa.PublicKey{
		baseMult(big.NewInt(1)),
		baseMult(big.NewInt(2)),
		baseMult(big.NewInt(3)),
		baseMult(big.NewInt(4)),
		baseMult(big.NewInt(5)),
	}
	context := []byte("host context")
	got, err := ComputeDLEQChallenge(context, points[0], points[1], points[2], points[3], points[4])
	if err != nil {
		t.Fatal(err)
	}

	h := sha256.New()
	writeReferenceField := func(field []byte) {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		h.Write(size[:])
		h.Write(field)
	}
	writeReferenceField([]byte(DLEQLabel))
	writeReferenceField(context)
	for _, point := range points {
		encoded := elliptic.MarshalCompressed(p256, point.X, point.Y)
		writeReferenceField(encoded)
	}
	want := new(big.Int).Mod(new(big.Int).SetBytes(h.Sum(nil)), p256Q)
	if got.Cmp(want) != 0 {
		t.Fatalf("challenge mismatch\n got %s\nwant %s", got, want)
	}

	other, err := ComputeDLEQChallenge([]byte("other context"), points[0], points[1], points[2], points[3], points[4])
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(other) == 0 {
		t.Fatal("changing the context did not change the challenge")
	}
}

func TestScalarAndContextHasherConstructors(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(p256, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(privateKey.D, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var sawInput, sawContext []byte
	hasher := func(input, context []byte) (*ecdsa.PublicKey, error) {
		sawInput = append([]byte(nil), input...)
		sawContext = append([]byte(nil), context...)
		return testHashToGroup(input)
	}
	client, err := NewClient(hasher, rand.Reader, server.Key())
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("input")
	context := []byte("context")
	request, state, response := issue(t, client, server, input, context)
	if !bytes.Equal(sawInput, input) || !bytes.Equal(sawContext, context) {
		t.Fatal("two-argument HashToGroup did not receive input and context")
	}
	if _, err := state.Complete(response); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := VerifyDLEQ(server.Key(), request.B, response.E, response.C, response.S, context); err != nil {
		t.Fatalf("proof verification failed: %v", err)
	}
}

func TestServerRejectsNonP256PrivateKey(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(privateKey, rand.Reader); err == nil {
		t.Fatal("P-384 private key was accepted")
	}
	for _, scalar := range []*big.Int{big.NewInt(0), new(big.Int).Set(p256Q)} {
		if _, err := NewServerFromScalar(scalar, rand.Reader); err == nil {
			t.Fatalf("invalid private scalar %s was accepted", scalar)
		}
	}
}

func TestCopiedStateSharesConsumption(t *testing.T) {
	server, client, _ := newTestParty(t)
	_, state, response := issue(t, client, server, []byte("input"), []byte("ctx"))
	copied := *state

	if _, err := state.Complete(response); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := copied.Complete(response); !errors.Is(err, ErrStateConsumed) {
		t.Fatalf("copied state: got %v, want ErrStateConsumed", err)
	}
}

func TestConcurrentStateUseIsSingleShot(t *testing.T) {
	server, client, _ := newTestParty(t)
	_, state, response := issue(t, client, server, []byte("input"), []byte("ctx"))

	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := state.Complete(response)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrStateConsumed):
		default:
			t.Fatalf("unexpected completion error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful completions, want 1", successes)
	}
}

func TestProofAndRequestBindingFailures(t *testing.T) {
	server, client, _ := newTestParty(t)
	context1 := []byte("context-1")
	context2 := []byte("context-2")

	t.Run("entire response swapped between requests", func(t *testing.T) {
		req1, state1, resp1 := issue(t, client, server, []byte("input-1"), context1)
		_, _, resp2 := issue(t, client, server, []byte("input-2"), context1)
		if _, err := state1.Complete(resp2); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
		if _, err := state1.Complete(resp1); !errors.Is(err, ErrStateConsumed) {
			t.Fatalf("state was not consumed on failure: %v", err)
		}
		_ = req1
	})

	t.Run("evaluation point swapped", func(t *testing.T) {
		_, state1, resp1 := issue(t, client, server, []byte("input-1"), context1)
		_, _, resp2 := issue(t, client, server, []byte("input-2"), context1)
		swapped := cloneResponse(resp1)
		swapped.E = clonePoint(resp2.E)
		if _, err := state1.Complete(swapped); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
	})

	t.Run("DLEQ proof swapped", func(t *testing.T) {
		_, state1, resp1 := issue(t, client, server, []byte("input-1"), context1)
		_, _, resp2 := issue(t, client, server, []byte("input-2"), context1)
		swapped := cloneResponse(resp1)
		swapped.C = new(big.Int).Set(resp2.C)
		swapped.S = new(big.Int).Set(resp2.S)
		if _, err := state1.Complete(swapped); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
	})

	t.Run("context changed", func(t *testing.T) {
		request, state, err := client.Blind([]byte("input"), context1)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Evaluate(request, context2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Complete(response); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
	})

	t.Run("server public key swapped", func(t *testing.T) {
		otherServer, _, _ := newTestParty(t)
		request, state, err := client.Blind([]byte("input"), context1)
		if err != nil {
			t.Fatal(err)
		}
		response, err := otherServer.Evaluate(request, context1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Complete(response); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
	})

	t.Run("client trusted public key swapped", func(t *testing.T) {
		otherServer, _, _ := newTestParty(t)
		wrongClient, err := NewClient(testHashToGroup, rand.Reader, otherServer.Key())
		if err != nil {
			t.Fatal(err)
		}
		request, state, err := wrongClient.Blind([]byte("input"), context1)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Evaluate(request, context1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Complete(response); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
	})

	t.Run("blind point changed on request", func(t *testing.T) {
		_, state1, _ := issue(t, client, server, []byte("input-1"), context1)
		request2, _, _ := issue(t, client, server, []byte("input-2"), context1)
		mutated := &BlindRequest{B: clonePoint(request2.B)}
		response, err := server.Evaluate(mutated, context1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state1.Complete(response); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
	})

	t.Run("blind point swapped from another request", func(t *testing.T) {
		_, state1, _ := issue(t, client, server, []byte("input-1"), context1)
		_, _, resp2 := issue(t, client, server, []byte("input-2"), context1)
		if _, err := state1.Complete(resp2); !errors.Is(err, ErrProofRejected) {
			t.Fatalf("got %v, want ErrProofRejected", err)
		}
	})
}

func TestCanonicalPointsAndScalars(t *testing.T) {
	server, client, _ := newTestParty(t)
	context := []byte("ctx")
	request, state, good := issue(t, client, server, []byte("input"), context)

	offCurve := &ecdsa.PublicKey{Curve: p256, X: big.NewInt(1), Y: big.NewInt(1)}
	infinity := &ecdsa.PublicKey{Curve: p256}
	if _, err := server.Evaluate(&BlindRequest{B: nil}, context); err == nil {
		t.Fatal("nil blind point was accepted")
	}
	if _, err := server.Evaluate(&BlindRequest{B: infinity}, context); err == nil {
		t.Fatal("infinity blind point was accepted")
	}
	if _, err := server.Evaluate(&BlindRequest{B: offCurve}, context); err == nil {
		t.Fatal("off-curve blind point was accepted")
	}

	for _, tc := range []struct {
		name  string
		proof DLEQProof
	}{
		{name: "c equals q", proof: DLEQProof{C: new(big.Int).Set(p256Q), S: good.S}},
		{name: "s equals q", proof: DLEQProof{C: good.C, S: new(big.Int).Set(p256Q)}},
		{name: "negative c", proof: DLEQProof{C: big.NewInt(-1), S: good.S}},
		{name: "nil s", proof: DLEQProof{C: good.C, S: nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Re-mint because every response consumes a state, including bad
			// proof encodings.
			_, freshState, freshResponse := issue(t, client, server, []byte("input"), context)
			candidate := cloneResponse(freshResponse)
			candidate.DLEQProof = tc.proof
			if _, err := freshState.Complete(candidate); !errors.Is(err, ErrProofRejected) {
				t.Fatalf("got %v, want ErrProofRejected", err)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		point *ecdsa.PublicKey
	}{
		{name: "nil E", point: nil},
		{name: "infinity E", point: &ecdsa.PublicKey{Curve: p256}},
		{name: "off-curve E", point: offCurve},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, freshState, freshResponse := issue(t, client, server, []byte("input"), context)
			candidate := cloneResponse(freshResponse)
			candidate.E = clonePoint(tc.point)
			if _, err := freshState.Complete(candidate); !errors.Is(err, ErrProofRejected) {
				t.Fatalf("got %v, want ErrProofRejected", err)
			}
		})
	}

	if _, err := state.Complete(nil); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("nil response: got %v, want ErrProofRejected", err)
	}
	if _, err := state.Complete(good); !errors.Is(err, ErrStateConsumed) {
		t.Fatalf("state was not consumed after invalid response: %v", err)
	}
	if err := VerifyDLEQ(server.Key(), request.B, good.E, good.C, good.S, context); err != nil {
		t.Fatalf("valid proof was rejected: %v", err)
	}
}

func TestHostBoundaryFailuresDoNotLeakState(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(p256, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(privateKey, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	failing := &failingReader{err: io.ErrUnexpectedEOF}
	client, err := NewClient(testHashToGroup, failing, server.Key())
	if err != nil {
		t.Fatal(err)
	}
	request, state, err := client.Blind([]byte("input"), []byte("ctx"))
	if !errors.Is(err, ErrRandomness) {
		t.Fatalf("got %v, want ErrRandomness", err)
	}
	if request != nil || state != nil {
		t.Fatal("random-source failure returned a request or usable state")
	}

	zeroClient, err := NewClient(testHashToGroup, zeroReader{}, server.Key())
	if err != nil {
		t.Fatal(err)
	}
	request, state, err = zeroClient.Blind([]byte("input"), []byte("ctx"))
	if !errors.Is(err, ErrRandomness) {
		t.Fatalf("zero-progress reader: got %v, want ErrRandomness", err)
	}
	if request != nil || state != nil {
		t.Fatal("zero-progress random source returned a request or usable state")
	}

	for _, invalidHash := range []struct {
		name string
		fn   HashToGroupFunc
	}{
		{name: "nil point", fn: func([]byte) (*ecdsa.PublicKey, error) { return nil, nil }},
		{name: "infinity", fn: func([]byte) (*ecdsa.PublicKey, error) { return &ecdsa.PublicKey{Curve: p256}, nil }},
		{name: "off curve", fn: func([]byte) (*ecdsa.PublicKey, error) {
			return &ecdsa.PublicKey{Curve: p256, X: big.NewInt(1), Y: big.NewInt(1)}, nil
		}},
	} {
		t.Run("HashToGroup "+invalidHash.name, func(t *testing.T) {
			badClient, err := NewClient(invalidHash.fn, rand.Reader, server.Key())
			if err != nil {
				t.Fatal(err)
			}
			request, state, err := badClient.Blind([]byte("input"), []byte("ctx"))
			if err == nil {
				t.Fatal("invalid HashToGroup result was accepted")
			}
			if request != nil || state != nil {
				t.Fatal("invalid HashToGroup result returned a request or state")
			}
		})
	}

	failingServer, err := NewServer(privateKey, failing)
	if err != nil {
		t.Fatal(err)
	}
	goodClient, err := NewClient(testHashToGroup, rand.Reader, server.Key())
	if err != nil {
		t.Fatal(err)
	}
	goodRequest, _, err := goodClient.Blind([]byte("input"), []byte("ctx"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := failingServer.Evaluate(goodRequest, []byte("ctx"))
	if !errors.Is(err, ErrRandomness) {
		t.Fatalf("got %v, want ErrRandomness", err)
	}
	if response != nil {
		t.Fatal("server returned an evaluation after random-source failure")
	}
}

func TestServerDerivesPublicKeyFromPrivateScalar(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(p256, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := ecdsa.GenerateKey(p256, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claiming := &ecdsa.PrivateKey{
		D:         new(big.Int).Set(privateKey.D),
		PublicKey: otherKey.PublicKey,
	}
	server, err := NewServer(claiming, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	want := baseMult(privateKey.D)
	if server.Key() == nil || server.Key().X.Cmp(want.X) != 0 || server.Key().Y.Cmp(want.Y) != 0 {
		t.Fatal("server trusted the supplied public-key field instead of deriving K=kG")
	}
	if server.PublicKey == nil || server.PublicKey.X.Cmp(want.X) != 0 {
		t.Fatal("public key snapshot is not K=kG")
	}

	client, err := NewClient(testHashToGroup, rand.Reader, want)
	if err != nil {
		t.Fatal(err)
	}
	request, state, response := issue(t, client, server, []byte("input"), []byte("ctx"))
	if _, err := state.Complete(response); err != nil {
		t.Fatalf("completion failed: %v", err)
	}
	if err := VerifyDLEQ(want, request.B, response.E, response.C, response.S, []byte("ctx")); err != nil {
		t.Fatalf("proof against derived K failed: %v", err)
	}
}

func TestMutatingPublicKeySnapshotDoesNotAffectServer(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(p256, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(privateKey, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server.PublicKey.X.SetInt64(1)
	server.PublicKey.Y.SetInt64(1)

	client, err := NewClient(testHashToGroup, rand.Reader, baseMult(privateKey.D))
	if err != nil {
		t.Fatal(err)
	}
	request, state, response := issue(t, client, server, []byte("input"), []byte("ctx"))
	if _, err := state.Complete(response); err != nil {
		t.Fatalf("snapshot mutation affected evaluation: %v", err)
	}
	_ = request
}

type failingReader struct {
	err error
}

func (r *failingReader) Read([]byte) (int, error) {
	return 0, r.err
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) {
	return 0, nil
}

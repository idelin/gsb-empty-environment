package netsec_test

import (
	"errors"
	"sync"
	"testing"

	"example.org/netsec/q0062"
)

func TestDigestAndContextBinding(t *testing.T) {
	reg, suiteID, _, _ := newTestRegistry(t, 8, 8)
	ct1 := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	ct2 := []byte{9, 10, 11, 12, 13, 14, 15, 16}
	base := baseRequest(suiteID, ct1, ct2)
	baseKey := mustDecapsulate(t, reg, mustEncode(t, base))

	mutate := func(change func(*netsec.Request)) [32]byte {
		req := base
		change(&req)
		return mustDecapsulate(t, reg, mustEncode(t, req))
	}

	if got := mutate(func(r *netsec.Request) { r.ClientPublicKeyHash[0] ^= 1 }); got == baseKey {
		t.Fatal("client public key digest not bound")
	}
	if got := mutate(func(r *netsec.Request) { r.ServerPublicKeyHash[0] ^= 1 }); got == baseKey {
		t.Fatal("server public key digest not bound")
	}
	if got := mutate(func(r *netsec.Request) { r.HandshakeContextHash[0] ^= 1 }); got == baseKey {
		t.Fatal("handshake context digest not bound")
	}
}

func TestSuiteIDBinding(t *testing.T) {
	reg := netsec.NewRegistry()
	var id1, id2 [8]byte
	id1[0], id2[0] = 0x01, 0x02
	ct1 := []byte{1, 2, 3, 4}
	ct2 := []byte{5, 6, 7, 8}

	mkAdapter := func(id byte) *recordingAdapter {
		return &recordingAdapter{canonicalLen: 4, componentID: id}
	}
	for _, id := range [][8]byte{id1, id2} {
		if err := reg.RegisterSuite(id, netsec.SuiteConfig{
			Adapter1:       mkAdapter(0x01),
			Adapter2:       mkAdapter(0x02),
			Ciphertext1Len: 4,
			Ciphertext2Len: 4,
		}); err != nil {
			t.Fatalf("register %x: %v", id, err)
		}
	}

	key1 := mustDecapsulate(t, reg, mustEncode(t, baseRequest(id1, ct1, ct2)))
	key2 := mustDecapsulate(t, reg, mustEncode(t, baseRequest(id2, ct1, ct2)))
	if key1 == key2 {
		t.Fatal("suite ID is not bound into the derived key")
	}
}

func TestFailuresAreUniformAndDoNotRevealComponent(t *testing.T) {
	reg, suiteID, a1, a2 := newTestRegistry(t, 8, 8)
	goodCT1 := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	goodCT2 := []byte{9, 10, 11, 12, 13, 14, 15, 16}
	badCT1 := []byte{0, 2, 3, 4, 5, 6, 7, 8}
	badCT2 := []byte{0, 10, 11, 12, 13, 14, 15, 16}

	validKey := mustDecapsulate(t, reg, mustEncode(t, baseRequest(suiteID, goodCT1, goodCT2)))

	callsBefore := a1.Calls() + a2.Calls()
	singleWire := mustEncode(t, baseRequest(suiteID, badCT1, goodCT2))
	doubleWire := mustEncode(t, baseRequest(suiteID, badCT1, badCT2))

	keySingle, errSingle := reg.Decapsulate(singleWire)
	keyDouble, errDouble := reg.Decapsulate(doubleWire)
	if !errors.Is(errSingle, netsec.ErrDecapsulationFailed) ||
		!errors.Is(errDouble, netsec.ErrDecapsulationFailed) {
		t.Fatalf("want generic failure, got %v / %v", errSingle, errDouble)
	}
	// The external error must be identical regardless of which component
	// failed.
	if errSingle.Error() != errDouble.Error() {
		t.Fatalf("failure errors differ: %q vs %q", errSingle, errDouble)
	}

	// Both adapters always run, even on failure: there is no early return.
	if a1.Calls()+a2.Calls() != callsBefore+4 {
		t.Fatalf("expected both adapters invoked per failure, got a1=%d a2=%d",
			a1.Calls(), a2.Calls())
	}

	// Failure keys are 32 bytes, random-looking, and never equal to the real
	// derived key.
	var zero [32]byte
	for _, key := range [][32]byte{keySingle, keyDouble} {
		if key == zero {
			t.Fatal("failure key is all zeros")
		}
		if key == validKey {
			t.Fatal("failure leaked the real derived key")
		}
	}
	if keySingle == keyDouble {
		t.Fatal("failure keys are not independently randomized")
	}

	// Repeated single-side failures produce fresh random keys, so callers
	// cannot correlate failure causes from the returned bytes.
	keyAgain, errAgain := reg.Decapsulate(singleWire)
	if !errors.Is(errAgain, netsec.ErrDecapsulationFailed) || keyAgain == keySingle {
		t.Fatalf("repeated failure must return a fresh uniform key, got err=%v", errAgain)
	}

	// The failure key length always matches the success key length.
	if len(keySingle[:]) != len(validKey[:]) {
		t.Fatal("failure key length differs from success key length")
	}
}

func TestMalformedInputRejectedBeforeAdapters(t *testing.T) {
	reg, suiteID, a1, a2 := newTestRegistry(t, 8, 8)
	good := baseRequest(suiteID,
		[]byte{1, 2, 3, 4, 5, 6, 7, 8},
		[]byte{9, 10, 11, 12, 13, 14, 15, 16})
	wire := mustEncode(t, good)

	reject := func(name string, bad []byte, want error) {
		t.Helper()
		before1, before2 := a1.Calls(), a2.Calls()
		if _, err := reg.Decapsulate(bad); !errors.Is(err, want) {
			t.Fatalf("%s: got %v, want %v", name, err, want)
		}
		if a1.Calls() != before1 || a2.Calls() != before2 {
			t.Fatalf("%s: adapter invoked despite rejected input", name)
		}
	}

	reject("truncated input", wire[:len(wire)-1], netsec.ErrMalformedInput)
	reject("trailing byte", append(append([]byte(nil), wire...), 0xEE), netsec.ErrMalformedInput)

	// Forge a ct1 prefix one byte shorter than the registered canonical
	// length: it parses, but must be rejected before the adapter runs.
	short := append([]byte(nil), wire...)
	short[8] = 0x00
	short[9] = 0x07
	reject("non-canonical ct1 length", short, netsec.ErrMalformedInput)

	// Unknown suite: parseable but rejected before adapters.
	var unknown [8]byte
	unknown[0] = 0xFF
	badSuite := baseRequest(unknown,
		[]byte{1, 2, 3, 4, 5, 6, 7, 8},
		[]byte{9, 10, 11, 12, 13, 14, 15, 16})
	reject("unknown suite", mustEncode(t, badSuite), netsec.ErrUnknownSuite)

	reject("nil input", nil, netsec.ErrMalformedInput)
	reject("empty input", []byte{}, netsec.ErrMalformedInput)
}

func TestAdapterFuncInterface(t *testing.T) {
	reg := netsec.NewRegistry()
	var suiteID [8]byte
	suiteID[0] = 0x55

	fn := netsec.AdapterFunc(func(ct []byte) ([32]byte, bool) {
		var ss [32]byte
		copy(ss[:], ct)
		return ss, true
	})
	if err := reg.RegisterSuite(suiteID, netsec.SuiteConfig{
		Adapter1:       fn,
		Adapter2:       fn,
		Ciphertext1Len: 4,
		Ciphertext2Len: 4,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	wire := mustEncode(t, baseRequest(suiteID, []byte{1, 2, 3, 4}, []byte{5, 6, 7, 8}))
	if _, err := reg.Decapsulate(wire); err != nil {
		t.Fatalf("AdapterFunc decapsulate: %v", err)
	}
}

func TestConcurrentDecapsulate(t *testing.T) {
	reg, suiteID, _, _ := newTestRegistry(t, 8, 8)
	wire := mustEncode(t, baseRequest(suiteID,
		[]byte{1, 2, 3, 4, 5, 6, 7, 8},
		[]byte{9, 10, 11, 12, 13, 14, 15, 16}))

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := reg.Decapsulate(wire); err != nil {
					t.Errorf("concurrent decapsulate: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

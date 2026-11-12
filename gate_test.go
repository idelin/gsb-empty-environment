package netsec

import (
	"bytes"
	"crypto/rand"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

const testPolicy uint64 = 0x0003_0000 // SMT allowed; DEBUG clear.

func testGate(t *testing.T) (*Gate, Config) {
	t.Helper()

	firmware := make([]byte, MeasurementSize)
	firmware[0] = 0xaa
	firmware[1] = 0xbb

	cfg := Config{
		GuestID:        "guest-instance-a",
		FirmwareDigest: firmware,
		Policy:         testPolicy,
		MACKey:         bytes.Repeat([]byte{0x11}, 32),
		WrapKey:        bytes.Repeat([]byte{0x22}, 32),
	}
	gate, err := NewGate(cfg)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return gate, cfg
}

func randomNonce(t *testing.T) []byte {
	t.Helper()

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return nonce
}

func validRequest(cfg Config, nonce []byte, diskKey []byte) ReleaseRequest {
	return ReleaseRequest{
		GuestID:     cfg.GuestID,
		Measurement: ExpectedMeasurement(cfg.FirmwareDigest, cfg.Policy),
		Policy:      cfg.Policy,
		Nonce:       nonce,
		DiskKey:     diskKey,
	}
}

func assertNoPackage(t *testing.T, pkg InjectionPackage) {
	t.Helper()

	if pkg.Version != 0 ||
		pkg.GuestID != "" ||
		pkg.Measurement != nil ||
		pkg.Policy != 0 ||
		pkg.Nonce != nil ||
		pkg.Ciphertext != nil ||
		pkg.MAC != nil {
		t.Fatalf("failure returned a package: %+v", pkg)
	}
}

func TestReleaseAndOpen(t *testing.T) {
	gate, cfg := testGate(t)
	nonce := randomNonce(t)
	diskKey := bytes.Repeat([]byte{0x5a}, 32)

	pkg, err := gate.Release(validRequest(cfg, nonce, diskKey))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := gate.Verify(pkg); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(pkg.Ciphertext) == 0 || len(pkg.MAC) != 32 {
		t.Fatalf("incomplete package: ciphertext=%d mac=%d", len(pkg.Ciphertext), len(pkg.MAC))
	}
	if bytes.Equal(pkg.Ciphertext, diskKey) {
		t.Fatal("disk key was returned in plaintext")
	}

	got, err := gate.Open(pkg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, diskKey) {
		t.Fatalf("Open returned %x, want %x", got, diskKey)
	}
}

func TestRejectsDebugPolicyWithoutCiphertext(t *testing.T) {
	gate, cfg := testGate(t)
	nonce := randomNonce(t)
	debugPolicy := cfg.Policy | PolicyDebugBit

	req := validRequest(cfg, nonce, []byte("disk-key-material"))
	req.Policy = debugPolicy
	// Derive a measurement for the debug policy too, proving the DEBUG-bit
	// check itself (not only measurement derivation) rejects the launch.
	req.Measurement = ExpectedMeasurement(cfg.FirmwareDigest, debugPolicy)

	pkg, err := gate.Release(req)
	if !errors.Is(err, ErrDebugPolicy) {
		t.Fatalf("Release error = %v, want ErrDebugPolicy", err)
	}
	assertNoPackage(t, pkg)

	// The failed attempt did not produce or persist a package, and did not
	// burn the nonce before the validation phase completed.
	retry := validRequest(cfg, nonce, []byte("disk-key-material"))
	if _, err := gate.Release(retry); err != nil {
		t.Fatalf("valid retry after rejected debug request: %v", err)
	}
}

func TestRejectsDebugPolicyInConfig(t *testing.T) {
	_, cfg := testGate(t)
	cfg.Policy |= PolicyDebugBit

	if _, err := NewGate(cfg); !errors.Is(err, ErrDebugPolicy) {
		t.Fatalf("NewGate error = %v, want ErrDebugPolicy", err)
	}
}

func TestRejectsNonceReplay(t *testing.T) {
	gate, cfg := testGate(t)
	nonce := randomNonce(t)

	first, err := gate.Release(validRequest(cfg, nonce, []byte("first disk key")))
	if err != nil {
		t.Fatalf("first Release: %v", err)
	}

	second, err := gate.Release(validRequest(cfg, nonce, []byte("second disk key")))
	if !errors.Is(err, ErrNonceReused) {
		t.Fatalf("second Release error = %v, want ErrNonceReused", err)
	}
	assertNoPackage(t, second)

	if _, err := gate.Open(first); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := gate.Open(first); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("replayed Open error = %v, want ErrNonceReused", err)
	}
}

func TestRejectsWrongGuestIdentity(t *testing.T) {
	gate, cfg := testGate(t)
	nonce := randomNonce(t)

	req := validRequest(cfg, nonce, []byte("disk key"))
	req.GuestID = "guest-instance-b"
	pkg, err := gate.Release(req)
	if !errors.Is(err, ErrGuestMismatch) {
		t.Fatalf("Release error = %v, want ErrGuestMismatch", err)
	}
	assertNoPackage(t, pkg)

	valid, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	valid.GuestID = "guest-instance-b"
	if err := gate.Verify(valid); !errors.Is(err, ErrGuestMismatch) {
		t.Fatalf("Verify error = %v, want ErrGuestMismatch", err)
	}
	if key, err := gate.Open(valid); err == nil || key != nil {
		t.Fatalf("Open returned (%x, %v), want (nil, error)", key, err)
	}
}

func TestOpenAttestedRejectsReplayToAnotherInstance(t *testing.T) {
	gate, cfg := testGate(t)
	pkg, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	destination := Attestation{
		GuestID:     "guest-instance-b",
		Measurement: pkg.Measurement,
		Policy:      pkg.Policy,
		Nonce:       pkg.Nonce,
	}
	if key, err := gate.OpenAttested(pkg, destination); !errors.Is(err, ErrGuestMismatch) || key != nil {
		t.Fatalf("OpenAttested = (%x, %v), want (nil, ErrGuestMismatch)", key, err)
	}

	destination.GuestID = cfg.GuestID
	destination.Measurement = cloneBytes(destination.Measurement)
	destination.Measurement[0] ^= 0x01
	if key, err := gate.OpenAttested(pkg, destination); !errors.Is(err, ErrMeasurementMismatch) || key != nil {
		t.Fatalf("OpenAttested = (%x, %v), want (nil, ErrMeasurementMismatch)", key, err)
	}

	destination.Measurement = pkg.Measurement
	destination.Nonce = randomNonce(t)
	if key, err := gate.OpenAttested(pkg, destination); !errors.Is(err, ErrNonceMismatch) || key != nil {
		t.Fatalf("OpenAttested = (%x, %v), want (nil, ErrNonceMismatch)", key, err)
	}

	destination.Nonce = pkg.Nonce
	destination.Policy = pkg.Policy | PolicyDebugBit
	destination.Measurement = ExpectedMeasurement(cfg.FirmwareDigest, destination.Policy)
	if key, err := gate.OpenAttested(pkg, destination); !errors.Is(err, ErrDebugPolicy) || key != nil {
		t.Fatalf("OpenAttested = (%x, %v), want (nil, ErrDebugPolicy)", key, err)
	}

	// Failed destination checks do not consume the package nonce.
	destination.Policy = pkg.Policy
	destination.Measurement = pkg.Measurement
	if key, err := gate.OpenAttested(pkg, destination); err != nil || !bytes.Equal(key, []byte("disk key")) {
		t.Fatalf("valid OpenAttested = (%x, %v)", key, err)
	}
}

func TestRejectsChangedMeasurement(t *testing.T) {
	gate, cfg := testGate(t)
	pkg, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	pkg.Measurement = cloneBytes(pkg.Measurement)
	pkg.Measurement[0] ^= 0x01
	if err := gate.Verify(pkg); !errors.Is(err, ErrMeasurementMismatch) {
		t.Fatalf("Verify error = %v, want ErrMeasurementMismatch", err)
	}
	if key, err := gate.Open(pkg); err == nil || key != nil {
		t.Fatalf("Open returned (%x, %v), want (nil, error)", key, err)
	}
}

func TestRejectsPolicyThatIsNotExpected(t *testing.T) {
	gate, cfg := testGate(t)
	otherPolicy := uint64(0x0001_0000) // Different API major/minor; DEBUG clear.

	req := validRequest(cfg, randomNonce(t), []byte("disk key"))
	req.Policy = otherPolicy
	req.Measurement = ExpectedMeasurement(cfg.FirmwareDigest, otherPolicy)

	pkg, err := gate.Release(req)
	if !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("Release error = %v, want ErrPolicyMismatch", err)
	}
	assertNoPackage(t, pkg)
}

func TestRejectsTamperedNonceAndCiphertext(t *testing.T) {
	gate, cfg := testGate(t)
	base, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	changedNonce := base
	changedNonce.Nonce = randomNonce(t)
	if err := gate.Verify(changedNonce); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("changed nonce Verify error = %v, want ErrMACMismatch", err)
	}

	changedCiphertext := base
	changedCiphertext.Ciphertext = cloneBytes(base.Ciphertext)
	changedCiphertext.Ciphertext[len(changedCiphertext.Ciphertext)-1] ^= 0x01
	if err := gate.Verify(changedCiphertext); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("changed ciphertext Verify error = %v, want ErrMACMismatch", err)
	}
	if key, err := gate.Open(changedCiphertext); err == nil || key != nil {
		t.Fatalf("Open returned (%x, %v), want (nil, error)", key, err)
	}

	// A failed tampered package must not consume the original package's nonce.
	if _, err := gate.Open(base); err != nil {
		t.Fatalf("original package after failed tamper: %v", err)
	}
}

func TestMacCoversIdentityMeasurementAndNonce(t *testing.T) {
	gate, cfg := testGate(t)
	pkg, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	macFor := func(mutate func(*InjectionPackage)) []byte {
		candidate := pkg
		candidate.Measurement = cloneBytes(candidate.Measurement)
		candidate.Nonce = cloneBytes(candidate.Nonce)
		mutate(&candidate)
		return gate.computeMAC(candidate)
	}

	original := gate.computeMAC(pkg)
	cases := map[string]func(*InjectionPackage){
		"identity":    func(p *InjectionPackage) { p.GuestID += "-other" },
		"measurement": func(p *InjectionPackage) { p.Measurement[1] ^= 0x80 },
		"nonce":       func(p *InjectionPackage) { p.Nonce = randomNonce(t) },
		"policy":      func(p *InjectionPackage) { p.Policy ^= 0x10000 },
		"ciphertext":  func(p *InjectionPackage) { p.Ciphertext = cloneBytes(p.Ciphertext); p.Ciphertext[0] ^= 0x01 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(original, macFor(mutate)) {
				t.Fatalf("MAC did not change when %s changed", name)
			}
		})
	}
}

func TestInvalidNoncesProduceNoPackage(t *testing.T) {
	gate, cfg := testGate(t)

	cases := map[string][]byte{
		"missing":   nil,
		"too short": []byte("short"),
		"all zero":  make([]byte, 32),
	}
	for name, nonce := range cases {
		t.Run(name, func(t *testing.T) {
			pkg, err := gate.Release(validRequest(cfg, nonce, []byte("disk key")))
			if !errors.Is(err, ErrInvalidNonce) {
				t.Fatalf("Release error = %v, want ErrInvalidNonce", err)
			}
			assertNoPackage(t, pkg)
		})
	}
}

func TestInvalidConfig(t *testing.T) {
	_, cfg := testGate(t)

	cases := map[string]func(*Config){
		"guest": func(c *Config) { c.GuestID = "" },
		"firmware": func(c *Config) {
			c.FirmwareDigest = nil
		},
		"mac key":  func(c *Config) { c.MACKey = nil },
		"wrap key": func(c *Config) { c.WrapKey = nil },
		"short mac key": func(c *Config) {
			c.MACKey = []byte("too short")
		},
		"short wrap key": func(c *Config) {
			c.WrapKey = []byte("too short")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bad := cfg
			mutate(&bad)
			if _, err := NewGate(bad); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewGate error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestWrongWrapKeyProducesNoPlaintext(t *testing.T) {
	gate, cfg := testGate(t)
	pkg, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	cfg.WrapKey = bytes.Repeat([]byte{0x33}, 32)
	other, err := NewGate(cfg)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if err := other.Verify(pkg); err != nil {
		t.Fatalf("Verify with same MAC key: %v", err)
	}
	if key, err := other.Open(pkg); err == nil || key != nil {
		t.Fatalf("Open with wrong wrapping key returned (%x, %v)", key, err)
	}
}

func TestUnsupportedVersionAndMissingCiphertext(t *testing.T) {
	gate, cfg := testGate(t)
	pkg, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}

	pkg.Version = 2
	if err := gate.Verify(pkg); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Verify error = %v, want ErrUnsupportedVersion", err)
	}

	pkg, err = gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	pkg.Ciphertext = nil
	if err := gate.Verify(pkg); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("Verify error = %v, want ErrInvalidCiphertext", err)
	}
}

func TestConcurrentNonceClaim(t *testing.T) {
	gate, cfg := testGate(t)
	nonce := randomNonce(t)

	const workers = 32
	var successes atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := gate.Release(validRequest(cfg, nonce, []byte{byte(i)}))
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrNonceReused) {
				t.Errorf("Release error = %v, want nil or ErrNonceReused", err)
			}
		}(i)
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful releases = %d, want exactly 1", got)
	}
}

func TestSharedNonceStoreSurvivesGateRecreation(t *testing.T) {
	_, cfg := testGate(t)
	store := newMemoryNonceStore()
	cfg.NonceStore = store
	nonce := randomNonce(t)

	firstGate, err := NewGate(cfg)
	if err != nil {
		t.Fatalf("first NewGate: %v", err)
	}
	if _, err := firstGate.Release(validRequest(cfg, nonce, []byte("disk key"))); err != nil {
		t.Fatalf("first Release: %v", err)
	}

	secondGate, err := NewGate(cfg)
	if err != nil {
		t.Fatalf("second NewGate: %v", err)
	}
	pkg, err := secondGate.Release(validRequest(cfg, nonce, []byte("disk key")))
	if !errors.Is(err, ErrNonceReused) {
		t.Fatalf("Release after gate recreation = %v, want ErrNonceReused", err)
	}
	assertNoPackage(t, pkg)
}

type failingNonceStore struct {
	err error
}

func (s failingNonceStore) Claim(string, []byte) (bool, error) {
	return false, s.err
}

func TestNonceStoreFailureProducesNoCiphertext(t *testing.T) {
	_, cfg := testGate(t)
	storeErr := errors.New("replay store unavailable")
	cfg.NonceStore = failingNonceStore{err: storeErr}

	gate, err := NewGate(cfg)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	pkg, err := gate.Release(validRequest(cfg, randomNonce(t), []byte("disk key")))
	if !errors.Is(err, storeErr) {
		t.Fatalf("Release error = %v, want replay store error", err)
	}
	assertNoPackage(t, pkg)
}

func TestRequestSlicesAreCopied(t *testing.T) {
	gate, cfg := testGate(t)
	nonce := randomNonce(t)
	req := validRequest(cfg, nonce, []byte("disk key"))

	pkg, err := gate.Release(req)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	nonce[0] ^= 0xff
	req.Measurement[0] ^= 0xff

	if pkg.Nonce[0] == nonce[0] {
		t.Fatal("package nonce aliases request memory")
	}
	if err := gate.Verify(pkg); err != nil {
		t.Fatalf("package changed after request mutation: %v", err)
	}
}

func TestExpectedMeasurementBindsBothInputs(t *testing.T) {
	firmwareA := bytes.Repeat([]byte{0xa5}, MeasurementSize)
	firmwareB := bytes.Repeat([]byte{0x5a}, MeasurementSize)

	base := ExpectedMeasurement(firmwareA, testPolicy)
	if len(base) != MeasurementSize {
		t.Fatalf("measurement length = %d, want %d", len(base), MeasurementSize)
	}
	if bytes.Equal(base, ExpectedMeasurement(firmwareB, testPolicy)) {
		t.Fatal("firmware change did not change measurement")
	}
	if bytes.Equal(base, ExpectedMeasurement(firmwareA, testPolicy|PolicyDebugBit)) {
		t.Fatal("policy change did not change measurement")
	}
}

func TestGenerateNonce(t *testing.T) {
	first, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	if len(first) != DefaultNonceSize {
		t.Fatalf("nonce length = %d, want %d", len(first), DefaultNonceSize)
	}

	second, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two generated nonces are identical")
	}
	if _, err := GenerateNonceSize(MinNonceSize - 1); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("GenerateNonceSize error = %v, want ErrInvalidNonce", err)
	}
}

package netsec

import (
	"bytes"
	"crypto/sha3"
	"errors"
	"testing"
)

// toyPKE is a deterministic stand-in for K-PKE. The first 32 ciphertext bytes
// carry the message; the rest are an injective deterministic expansion of the
// encapsulation key, message, and randomness. It is not a real PKE and exists
// only to exercise the decapsulation control flow.
type toyPKE struct {
	encryptCalls int
	decryptCalls int
}

func (p *toyPKE) Encrypt(encapsulationKey, message, randomness []byte) ([]byte, error) {
	p.encryptCalls++
	ciphertext := make([]byte, MLKEM768CiphertextSize)
	copy(ciphertext, message)

	seed := make([]byte, 0, len(encapsulationKey)+len(message)+len(randomness))
	seed = append(seed, encapsulationKey...)
	seed = append(seed, message...)
	seed = append(seed, randomness...)
	stream := sha3.SumSHAKE256(seed, len(ciphertext)-len(message))
	copy(ciphertext[len(message):], stream)
	return ciphertext, nil
}

func (p *toyPKE) Decrypt(_, ciphertext []byte) ([]byte, error) {
	p.decryptCalls++
	if len(ciphertext) != MLKEM768CiphertextSize {
		return nil, ErrInvalidCiphertextLength
	}
	message := make([]byte, MLKEM768SharedKeySize)
	copy(message, ciphertext[:MLKEM768SharedKeySize])
	return message, nil
}

func (p *toyPKE) resetCalls() {
	p.encryptCalls = 0
	p.decryptCalls = 0
}

type recordingPRF struct {
	calls int
	z     []byte
	ct    []byte
}

func (p *recordingPRF) derive(z, ciphertext []byte) []byte {
	p.calls++
	p.z = bytes.Clone(z)
	p.ct = bytes.Clone(ciphertext)
	return toyRejectionKey(z, ciphertext)
}

func (p *recordingPRF) resetCalls() {
	p.calls = 0
	p.z = nil
	p.ct = nil
}

func toyRejectionKey(z, ciphertext []byte) []byte {
	input := make([]byte, 0, len(z)+len(ciphertext))
	input = append(input, z...)
	input = append(input, ciphertext...)
	return sha3.SumSHAKE256(input, MLKEM768SharedKeySize)
}

type failingPKE struct {
	pke        toyPKE
	decryptErr error
	encryptErr error
}

func (p *failingPKE) Encrypt(encapsulationKey, message, randomness []byte) ([]byte, error) {
	if p.encryptErr != nil {
		return nil, p.encryptErr
	}
	return p.pke.Encrypt(encapsulationKey, message, randomness)
}

func (p *failingPKE) Decrypt(decapsulationKey, ciphertext []byte) ([]byte, error) {
	if p.decryptErr != nil {
		return nil, p.decryptErr
	}
	return p.pke.Decrypt(decapsulationKey, ciphertext)
}

type decapsFixture struct {
	dk               []byte
	ct               []byte
	expectedKey      []byte
	encapsulationKey []byte
	z                []byte
	message          []byte
	randomness       []byte
	pke              *toyPKE
	prf              *recordingPRF
}

func newDecapsFixture(t *testing.T) *decapsFixture {
	t.Helper()

	pke := &toyPKE{}
	prf := &recordingPRF{}

	dkPKE := make([]byte, MLKEM768PKEKeySize)
	for i := range dkPKE {
		dkPKE[i] = byte(i*7 + 3)
	}
	ek := make([]byte, MLKEM768EncapsulationKeySize)
	for i := range ek {
		ek[i] = byte(i*11 + 5)
	}
	z := make([]byte, MLKEM768SharedKeySize)
	for i := range z {
		z[i] = byte(i*13 + 9)
	}
	message := make([]byte, MLKEM768SharedKeySize)
	for i := range message {
		message[i] = byte(i*17 + 1)
	}

	h := sha3.Sum256(ek)
	gInput := make([]byte, 0, len(message)+len(h))
	gInput = append(gInput, message...)
	gInput = append(gInput, h[:]...)
	g := sha3.Sum512(gInput)

	dk := make([]byte, 0, MLKEM768DecapsulationKeySize)
	dk = append(dk, dkPKE...)
	dk = append(dk, ek...)
	dk = append(dk, h[:]...)
	dk = append(dk, z...)

	ct, err := pke.Encrypt(ek, message, g[MLKEM768SharedKeySize:])
	if err != nil {
		t.Fatalf("toy Encrypt: %v", err)
	}

	return &decapsFixture{
		dk:               dk,
		ct:               ct,
		expectedKey:      bytes.Clone(g[:MLKEM768SharedKeySize]),
		encapsulationKey: ek,
		z:                z,
		message:          message,
		randomness:       bytes.Clone(g[MLKEM768SharedKeySize:]),
		pke:              pke,
		prf:              prf,
	}
}

func newFixtureDecapsulator(t *testing.T, f *decapsFixture) *Decapsulator {
	t.Helper()
	d, err := NewDecapsulator(f.pke, f.prf.derive)
	if err != nil {
		t.Fatalf("NewDecapsulator: %v", err)
	}
	return d
}

func TestDecapsulateValidCiphertext(t *testing.T) {
	f := newDecapsFixture(t)
	d := newFixtureDecapsulator(t, f)
	f.pke.resetCalls()
	f.prf.resetCalls()

	got, err := d.Decapsulate(f.dk, f.ct)
	if err != nil {
		t.Fatalf("Decapsulate: %v", err)
	}
	if !bytes.Equal(got, f.expectedKey) {
		t.Fatalf("shared key mismatch:\n got %x\nwant %x", got, f.expectedKey)
	}
	if f.pke.decryptCalls != 1 || f.pke.encryptCalls != 1 {
		t.Fatalf("K-PKE calls = decrypt %d, encrypt %d; want 1, 1", f.pke.decryptCalls, f.pke.encryptCalls)
	}
	if f.prf.calls != 1 {
		t.Fatalf("PRF calls = %d; want 1", f.prf.calls)
	}
	if !bytes.Equal(f.prf.z, f.z) || !bytes.Equal(f.prf.ct, f.ct) {
		t.Fatalf("PRF did not receive z and the received ciphertext")
	}
}

func TestDecapsulateEverySingleBitFlip(t *testing.T) {
	f := newDecapsFixture(t)
	d := newFixtureDecapsulator(t, f)

	for byteIndex := range f.ct {
		for bit := uint(0); bit < 8; bit++ {
			tampered := bytes.Clone(f.ct)
			tampered[byteIndex] ^= 1 << bit

			got, err := d.Decapsulate(f.dk, tampered)
			if err != nil {
				t.Fatalf("flip byte %d bit %d returned error: %v", byteIndex, bit, err)
			}
			if bytes.Equal(got, f.expectedKey) {
				t.Fatalf("flip byte %d bit %d returned the valid shared key", byteIndex, bit)
			}
			want := toyRejectionKey(f.z, tampered)
			if !bytes.Equal(got, want) {
				t.Fatalf("flip byte %d bit %d: got %x, want rejection key %x", byteIndex, bit, got, want)
			}

			again, err := d.Decapsulate(f.dk, tampered)
			if err != nil {
				t.Fatalf("repeated flip byte %d bit %d returned error: %v", byteIndex, bit, err)
			}
			if !bytes.Equal(got, again) {
				t.Fatalf("flip byte %d bit %d is not deterministic", byteIndex, bit)
			}
		}
	}
}

func TestDecapsulateRejectsWrongCiphertextLength(t *testing.T) {
	f := newDecapsFixture(t)
	d := newFixtureDecapsulator(t, f)
	f.pke.resetCalls()
	f.prf.resetCalls()

	for _, size := range []int{0, 1, 31, 32, 1024, MLKEM768CiphertextSize - 1, MLKEM768CiphertextSize + 1} {
		_, err := d.Decapsulate(f.dk, make([]byte, size))
		if !errors.Is(err, ErrInvalidCiphertextLength) {
			t.Fatalf("length %d: got error %v, want ErrInvalidCiphertextLength", size, err)
		}
	}
	if f.pke.decryptCalls != 0 || f.pke.encryptCalls != 0 || f.prf.calls != 0 {
		t.Fatalf("invalid lengths reached primitives: decrypt %d, encrypt %d, prf %d",
			f.pke.decryptCalls, f.pke.encryptCalls, f.prf.calls)
	}
}

func TestDecapsulateRejectsModifiedEmbeddedHash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
	}{
		{name: "embedded-hash", offset: mlKEM768HashOffset},
		{name: "encapsulation-key", offset: mlKEM768EncapsulationKeyOffset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDecapsFixture(t)
			d := newFixtureDecapsulator(t, f)
			f.pke.resetCalls()
			f.prf.resetCalls()

			tampered := bytes.Clone(f.dk)
			tampered[tc.offset] ^= 1

			key, err := d.Decapsulate(tampered, f.ct)
			if key != nil {
				t.Fatalf("tampered key produced a shared key")
			}
			if !errors.Is(err, ErrInvalidDecapsulationKey) {
				t.Fatalf("got error %v, want ErrInvalidDecapsulationKey", err)
			}
			if !errors.Is(err, ErrEmbeddedHashMismatch) {
				t.Fatalf("got error %v, want ErrEmbeddedHashMismatch", err)
			}
			if f.pke.decryptCalls != 0 || f.pke.encryptCalls != 0 || f.prf.calls != 0 {
				t.Fatalf("H(ek) failure reached primitives: decrypt %d, encrypt %d, prf %d",
					f.pke.decryptCalls, f.pke.encryptCalls, f.prf.calls)
			}
		})
	}
}

func TestDecapsulateRejectsMalformedKeyLength(t *testing.T) {
	f := newDecapsFixture(t)
	d := newFixtureDecapsulator(t, f)
	f.pke.resetCalls()
	f.prf.resetCalls()

	for _, size := range []int{0, 1, MLKEM768DecapsulationKeySize - 1, MLKEM768DecapsulationKeySize + 1} {
		key, err := d.Decapsulate(make([]byte, size), f.ct)
		if key != nil {
			t.Fatalf("key length %d produced a shared key", size)
		}
		if !errors.Is(err, ErrInvalidDecapsulationKey) {
			t.Fatalf("key length %d: got error %v, want ErrInvalidDecapsulationKey", size, err)
		}
	}
	if f.pke.decryptCalls != 0 || f.pke.encryptCalls != 0 || f.prf.calls != 0 {
		t.Fatalf("invalid key lengths reached primitives")
	}
}

func TestDeterministicToyPKEAndDecapsulation(t *testing.T) {
	f := newDecapsFixture(t)
	d := newFixtureDecapsulator(t, f)

	ct1, err := f.pke.Encrypt(f.encapsulationKey, f.message, f.randomness)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct2, err := f.pke.Encrypt(f.encapsulationKey, f.message, f.randomness)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.Equal(ct1, ct2) {
		t.Fatalf("toy PKE encryption is not deterministic")
	}

	m1, err := f.pke.Decrypt(nil, ct1)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	m2, err := f.pke.Decrypt(nil, ct2)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(m1, m2) || !bytes.Equal(m1, f.message) {
		t.Fatalf("toy PKE decryption is not deterministic")
	}

	key1, err := d.Decapsulate(f.dk, ct1)
	if err != nil {
		t.Fatalf("Decapsulate: %v", err)
	}
	key2, err := d.Decapsulate(f.dk, ct2)
	if err != nil {
		t.Fatalf("Decapsulate: %v", err)
	}
	if !bytes.Equal(key1, key2) || !bytes.Equal(key1, f.expectedKey) {
		t.Fatalf("decapsulation is not deterministic")
	}
}

func TestInvalidPrimitivesAndPRFOutput(t *testing.T) {
	f := newDecapsFixture(t)
	if _, err := NewDecapsulator(nil, f.prf.derive); !errors.Is(err, ErrInvalidPrimitives) {
		t.Fatalf("nil K-PKE: got %v, want ErrInvalidPrimitives", err)
	}
	if _, err := NewDecapsulator(f.pke, nil); !errors.Is(err, ErrInvalidPrimitives) {
		t.Fatalf("nil PRF: got %v, want ErrInvalidPrimitives", err)
	}

	shortPRF := func(_, _ []byte) []byte { return []byte{1, 2, 3} }
	d, err := NewDecapsulator(f.pke, shortPRF)
	if err != nil {
		t.Fatalf("NewDecapsulator: %v", err)
	}
	if _, err := d.Decapsulate(f.dk, f.ct); !errors.Is(err, ErrInvalidPRFOutput) {
		t.Fatalf("short PRF output: got %v, want ErrInvalidPRFOutput", err)
	}

	var nilDecapsulator *Decapsulator
	if _, err := nilDecapsulator.Decapsulate(f.dk, f.ct); !errors.Is(err, ErrInvalidPrimitives) {
		t.Fatalf("nil decapsulator: got %v, want ErrInvalidPrimitives", err)
	}
}

func TestInjectedPKEErrorsBecomeImplicitRejection(t *testing.T) {
	f := newDecapsFixture(t)

	for _, tc := range []struct {
		name string
		pke  *failingPKE
	}{
		{name: "decrypt-error", pke: &failingPKE{decryptErr: errors.New("injected decrypt failure")}},
		{name: "encrypt-error", pke: &failingPKE{encryptErr: errors.New("injected encrypt failure")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewDecapsulator(tc.pke, f.prf.derive)
			if err != nil {
				t.Fatalf("NewDecapsulator: %v", err)
			}
			got, err := d.Decapsulate(f.dk, f.ct)
			if err != nil {
				t.Fatalf("injected K-PKE error leaked from Decapsulate: %v", err)
			}
			want := toyRejectionKey(f.z, f.ct)
			if !bytes.Equal(got, want) {
				t.Fatalf("got %x, want implicit rejection key %x", got, want)
			}
		})
	}
}

func TestDecapsulateMLKEM768Helper(t *testing.T) {
	f := newDecapsFixture(t)
	got, err := DecapsulateMLKEM768(f.dk, f.ct, f.pke, f.prf.derive)
	if err != nil {
		t.Fatalf("DecapsulateMLKEM768: %v", err)
	}
	if !bytes.Equal(got, f.expectedKey) {
		t.Fatalf("helper key mismatch")
	}
}

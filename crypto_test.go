package netsec

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"testing"
)

func TestPRFExpansion(t *testing.T) {
	secret := bytes.Repeat([]byte{0x11}, 32)
	seed := bytes.Repeat([]byte{0x22}, 64)

	out := PRF(HashFunc(crypto.SHA256), secret, "test label", seed, 100)
	if len(out) != 100 {
		t.Fatalf("PRF length = %d, want 100", len(out))
	}

	mac := hmac.New(crypto.SHA256.New, secret)
	mac.Write([]byte("test label"))
	mac.Write(seed)
	a1 := mac.Sum(nil)

	mac.Reset()
	mac.Write(a1)
	mac.Write([]byte("test label"))
	mac.Write(seed)
	firstBlock := mac.Sum(nil)
	if !bytes.Equal(out[:32], firstBlock) {
		t.Fatal("PRF first P_SHA256 block does not match independent HMAC computation")
	}
}

func TestPRFDeterministicAndLabelSensitive(t *testing.T) {
	secret := []byte("master-secret-bytes-here-1234567890")
	seed := []byte("deterministic-seed")

	first := PRF(HashFunc(crypto.SHA256), secret, "label A", seed, 40)
	second := PRF(HashFunc(crypto.SHA256), secret, "label A", seed, 40)
	if !bytes.Equal(first, second) {
		t.Fatal("PRF is not deterministic")
	}

	other := PRF(HashFunc(crypto.SHA256), secret, "label B", seed, 40)
	if bytes.Equal(first, other) {
		t.Fatal("PRF output must change when the label changes")
	}
}

func TestDeriveMasterSecretEMSvsLegacy(t *testing.T) {
	preMaster := bytes.Repeat([]byte{0xAB}, 48)
	clientRandom := bytes.Repeat([]byte{0x01}, 32)
	serverRandom := bytes.Repeat([]byte{0x02}, 32)
	transcript := bytes.Repeat([]byte{0x03}, 128)

	legacy := DeriveMasterSecret(HashFunc(crypto.SHA256), preMaster, clientRandom, serverRandom, transcript, false)
	extended := DeriveMasterSecret(HashFunc(crypto.SHA256), preMaster, clientRandom, serverRandom, transcript, true)

	if len(legacy) != MasterSecretLength || len(extended) != MasterSecretLength {
		t.Fatalf("master secret lengths = %d, %d, want %d", len(legacy), len(extended), MasterSecretLength)
	}
	if bytes.Equal(legacy, extended) {
		t.Fatal("extended and legacy master secrets must differ")
	}

	wantSeed := make([]byte, 0, 64)
	wantSeed = append(wantSeed, clientRandom...)
	wantSeed = append(wantSeed, serverRandom...)
	wantLegacy := PRF(HashFunc(crypto.SHA256), preMaster, masterSecretLabel, wantSeed, MasterSecretLength)
	if !bytes.Equal(legacy, wantLegacy) {
		t.Fatal("legacy master secret does not match direct PRF over random values")
	}

	wantExtended := PRF(HashFunc(crypto.SHA256), preMaster, extendedMasterSecretLbl, SessionHash(HashFunc(crypto.SHA256), transcript), MasterSecretLength)
	if !bytes.Equal(extended, wantExtended) {
		t.Fatal("extended master secret does not match direct PRF over session hash")
	}
}

func TestKeyBlockAndVerifyData(t *testing.T) {
	master := bytes.Repeat([]byte{0x44}, MasterSecretLength)
	clientRandom := bytes.Repeat([]byte{0x05}, 32)
	serverRandom := bytes.Repeat([]byte{0x06}, 32)
	transcript := bytes.Repeat([]byte{0x07}, 96)

	keyBlock := DeriveKeyBlock(HashFunc(crypto.SHA256), master, clientRandom, serverRandom, 40)
	if len(keyBlock) != 40 {
		t.Fatalf("key block length = %d, want 40", len(keyBlock))
	}

	seed := append(append([]byte(nil), serverRandom...), clientRandom...)
	if !bytes.Equal(keyBlock, PRF(HashFunc(crypto.SHA256), master, keyExpansionLabel, seed, 40)) {
		t.Fatal("key block does not match direct key expansion PRF")
	}

	clientVerify := VerifyData(HashFunc(crypto.SHA256), master, transcript, true)
	serverVerify := VerifyData(HashFunc(crypto.SHA256), master, transcript, false)
	if len(clientVerify) != 12 || len(serverVerify) != 12 {
		t.Fatalf("verify_data lengths = %d, %d, want 12", len(clientVerify), len(serverVerify))
	}
	if bytes.Equal(clientVerify, serverVerify) {
		t.Fatal("client and server verify_data must differ")
	}
}

func TestSessionHash(t *testing.T) {
	transcript := []byte("handshake transcript bytes")
	h := crypto.SHA256.New()
	h.Write(transcript)
	want := h.Sum(nil)
	if got := SessionHash(HashFunc(crypto.SHA256), transcript); !bytes.Equal(got, want) {
		t.Fatal("SessionHash does not match direct hash of transcript")
	}
}

func TestHashDefaultsToSHA256(t *testing.T) {
	out := PRF(0, []byte("secret"), "label", []byte("seed"), 16)
	want := PRF(HashFunc(crypto.SHA256), []byte("secret"), "label", []byte("seed"), 16)
	if !bytes.Equal(out, want) {
		t.Fatal("zero HashFunc must default to SHA-256")
	}
}

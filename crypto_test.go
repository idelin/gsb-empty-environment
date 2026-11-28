package wireguard

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBLAKE2sVectors(t *testing.T) {
	vectors := []struct {
		msg  string
		want string
	}{
		{"", "69217a3079908094e11121d042354a7c1f55b6482ca1a51e1b250dfd1ed0eef9"},
		{"abc", "508c5e8c327c14e2e1a72ba34eeb452f37458b209ed63a294d999b4c86675982"},
		{"The quick brown fox jumps over the lazy dog", "606beeec743ccbeff6cbcdf5d5302aa855c256c29b88c8ed331ea1a6bf3c8812"},
	}
	for _, v := range vectors {
		got := blake2sHash([]byte(v.msg))
		if hex.EncodeToString(got[:]) != v.want {
			t.Fatalf("BLAKE2s(%q) = %x, want %s", v.msg, got, v.want)
		}
	}
}

func TestBLAKE2sMultiBlockVectors(t *testing.T) {
	vectors := []struct {
		n    int
		want string
	}{
		{64, "651d2f5f20952eacaea2fba2f2af2bcd633e511ea2d2e4c9ae2ac0d9ffb7b252"},
		{65, "045f8ae18932119bd051ac7ba5c73db59892055fad5c32f82d79a6543d92a497"},
		{128, "3ac477e27353f9019b81694afe60c8049403784f91a58288428ea318bfa82809"},
		{200, "2b033f9f5ba9cf20671da79e492f41545e673b562603945ffed09662fd92321a"},
	}
	for _, v := range vectors {
		data := bytes.Repeat([]byte{'a'}, v.n)
		got := blake2sHash(data)
		if hex.EncodeToString(got[:]) != v.want {
			t.Fatalf("BLAKE2s(%d bytes) = %x, want %s", v.n, got, v.want)
		}
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	data := bytes.Repeat([]byte{'b'}, 200)
	got := blake2sMAC(key, data)
	if hex.EncodeToString(got[:]) != "642ef8fa2367b04c8d2c3cf853fde621" {
		t.Fatalf("keyed multi-block BLAKE2s = %x", got)
	}
}

func TestBLAKE2sKeyedVectors(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	got := blake2sMAC(key, []byte("hello"))
	if hex.EncodeToString(got[:]) != "8e02c77ff5166d400b7dbd2559cc292c" {
		t.Fatalf("keyed BLAKE2s = %x", got)
	}
	got = blake2sMAC(key[:16], []byte("hello"))
	if hex.EncodeToString(got[:]) != "a40eccae10cc7838077e17e5792bf52c" {
		t.Fatalf("keyed BLAKE2s-128 = %x", got)
	}
}

func TestHMACBLAKE2sVector(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	got := hmacBlake2s(key, []byte("WireGuard KDF test"))
	if hex.EncodeToString(got[:]) != "c2ad0c774c25cfeac1387c742d2e4b11b146a1bc13d1ee02f0c441490fb7cfca" {
		t.Fatalf("HMAC-BLAKE2s = %x", got)
	}
	suite := DefaultSuite{}
	k1 := suite.KDF1([32]byte(key), []byte("WireGuard KDF test"))
	if hex.EncodeToString(k1[:]) != "c2ad0c774c25cfeac1387c742d2e4b11b146a1bc13d1ee02f0c441490fb7cfca" {
		t.Fatalf("KDF1 = %x", k1)
	}
}

func TestPoly1305RFCVector(t *testing.T) {
	key := mustHex(t, "85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b")
	mac := newPoly1305(key)
	mac.Write([]byte("Cryptographic Forum Research Group"))
	tag := mac.Sum()
	if hex.EncodeToString(tag[:]) != "a8061dc1305136c6c22b8baf0c0127a9" {
		t.Fatalf("Poly1305 = %x", tag)
	}
}

func TestChaCha20Poly1305RFCVector(t *testing.T) {
	key := mustHex(t, "808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f")
	nonce := mustHex(t, "070000004041424344454647")
	aad := mustHex(t, "50515253c0c1c2c3c4c5c6c7")
	plaintext := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")
	want := mustHex(t, "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b61161ae10b594f09e26a7e902ecbd0600691")
	keyArr := [32]byte(key)
	var nonceArr [12]byte
	copy(nonceArr[:], nonce)
	got := chacha20Poly1305Seal(&keyArr, &nonceArr, plaintext, aad)
	if !bytes.Equal(got, want) {
		t.Fatalf("ChaCha20-Poly1305 mismatch\n got %x\nwant %x", got, want)
	}
	opened, err := chacha20Poly1305Open(&keyArr, &nonceArr, got, aad)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("open failed: %v", err)
	}
	got[len(got)-1] ^= 1
	if _, err := chacha20Poly1305Open(&keyArr, &nonceArr, got, aad); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestHChaCha20Vector(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	var nonce [16]byte
	copy(nonce[:], mustHex(t, "000000090000004a0000000031415927"))
	keyArr := [32]byte(key)
	got := hchacha20(&keyArr, &nonce)
	if hex.EncodeToString(got[:]) != "82413b4227b27bfed30e42508a877d73a0f9e4d58a74a853c12ec41326d3ecdc" {
		t.Fatalf("HChaCha20 = %x", got)
	}
}

func TestXChaCha20Poly1305Vector(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	var nonce [24]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	aad := []byte("header-ad")
	plaintext := []byte("wireguard cookie test")
	keyArr := [32]byte(key)
	got := xchacha20Poly1305Seal(&keyArr, &nonce, plaintext, aad)
	if hex.EncodeToString(got) != "e9ab7d1af7a7ecdc576445a1a439c18d6b334dd689ca48daa70078a23193c2658c30eca24c" {
		t.Fatalf("XChaCha20-Poly1305 = %x", got)
	}
	opened, err := xchacha20Poly1305Open(&keyArr, &nonce, got, aad)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("XChaCha open failed: %v", err)
	}
}

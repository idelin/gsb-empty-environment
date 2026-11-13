package netsec_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"

	netsec "example.org/netsec/q0104"
)

func TestPackageIsImportable(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(aesBlock)
	if err != nil {
		t.Fatal(err)
	}
	server, err := netsec.NewMemoryServer("imported-tree", 3)
	if err != nil {
		t.Fatal(err)
	}
	cfg := netsec.Config{
		TreeID:         "imported-tree",
		Height:         3,
		BucketCapacity: 3,
		BlockDataSize:  24,
		StashCapacity:  64,
		AEAD:           aead,
		MACKey:         bytes.Repeat([]byte{0x22}, 32),
		RootPublicKey:  server.PublicKey(),
	}
	client, err := netsec.NewClient(cfg, server, netsec.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte{0x33}, 24)
	written, err := client.Write(1, data)
	if err != nil {
		t.Fatal(err)
	}
	read, err := client.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	if written.Version != 1 || read.Version != 1 || !bytes.Equal(read.Data, data) {
		t.Fatalf("written %+v read %+v", written, read)
	}
	if _, err := client.Read(1); err != nil {
		t.Fatal(err)
	}
	if err := client.Recover(); err != nil {
		t.Fatal(err)
	}
}

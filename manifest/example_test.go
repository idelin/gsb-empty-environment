package manifest_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"prescreen.local/ps-d4797ec147/manifest"
)

func Example() {
	// Two pinned roots; the host requires both to sign every manifest.
	rootApub, rootApriv, _ := ed25519.GenerateKey(rand.Reader)
	rootBpub, rootBpriv, _ := ed25519.GenerateKey(rand.Reader)
	fwPub, fwPriv, _ := ed25519.GenerateKey(rand.Reader)

	dir := filepath.Join(os.TempDir(), "manifest-example")
	defer os.RemoveAll(dir)
	store, err := manifest.NewFileStore(dir)
	if err != nil {
		panic(err)
	}

	host, err := manifest.NewHost(manifest.Config{
		DeviceModel: "board-42",
		Roots: map[string]manifest.Root{
			"root-a": {ID: "root-a", PubKey: []byte(rootApub)},
			"root-b": {ID: "root-b", PubKey: []byte(rootBpub)},
		},
		RootThreshold:        2,
		MinFirmwareThreshold: 1,
		Now:                  func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) },
	}, store)
	if err != nil {
		panic(err)
	}

	m := &manifest.Manifest{
		DeviceModel: "board-42",
		Generation:  1,
		NotBefore:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:    time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Threshold:   1,
		Entries: []manifest.KeyEntry{{
			KeyID:    "fw-1",
			PubKey:   []byte(fwPub),
			Purposes: []string{manifest.PurposeFirmware},
			GenStart: 1,
			GenStop:  100,
		}},
	}
	signed := &manifest.SignedManifest{
		Manifest: m,
		Signatures: []manifest.RootSignature{
			manifest.SignManifest(m, "root-a", rootApriv),
			manifest.SignManifest(m, "root-b", rootBpriv),
		},
	}
	if err := host.Accept(signed); err != nil {
		panic(err)
	}

	image := &manifest.FirmwareImage{
		DeviceModel: "board-42",
		ImageDigest: []byte("sha256:firmware"),
		Version:     "2.0.0",
		Generation:  1,
	}
	image.Signatures = []manifest.FirmwareSignature{
		manifest.SignFirmware(image, "fw-1", fwPriv),
	}

	if err := host.BootCheck(image); err != nil {
		fmt.Println("boot denied:", err)
		return
	}
	fmt.Println("boot admitted")
	// Output: boot admitted
}

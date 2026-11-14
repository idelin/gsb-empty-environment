package safeuntar_test

import (
	"archive/tar"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"example.org/netsec/q0230/safeuntar"
)

func ExampleExtract() {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	_ = tw.WriteHeader(&tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "bin/firmware.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5})
	_, _ = tw.Write([]byte("hello"))
	_ = tw.Close()

	dest := filepath.Join(os.TempDir(), "firmware-build")
	_ = os.RemoveAll(dest) // example only; real callers ensure dest is absent

	if err := safeuntar.Extract(&archive, dest); err != nil {
		log.Fatalf("unpack failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dest, "bin", "firmware.txt"))
	if err != nil {
		log.Fatalf("read extracted file: %v", err)
	}
	fmt.Println(string(content))
	// Output: hello
}

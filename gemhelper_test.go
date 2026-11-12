package netsec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"io"
)

type gemFixture struct {
	Metadata []byte
	Data     []byte
}

func newGemFixture() *gemFixture {
	return &gemFixture{
		Metadata: []byte("---\nname: example\nversion: 1.0.0\n"),
		Data:     []byte("fake data.tar.gz payload with files inside"),
	}
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

type buildConfig struct {
	// signer signs every member; nil produces an unsigned gem.
	signer *testChain
	// algo selects the signature algorithms; zero means RSA-SHA256.
	algo sigAlgo
	// omitSigs drops these .sig members to model signature stripping.
	omitSigs []string
	// duplicateData adds a second data.tar.gz entry.
	duplicateData bool
	// corruptChecksum makes checksums.yaml lie about metadata.gz.
	corruptChecksum bool
	// tamperMetadataAfterBuild appends a byte to the on-disk metadata.gz
	// entry only, after checksums and signatures were produced.
	tamperMetadataAfterBuild bool
	// duplicateChecksumKey repeats a key inside checksums.yaml.
	duplicateChecksumKey bool
	// extraMember adds an unknown outer tar member.
	extraMember bool
}

func (f *gemFixture) build(cfg buildConfig) []byte {
	metadataGZ := gzipBytes(f.Metadata)
	dataGZ := gzipBytes(f.Data)
	members := map[string][]byte{
		MemberMetadata:  metadataGZ,
		MemberData:      dataGZ,
		MemberChecksums: f.checksumsGZ(metadataGZ, dataGZ, cfg),
	}
	pristineMetadataGZ := metadataGZ

	signer := cfg.signer
	algo := cfg.algo
	if algo.hash == 0 {
		algo = rsaSHA256Algo
	}
	if signer != nil {
		for _, name := range signedMembers {
			if contains(cfg.omitSigs, name+".sig") {
				continue
			}
			sig, err := signer.signDetached(members[name], algo)
			if err != nil {
				panic(err)
			}
			members[name+".sig"] = sig
		}
	}

	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	writeTarMember := func(name string, content []byte) {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if _, err := io.Copy(tw, bytes.NewReader(content)); err != nil {
			panic(err)
		}
	}

	if cfg.tamperMetadataAfterBuild {
		tampered := append(append([]byte(nil), pristineMetadataGZ...), 0x00)
		writeTarMember(MemberMetadata, tampered)
	} else {
		writeTarMember(MemberMetadata, members[MemberMetadata])
	}
	if cfg.duplicateData {
		writeTarMember(MemberData, gzipBytes([]byte("second data.tar.gz, different bytes")))
	}
	writeTarMember(MemberData, members[MemberData])
	writeTarMember(MemberChecksums, members[MemberChecksums])
	for _, name := range signedMembers {
		if sig, ok := members[name+".sig"]; ok {
			writeTarMember(name+".sig", sig)
		}
	}
	if cfg.extraMember {
		writeTarMember("etc/passwd", []byte("root:x:0:0\n"))
	}
	_ = tw.Close()
	return out.Bytes()
}

func (f *gemFixture) checksumsGZ(metadataGZ, dataGZ []byte, cfg buildConfig) []byte {
	metaSum := sha512.Sum512(metadataGZ)
	dataSum := sha512.Sum512(dataGZ)
	metaHex := hex.EncodeToString(metaSum[:])
	if cfg.corruptChecksum {
		metaHex = "00" + metaHex[2:]
	}
	dataHex := hex.EncodeToString(dataSum[:])

	var doc string
	if cfg.duplicateChecksumKey {
		doc = "---\nSHA512:\n" +
			"  metadata.gz: " + metaHex + "\n" +
			"  metadata.gz: " + metaHex + "\n" +
			"  data.tar.gz: " + dataHex + "\n"
	} else {
		doc = "---\nSHA512:\n" +
			"  metadata.gz: " + metaHex + "\n" +
			"  data.tar.gz: " + dataHex + "\n"
	}
	return gzipBytes([]byte(doc))
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// rebuildWithForeignDataSig re-signs only data.tar.gz with foreign's leaf key
// while keeping the other signatures from chainA, producing a gem whose
// members claim different signers.
func rebuildWithForeignDataSig(tb testingTB, gem []byte, chainA, foreign *testChain) []byte {
	tb.Helper()

	members, err := ReadMembers(bytes.NewReader(gem), 0)
	if err != nil {
		tb.Fatalf("read gem members: %v", err)
	}

	foreignSig, err := foreign.signDetached(members[MemberData], rsaSHA256Algo)
	if err != nil {
		tb.Fatalf("foreign signature: %v", err)
	}
	members[MemberData+".sig"] = foreignSig

	order := []string{
		MemberMetadata,
		MemberData,
		MemberChecksums,
		MemberMetadata + ".sig",
		MemberData + ".sig",
		MemberChecksums + ".sig",
	}

	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, name := range order {
		content, ok := members[name]
		if !ok {
			continue
		}
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			tb.Fatalf("tar header: %v", err)
		}
		if _, err := io.Copy(tw, bytes.NewReader(content)); err != nil {
			tb.Fatalf("tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		tb.Fatalf("tar close: %v", err)
	}
	return out.Bytes()
}

type testingTB interface {
	Helper()
	Fatalf(format string, args ...any)
}

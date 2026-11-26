package apkverify

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"testing"
)

type testEntry struct {
	name  string
	data  []byte
	extra []byte
}

type testScheme struct {
	scheme       Scheme
	minSDK       uint32
	maxSDK       uint32
	cert         []byte
	lineageCerts [][]byte
}

func lp(data []byte) []byte {
	result := make([]byte, 4, 4+len(data))
	binary.LittleEndian.PutUint32(result, uint32(len(data)))
	return append(result, data...)
}

func u32(value uint32) []byte {
	result := make([]byte, 4)
	binary.LittleEndian.PutUint32(result, value)
	return result
}

func u64(value uint64) []byte {
	result := make([]byte, 8)
	binary.LittleEndian.PutUint64(result, value)
	return result
}

func concat(parts ...[]byte) []byte {
	var result []byte
	for _, part := range parts {
		result = append(result, part...)
	}
	return result
}

func makeLocalRecord(entry testEntry) []byte {
	dataCRC := crc32.ChecksumIEEE(entry.data)
	header := make([]byte, localFileHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], localFileHeaderSignature)
	binary.LittleEndian.PutUint16(header[4:6], 20)
	binary.LittleEndian.PutUint16(header[8:10], 0)
	binary.LittleEndian.PutUint32(header[14:18], dataCRC)
	binary.LittleEndian.PutUint32(header[18:22], uint32(len(entry.data)))
	binary.LittleEndian.PutUint32(header[22:26], uint32(len(entry.data)))
	binary.LittleEndian.PutUint16(header[26:28], uint16(len(entry.name)))
	binary.LittleEndian.PutUint16(header[28:30], uint16(len(entry.extra)))
	return concat(header, []byte(entry.name), entry.extra, entry.data)
}

func makeCentralRecord(entry testEntry, localOffset uint32) []byte {
	dataCRC := crc32.ChecksumIEEE(entry.data)
	header := make([]byte, centralDirHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], centralDirHeaderSignature)
	binary.LittleEndian.PutUint16(header[4:6], 20)
	binary.LittleEndian.PutUint16(header[6:8], 20)
	binary.LittleEndian.PutUint16(header[10:12], 0)
	binary.LittleEndian.PutUint32(header[16:20], dataCRC)
	binary.LittleEndian.PutUint32(header[20:24], uint32(len(entry.data)))
	binary.LittleEndian.PutUint32(header[24:28], uint32(len(entry.data)))
	binary.LittleEndian.PutUint16(header[28:30], uint16(len(entry.name)))
	binary.LittleEndian.PutUint16(header[30:32], uint16(len(entry.extra)))
	binary.LittleEndian.PutUint32(header[42:46], localOffset)
	return concat(header, []byte(entry.name), entry.extra)
}

func assembleAPK(local []byte, central [][]byte, signingBlock []byte, comment string) []byte {
	cdOffset := len(local) + len(signingBlock)
	var cd []byte
	for _, record := range central {
		cd = append(cd, record...)
	}
	eocd := make([]byte, endOfCentralDirSize)
	binary.LittleEndian.PutUint32(eocd[0:4], endOfCentralDirSignature)
	binary.LittleEndian.PutUint16(eocd[8:10], uint16(len(central)))
	binary.LittleEndian.PutUint16(eocd[10:12], uint16(len(central)))
	binary.LittleEndian.PutUint32(eocd[12:16], uint32(len(cd)))
	binary.LittleEndian.PutUint32(eocd[16:20], uint32(cdOffset))
	binary.LittleEndian.PutUint16(eocd[20:22], uint16(len(comment)))
	return concat(local, signingBlock, cd, eocd, []byte(comment))
}

func buildAPK(t testing.TB, entries []testEntry, schemes []testScheme, comment string) []byte {
	t.Helper()
	var local []byte
	offsets := make([]uint32, len(entries))
	for i, entry := range entries {
		offsets[i] = uint32(len(local))
		local = append(local, makeLocalRecord(entry)...)
	}
	central := make([][]byte, len(entries))
	for i, entry := range entries {
		central[i] = makeCentralRecord(entry, offsets[i])
	}
	return assembleAPK(local, central, buildSigningBlock(t, schemes), comment)
}

func buildSigningBlock(t testing.TB, schemes []testScheme) []byte {
	t.Helper()
	if len(schemes) == 0 {
		return nil
	}
	var pairs []byte
	for _, spec := range schemes {
		value := buildSchemeValue(t, spec)
		pairs = append(pairs, u64(uint64(4+len(value)))...)
		pairs = append(pairs, u32(uint32(spec.scheme))...)
		pairs = append(pairs, value...)
	}
	size := uint64(len(pairs) + 24)
	return concat(u64(size), pairs, u64(size), []byte(apkSigningBlockMagic))
}

func buildSchemeValue(t testing.TB, spec testScheme) []byte {
	t.Helper()
	if len(spec.cert) == 0 {
		spec.cert = []byte("test-certificate")
	}
	const algorithm = uint32(0x0201)

	digestRecord := concat(u32(algorithm), lp([]byte("0123456789abcdef0123456789abcdef")))
	digestSequence := lp(lp(digestRecord))
	certSequence := lp(lp(spec.cert))
	signatureRecord := concat(u32(algorithm), lp([]byte("test-signature")))
	signatureSequence := lp(lp(signatureRecord))

	var attrBlock []byte
	if len(spec.lineageCerts) > 0 {
		attrBlock = concat(u32(proofOfRotationAttrID), lp(buildLineageValue(spec.lineageCerts)))
	}
	attrSequence := lp(attrBlock)

	var signedData []byte
	if spec.scheme == SchemeV2 {
		signedData = concat(digestSequence, certSequence, attrSequence)
	} else {
		signedData = concat(digestSequence, certSequence,
			u32(spec.minSDK), u32(spec.maxSDK), attrSequence)
	}

	signer := concat(lp(signedData), signatureSequence, lp([]byte("test-public-key")))
	return lp(lp(signer))
}

func buildLineageValue(certs [][]byte) []byte {
	var nodes []byte
	for _, cert := range certs {
		nodeSignedData := concat(lp(cert), u32(0x0201), u32(0))
		node := concat(lp(nodeSignedData), lp([]byte("lineage-signature")))
		nodes = append(nodes, lp(node)...)
	}
	return concat(u32(1), lp(nodes))
}

func validEntries() []testEntry {
	return []testEntry{
		{name: "AndroidManifest.xml", data: []byte("manifest-bytes")},
		{name: "classes.dex", data: []byte("dex\n035\x00classes")},
	}
}

func validV2Scheme() testScheme {
	return testScheme{scheme: SchemeV2, cert: []byte("v2-cert")}
}

func validV3Schemes() []testScheme {
	return []testScheme{
		{scheme: SchemeV3, minSDK: 28, maxSDK: 32, cert: []byte("v3-cert"),
			lineageCerts: [][]byte{[]byte("v3-cert")}},
		{scheme: SchemeV31, minSDK: 33, maxSDK: math.MaxUint32, cert: []byte("v31-cert"),
			lineageCerts: [][]byte{[]byte("v3-cert"), []byte("v31-cert")}},
	}
}

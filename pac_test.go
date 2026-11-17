package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

var (
	testServerKey = []byte("service-long-term-key!")
	testKDCKey    = []byte("krbtgt-long-term-key!!")
)

// testLogonInfo builds a fake PAC_LOGON_INFO payload whose tail carries
// two group SIDs in binary SID form (revision, sub-authority count,
// 6-byte authority, little-endian sub-authorities).
func testLogonInfo() []byte {
	info := bytes.Repeat([]byte{0x41}, 64) // stand-in for NDR user fields
	info = append(info, testSID(513)...)   // Domain Users
	info = append(info, testSID(514)...)   // Domain Computers
	return info
}

func testSID(subAuth uint32) []byte {
	sid := []byte{1, 2, 0, 0, 0, 0, 0, 5} // S-1-5
	sid = append(sid, 0x20, 0x02, 0, 0)   // 32 (SECURITY_NT_AUTHORITY-ish)
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], subAuth)
	return append(sid, b[:]...)
}

type bufferSpec struct {
	typ  uint32
	data []byte
}

// buildPAC assembles a PAC from the given buffers, computing the server
// and KDC checksums over the PAC with both signature fields zeroed.
func buildPAC(t *testing.T, logonInfo []byte, includeKDC bool) []byte {
	t.Helper()

	sigLen, _ := signatureLength(checksumHMACMD5)
	serverSig := make([]byte, 4+sigLen)
	binary.LittleEndian.PutUint32(serverSig[0:4], checksumHMACMD5)

	specs := []bufferSpec{
		{typ: BufferTypeLogonInfo, data: logonInfo},
		{typ: BufferTypeServerChecksum, data: serverSig},
	}
	if includeKDC {
		kdcSig := make([]byte, 4+sigLen)
		binary.LittleEndian.PutUint32(kdcSig[0:4], checksumHMACMD5)
		specs = append(specs, bufferSpec{typ: BufferTypeKDCChecksum, data: kdcSig})
	}
	return assemblePAC(t, specs, testServerKey, testKDCKey)
}

// assemblePAC lays out buffers, then fills in real checksums for any
// server/KDC checksum buffers present.
func assemblePAC(t *testing.T, specs []bufferSpec, serverKey, kdcKey []byte) []byte {
	t.Helper()

	headerLen := pacHeaderLen + len(specs)*infoBufferLen
	offset := alignUp(headerLen, pacBufferAlign)
	offsets := make([]int, len(specs))
	for i, s := range specs {
		offsets[i] = offset
		offset = alignUp(offset+len(s.data), pacBufferAlign)
	}

	pac := make([]byte, offset)
	binary.LittleEndian.PutUint32(pac[0:4], uint32(len(specs)))
	binary.LittleEndian.PutUint32(pac[4:8], pacVersion)
	for i, s := range specs {
		entry := pac[pacHeaderLen+i*infoBufferLen:]
		binary.LittleEndian.PutUint32(entry[0:4], s.typ)
		binary.LittleEndian.PutUint32(entry[4:8], uint32(len(s.data)))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(offsets[i]))
		copy(pac[offsets[i]:], s.data)
	}

	// Sign: zero both signature fields (as the verifier does), compute
	// each checksum over the zeroed PAC, then write the signatures in.
	sigField := func(typ uint32) (start, length int, ok bool) {
		for i, s := range specs {
			if s.typ == typ {
				sigType := binary.LittleEndian.Uint32(pac[offsets[i]:])
				n, err := signatureLength(sigType)
				if !err {
					t.Fatalf("test spec has unsupported checksum type %d", sigType)
				}
				return offsets[i] + 4, n, true
			}
		}
		return 0, 0, false
	}
	zeroBoth := func() (restore func()) {
		type field struct {
			start int
			saved []byte
		}
		var fields []field
		for _, typ := range []uint32{BufferTypeServerChecksum, BufferTypeKDCChecksum} {
			start, n, ok := sigField(typ)
			if !ok {
				continue
			}
			saved := make([]byte, n)
			copy(saved, pac[start:start+n])
			for i := 0; i < n; i++ {
				pac[start+i] = 0
			}
			fields = append(fields, field{start, saved})
		}
		return func() {
			for _, f := range fields {
				copy(pac[f.start:f.start+len(f.saved)], f.saved)
			}
		}
	}
	sign := func(typ uint32, key []byte) {
		start, _, ok := sigField(typ)
		if !ok {
			return
		}
		sigType := binary.LittleEndian.Uint32(pac[start-4:])
		restore := zeroBoth()
		sum, err := computeChecksum(sigType, key, pac)
		restore()
		if err != nil {
			t.Fatalf("computeChecksum: %v", err)
		}
		copy(pac[start:start+len(sum)], sum)
	}
	sign(BufferTypeServerChecksum, serverKey)
	sign(BufferTypeKDCChecksum, kdcKey)
	return pac
}

func alignUp(n, align int) int {
	if n%align == 0 {
		return n
	}
	return n + align - n%align
}

func logonInfoOffset(t *testing.T, pac []byte) int {
	t.Helper()
	cBuffers := binary.LittleEndian.Uint32(pac[0:4])
	for i := uint32(0); i < cBuffers; i++ {
		entry := pac[pacHeaderLen+int(i)*infoBufferLen:]
		if binary.LittleEndian.Uint32(entry[0:4]) == BufferTypeLogonInfo {
			return int(binary.LittleEndian.Uint64(entry[8:16]))
		}
	}
	t.Fatal("no logon info buffer")
	return 0
}

func TestVerifyValidPAC(t *testing.T) {
	logonInfo := testLogonInfo()
	pac := buildPAC(t, logonInfo, true)

	verified, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if err != nil {
		t.Fatalf("VerifyPAC: %v", err)
	}
	if !bytes.Equal(verified.LogonInfo(), logonInfo) {
		t.Fatal("logon info mismatch after verification")
	}
}

func TestVerifyValidPACWithSHA1(t *testing.T) {
	sigLen, _ := signatureLength(checksumHMACSHA1AES256)
	mkSig := func() []byte {
		b := make([]byte, 4+sigLen)
		binary.LittleEndian.PutUint32(b[0:4], checksumHMACSHA1AES256)
		return b
	}
	specs := []bufferSpec{
		{typ: BufferTypeLogonInfo, data: testLogonInfo()},
		{typ: BufferTypeServerChecksum, data: mkSig()},
		{typ: BufferTypeKDCChecksum, data: mkSig()},
	}
	pac := assemblePAC(t, specs, testServerKey, testKDCKey)
	if _, err := VerifyPAC(pac, testServerKey, testKDCKey); err != nil {
		t.Fatalf("VerifyPAC: %v", err)
	}
}

func TestRejectDuplicateBufferType(t *testing.T) {
	specs := []bufferSpec{
		{typ: BufferTypeLogonInfo, data: testLogonInfo()},
		{typ: BufferTypeLogonInfo, data: testLogonInfo()}, // attacker-appended copy
		{typ: BufferTypeServerChecksum, data: md5SigSpec()},
		{typ: BufferTypeKDCChecksum, data: md5SigSpec()},
	}
	pac := assemblePAC(t, specs, testServerKey, testKDCKey)

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrDuplicateBufferType) {
		t.Fatalf("want ErrDuplicateBufferType, got %v", err)
	}
}

func md5SigLen() int {
	n, _ := signatureLength(checksumHMACMD5)
	return n
}

func md5SigSpec() []byte {
	b := make([]byte, 4+md5SigLen())
	binary.LittleEndian.PutUint32(b[0:4], checksumHMACMD5)
	return b
}

func TestRejectOverlappingBuffers(t *testing.T) {
	pac := buildPAC(t, testLogonInfo(), true)

	// Point the KDC checksum buffer into the middle of the logon info.
	cBuffers := binary.LittleEndian.Uint32(pac[0:4])
	liOff := logonInfoOffset(t, pac)
	for i := uint32(0); i < cBuffers; i++ {
		entry := pac[pacHeaderLen+int(i)*infoBufferLen:]
		if binary.LittleEndian.Uint32(entry[0:4]) == BufferTypeKDCChecksum {
			binary.LittleEndian.PutUint64(entry[8:16], uint64(liOff+8))
		}
	}

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrOverlappingBuffers) {
		t.Fatalf("want ErrOverlappingBuffers, got %v", err)
	}
}

func TestRejectTamperedGroupSID(t *testing.T) {
	logonInfo := testLogonInfo()
	pac := buildPAC(t, logonInfo, true)

	// Attacker rewrites a group RID to 512 (Domain Admins) inside the
	// logon info buffer without knowing either key.
	off := logonInfoOffset(t, pac)
	sidPos := off + 64 + 12 // first SID's sub-authority field
	binary.LittleEndian.PutUint32(pac[sidPos:], 512)

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestRejectForgedServerKeyOnly(t *testing.T) {
	// An attacker who knows only the service key can forge the server
	// checksum but not the KDC checksum.
	logonInfo := testLogonInfo()
	pac := buildPAC(t, logonInfo, true)
	off := logonInfoOffset(t, pac)
	binary.LittleEndian.PutUint32(pac[off+64+12:], 512)

	// Re-sign with the server key only.
	cBuffers := binary.LittleEndian.Uint32(pac[0:4])
	for i := uint32(0); i < cBuffers; i++ {
		entry := pac[pacHeaderLen+int(i)*infoBufferLen:]
		if binary.LittleEndian.Uint32(entry[0:4]) == BufferTypeServerChecksum {
			soff := int(binary.LittleEndian.Uint64(entry[8:16]))
			sigLen := md5SigLen()
			for j := 0; j < sigLen; j++ {
				pac[soff+4+j] = 0
			}
			// KDC field is still the original (now invalid) value; the
			// attacker cannot fix it. Zero it for the server computation
			// as the verifier would.
			for j := uint32(0); j < cBuffers; j++ {
				e2 := pac[pacHeaderLen+int(j)*infoBufferLen:]
				if binary.LittleEndian.Uint32(e2[0:4]) == BufferTypeKDCChecksum {
					koff := int(binary.LittleEndian.Uint64(e2[8:16]))
					saved := make([]byte, sigLen)
					copy(saved, pac[koff+4:koff+4+sigLen])
					for k := 0; k < sigLen; k++ {
						pac[koff+4+k] = 0
					}
					sum, _ := computeChecksum(checksumHMACMD5, testServerKey, pac)
					copy(pac[soff+4:soff+4+sigLen], sum)
					copy(pac[koff+4:koff+4+sigLen], saved)
				}
			}
		}
	}

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestRejectMissingKDCSignature(t *testing.T) {
	pac := buildPAC(t, testLogonInfo(), false)

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrMissingKDCSignature) {
		t.Fatalf("want ErrMissingKDCSignature, got %v", err)
	}
}

func TestRejectMissingServerSignature(t *testing.T) {
	specs := []bufferSpec{
		{typ: BufferTypeLogonInfo, data: testLogonInfo()},
		{typ: BufferTypeKDCChecksum, data: md5SigSpec()},
	}
	pac := assemblePAC(t, specs, testServerKey, testKDCKey)

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrMissingServerSignature) {
		t.Fatalf("want ErrMissingServerSignature, got %v", err)
	}
}

func TestRejectOutOfBoundsBuffer(t *testing.T) {
	pac := buildPAC(t, testLogonInfo(), true)
	cBuffers := binary.LittleEndian.Uint32(pac[0:4])
	for i := uint32(0); i < cBuffers; i++ {
		entry := pac[pacHeaderLen+int(i)*infoBufferLen:]
		if binary.LittleEndian.Uint32(entry[0:4]) == BufferTypeLogonInfo {
			binary.LittleEndian.PutUint32(entry[4:8], uint32(len(pac))*2)
		}
	}

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrMalformedPAC) {
		t.Fatalf("want ErrMalformedPAC, got %v", err)
	}
}

func TestRejectUnalignedOffset(t *testing.T) {
	pac := buildPAC(t, testLogonInfo(), true)
	cBuffers := binary.LittleEndian.Uint32(pac[0:4])
	for i := uint32(0); i < cBuffers; i++ {
		entry := pac[pacHeaderLen+int(i)*infoBufferLen:]
		if binary.LittleEndian.Uint32(entry[0:4]) == BufferTypeLogonInfo {
			off := binary.LittleEndian.Uint64(entry[8:16])
			binary.LittleEndian.PutUint64(entry[8:16], off+1)
		}
	}

	_, err := VerifyPAC(pac, testServerKey, testKDCKey)
	if !errors.Is(err, ErrMalformedPAC) {
		t.Fatalf("want ErrMalformedPAC, got %v", err)
	}
}

func TestRejectTruncatedPAC(t *testing.T) {
	pac := buildPAC(t, testLogonInfo(), true)
	if _, err := VerifyPAC(pac[:len(pac)/2], testServerKey, testKDCKey); !errors.Is(err, ErrMalformedPAC) {
		t.Fatalf("want ErrMalformedPAC, got %v", err)
	}
}

func TestRejectWrongKeys(t *testing.T) {
	pac := buildPAC(t, testLogonInfo(), true)
	if _, err := VerifyPAC(pac, []byte("wrong-server-key"), testKDCKey); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature for wrong server key, got %v", err)
	}
	if _, err := VerifyPAC(pac, testServerKey, []byte("wrong-kdc-key")); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature for wrong KDC key, got %v", err)
	}
}

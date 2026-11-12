package netsec

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

const testSignatureType uint32 = 12 // hmac-sha2-256

var (
	testServerKey = []byte("0123456789abcdef0123456789abcdef")
	testKDCKey    = []byte("fedcba9876543210fedcba9876543210")
	testDomainSID = SID{
		Revision:       1,
		Authority:      [6]byte{0, 0, 0, 0, 0, 5},
		SubAuthorities: []uint32{21, 100, 200, 300},
	}
	testDomainUsersSID  = relativeSID(testDomainSID, 513)
	testDomainAdminsSID = relativeSID(testDomainSID, 512)
)

func TestAcceptValidPAC(t *testing.T) {
	const userID = 1101
	const primaryGroup = 513
	groups := []uint32{513, 1100}

	logon := buildLogonInfo(t, userID, primaryGroup, groups, nil)
	raw, descs := buildSignedPAC(t, logon, true)

	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	info, err := validator.Accept(raw)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if info.UserID != userID {
		t.Fatalf("UserID = %d, want %d", info.UserID, userID)
	}
	if info.PrimaryGroupID != primaryGroup {
		t.Fatalf("PrimaryGroupID = %d, want %d", info.PrimaryGroupID, primaryGroup)
	}
	if len(info.GroupIDs) != len(groups) {
		t.Fatalf("GroupIDs = %v, want %v", info.GroupIDs, groups)
	}
	for i := range groups {
		if info.GroupIDs[i] != groups[i] {
			t.Fatalf("GroupIDs = %v, want %v", info.GroupIDs, groups)
		}
	}
	if got, want := info.UserSID.String(), relativeSID(testDomainSID, userID).String(); got != want {
		t.Fatalf("UserSID = %q, want %q", got, want)
	}
	if got, want := info.PrimaryGroupSID.String(), testDomainUsersSID.String(); got != want {
		t.Fatalf("PrimaryGroupSID = %q, want %q", got, want)
	}
	if len(info.GroupSIDs) != len(groups) {
		t.Fatalf("GroupSIDs = %v, want %d entries", info.GroupSIDs, len(groups))
	}
	if got, want := info.GroupSIDs[0].String(), testDomainUsersSID.String(); got != want {
		t.Fatalf("GroupSIDs[0] = %q, want %q", got, want)
	}
	if info.HasGroupSID(testDomainAdminsSID) {
		t.Fatalf("low-privilege PAC unexpectedly contains %s", testDomainAdminsSID)
	}

	if _, ok := descs[PACTypeKDCChecksum]; !ok {
		t.Fatal("valid PAC has no KDC signature")
	}
}

func TestParseRejectsDuplicateBufferTypes(t *testing.T) {
	raw := rawPAC([]PACInfoBuffer{
		{Type: PACTypeLogonInfo, Offset: 40, Size: 16},
		{Type: PACTypeLogonInfo, Offset: 56, Size: 16},
	}, 72)

	if _, err := ParsePAC(raw); !errors.Is(err, ErrDuplicatePACBuffer) {
		t.Fatalf("ParsePAC() error = %v, want %v", err, ErrDuplicatePACBuffer)
	}

	raw = rawPAC([]PACInfoBuffer{
		{Type: PACTypeServerChecksum, Offset: 40, Size: 20},
		{Type: PACTypeServerChecksum, Offset: 60, Size: 20},
	}, 80)
	if _, err := ParsePAC(raw); !errors.Is(err, ErrDuplicatePACBuffer) {
		t.Fatalf("ParsePAC() error = %v, want %v", err, ErrDuplicatePACBuffer)
	}
}

func TestParseRejectsOverlappingBuffers(t *testing.T) {
	raw := rawPAC([]PACInfoBuffer{
		{Type: PACTypeLogonInfo, Offset: 40, Size: 32},
		{Type: PACTypeCredentialInfo, Offset: 48, Size: 32},
	}, 80)

	if _, err := ParsePAC(raw); !errors.Is(err, ErrOverlappingPACBuffers) {
		t.Fatalf("ParsePAC() error = %v, want %v", err, ErrOverlappingPACBuffers)
	}

	// Adjacent ranges (end == next start) are legal.
	raw = rawPAC([]PACInfoBuffer{
		{Type: PACTypeLogonInfo, Offset: 40, Size: 16},
		{Type: PACTypeCredentialInfo, Offset: 56, Size: 16},
	}, 72)
	if _, err := ParsePAC(raw); err != nil {
		t.Fatalf("ParsePAC() adjacent ranges error = %v", err)
	}
}

func TestParseRejectsOutOfBoundsAndOverflow(t *testing.T) {
	tests := []struct {
		name string
		desc PACInfoBuffer
		size uint64
	}{
		{
			name: "offset in descriptor table",
			desc: PACInfoBuffer{Type: PACTypeLogonInfo, Offset: 8, Size: 8},
			size: 64,
		},
		{
			name: "offset at end",
			desc: PACInfoBuffer{Type: PACTypeLogonInfo, Offset: 64, Size: 8},
			size: 64,
		},
		{
			name: "size crosses end",
			desc: PACInfoBuffer{Type: PACTypeLogonInfo, Offset: 60, Size: 8},
			size: 64,
		},
		{
			name: "offset plus size overflows",
			desc: PACInfoBuffer{Type: PACTypeLogonInfo, Offset: ^uint64(0) - 4, Size: 8},
			size: 64,
		},
		{
			name: "zero length",
			desc: PACInfoBuffer{Type: PACTypeLogonInfo, Offset: 40, Size: 0},
			size: 64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := rawPAC([]PACInfoBuffer{tt.desc}, tt.size)
			if _, err := ParsePAC(raw); !errors.Is(err, ErrMalformedPAC) {
				t.Fatalf("ParsePAC() error = %v, want %v", err, ErrMalformedPAC)
			}
		})
	}
}

func TestAcceptRejectsChangedGroupSID(t *testing.T) {
	logon := buildLogonInfo(t, 1101, 513, []uint32{513, 1100}, nil)
	raw, descs := buildSignedPAC(t, logon, true)

	// Change the second group RID from 1100 to the domain administrator
	// group RID 512 without changing any signature.
	groupIndex := 1
	logonOff := descs[PACTypeLogonInfo].Offset
	ridOff := logonOff + uint64(validationGroupIDsOffset) + uint64(4*groupIndex)
	binary.LittleEndian.PutUint32(raw[ridOff:ridOff+4], 512)

	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	info, err := validator.Accept(raw)
	if info != nil {
		t.Fatal("Accept() returned SIDs from a PAC with a tampered group SID")
	}
	if !errors.Is(err, ErrInvalidPACSignature) || !strings.Contains(err.Error(), "server") {
		t.Fatalf("Accept() error = %v, want server signature failure", err)
	}
}

func TestAcceptRejectsServerRekeyedGroupSIDWithoutValidKDC(t *testing.T) {
	// This models a compromised service key: the attacker changes the group
	// SID and recomputes the server signature, but cannot forge the KDC
	// signature over the new server signature.
	logon := buildLogonInfo(t, 1101, 513, []uint32{513, 1100}, nil)
	raw, descs := buildSignedPAC(t, logon, true)

	logonOff := descs[PACTypeLogonInfo].Offset
	ridOff := logonOff + validationGroupIDsOffset + 4 // second group
	binary.LittleEndian.PutUint32(raw[ridOff:ridOff+4], 512)
	writeServerSignature(t, raw, descs)

	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	info, err := validator.Accept(raw)
	if info != nil {
		t.Fatal("Accept() returned SIDs after only the server signature was re-created")
	}
	if !errors.Is(err, ErrInvalidPACSignature) || !strings.Contains(err.Error(), "KDC") {
		t.Fatalf("Accept() error = %v, want KDC signature failure", err)
	}
}

func TestAcceptRejectsMissingKDCSignature(t *testing.T) {
	logon := buildLogonInfo(t, 1101, 513, []uint32{513, 1100}, nil)
	raw, descs := buildSignedPAC(t, logon, false)
	writeServerSignature(t, raw, descs)

	p, err := ParsePAC(raw)
	if err != nil {
		t.Fatalf("ParsePAC() error = %v", err)
	}
	if err := p.VerifyServerSignature(testServerKey, PACKeyUsage); err != nil {
		t.Fatalf("server signature should be valid: %v", err)
	}
	if _, err := p.LogonInfo(); !errors.Is(err, ErrUnverifiedPAC) {
		t.Fatalf("LogonInfo() error = %v, want %v", err, ErrUnverifiedPAC)
	}

	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	info, err := validator.Accept(raw)
	if info != nil {
		t.Fatal("Accept() returned SIDs from a PAC without a KDC signature")
	}
	if !errors.Is(err, ErrMissingKDCSignature) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrMissingKDCSignature)
	}
}

func TestAcceptRejectsWrongKeys(t *testing.T) {
	logon := buildLogonInfo(t, 1101, 513, []uint32{513, 1100}, nil)
	raw, _ := buildSignedPAC(t, logon, true)

	validator := &Validator{
		ServerKey: []byte("wrong-server-key"),
		KDCKey:    testKDCKey,
	}
	if _, err := validator.Accept(raw); !errors.Is(err, ErrInvalidPACSignature) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrInvalidPACSignature)
	}
}

func TestAcceptKDCSignatureInputForms(t *testing.T) {
	logon := buildLogonInfo(t, 1101, 513, []uint32{513, 1100}, nil)

	tests := []struct {
		name  string
		input func(t *testing.T, raw []byte, descs map[PACType]PACInfoBuffer) []byte
	}{
		{
			name: "server signature value",
			input: func(t *testing.T, raw []byte, descs map[PACType]PACInfoBuffer) []byte {
				server := descs[PACTypeServerChecksum]
				return raw[server.Offset+4 : server.Offset+uint64(server.Size)]
			},
		},
		{
			name: "server checksum buffer",
			input: func(t *testing.T, raw []byte, descs map[PACType]PACInfoBuffer) []byte {
				server := descs[PACTypeServerChecksum]
				return raw[server.Offset : server.Offset+uint64(server.Size)]
			},
		},
		{
			name: "zeroed PAC",
			input: func(t *testing.T, raw []byte, descs map[PACType]PACInfoBuffer) []byte {
				signed := append([]byte(nil), raw...)
				zeroSignature(t, signed, descs[PACTypeServerChecksum])
				zeroSignature(t, signed, descs[PACTypeKDCChecksum])
				return signed
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, descs := buildSignedPAC(t, logon, true)
			kdc := descs[PACTypeKDCChecksum]
			sigType := binary.LittleEndian.Uint32(raw[kdc.Offset : kdc.Offset+4])
			sum, err := DefaultChecksum(sigType, testKDCKey, tt.input(t, raw, descs), PACKeyUsage)
			if err != nil {
				t.Fatalf("KDC checksum: %v", err)
			}
			copy(raw[kdc.Offset+4:kdc.Offset+uint64(kdc.Size)], sum)

			validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
			if _, err := validator.Accept(raw); err != nil {
				t.Fatalf("Accept() error = %v", err)
			}
		})
	}
}

func TestAcceptRejectsUnsupportedSignatureType(t *testing.T) {
	logon := buildLogonInfo(t, 1101, 513, []uint32{513}, nil)
	const unsupported = 99

	server := newSignatureBuffer(unsupported)
	kdc := newSignatureBuffer(unsupported)
	raw, _ := layoutPAC([]testPACBuffer{
		{typ: PACTypeLogonInfo, data: logon},
		{typ: PACTypeServerChecksum, data: server},
		{typ: PACTypeKDCChecksum, data: kdc},
	})

	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	_, err := validator.Accept(raw)
	if !errors.Is(err, ErrUnsupportedChecksum) {
		t.Fatalf("Accept() error = %v, want %v", err, ErrUnsupportedChecksum)
	}
}

func TestAcceptAES256Signature(t *testing.T) {
	logon := buildLogonInfo(t, 1101, 513, []uint32{513, 1100}, nil)
	raw, _ := buildSignedPACSig(t, logon, 16, true) // hmac-sha1-96-aes256

	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	if _, err := validator.Accept(raw); err != nil {
		t.Fatalf("Accept() with AES256 signature error = %v", err)
	}
}

func TestAcceptParsesAndProtectsExtraSIDs(t *testing.T) {
	users := SID{
		Revision:       1,
		Authority:      [6]byte{0, 0, 0, 0, 0, 5},
		SubAuthorities: []uint32{32, 545}, // BUILTIN\Users
	}
	logon := buildLogonInfo(t, 1101, 513, []uint32{513}, []SID{users})

	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	raw, descs := buildSignedPAC(t, logon, true)
	info, err := validator.Accept(raw)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if len(info.ExtraSIDs) != 1 || !info.ExtraSIDs[0].Equal(users) {
		t.Fatalf("ExtraSIDs = %v, want %s", info.ExtraSIDs, users)
	}

	// Turn BUILTIN\Users into BUILTIN\Administrators (S-1-5-32-544) without
	// recomputing either signature.
	groupCount := len(info.GroupIDs)
	domainOff := uint64(validationGroupIDsOffset) + uint64(4*groupCount) + 36
	sidEnd := domainOff + uint64(len(testDomainSID.Bytes()))
	extraArrayOff := sidEnd + 56
	extraSIDOff := binary.LittleEndian.Uint32(logon[extraArrayOff : extraArrayOff+4])
	lastSubAuthority := uint64(extraSIDOff) + 8 + 4 // second of two RIDs
	logonOff := descs[PACTypeLogonInfo].Offset
	rawOff := logonOff + lastSubAuthority
	binary.LittleEndian.PutUint32(raw[rawOff:rawOff+4], 544)

	info, err = validator.Accept(raw)
	if info != nil || !errors.Is(err, ErrInvalidPACSignature) {
		t.Fatalf("Accept() tampered ExtraSID = (%v, %v), want nil and %v", info, err, ErrInvalidPACSignature)
	}
}

func TestParsePACDoesNotAliasInput(t *testing.T) {
	logon := buildLogonInfo(t, 1101, 513, []uint32{513}, nil)
	raw, _ := buildSignedPAC(t, logon, true)

	p, err := ParsePAC(raw)
	if err != nil {
		t.Fatalf("ParsePAC() error = %v", err)
	}
	// Corrupt the caller's slice after parsing; a verified/parsed PAC must
	// still refer to its private copy.
	for i := range raw {
		raw[i] = 0
	}
	validator := &Validator{ServerKey: testServerKey, KDCKey: testKDCKey}
	// The validator receives a zeroed input here, so use the parsed PAC.
	if err := p.VerifyServerSignature(testServerKey, PACKeyUsage); err != nil {
		t.Fatalf("parsed PAC was aliased to caller data: %v", err)
	}
	if err := p.VerifyKDCSignature(testKDCKey, PACKeyUsage); err != nil {
		t.Fatalf("parsed PAC was aliased to caller data: %v", err)
	}
	if _, err := validator.Accept(raw); err == nil {
		t.Fatal("Accept() succeeded on corrupted input")
	}
}

type testPACBuffer struct {
	typ  PACType
	data []byte
}

func layoutPAC(bufs []testPACBuffer) ([]byte, map[PACType]PACInfoBuffer) {
	headerEnd := uint64(pacHeaderSize + pacInfoBufferSize*len(bufs))
	offsets := make([]uint64, len(bufs))
	next := headerEnd
	total := headerEnd
	for i, b := range bufs {
		next = (next + 7) &^ 7
		offsets[i] = next
		next += uint64(len(b.data))
		total = next
	}

	raw := make([]byte, total)
	binary.LittleEndian.PutUint32(raw[0:4], uint32(len(bufs)))
	binary.LittleEndian.PutUint32(raw[4:8], 0)

	descs := make(map[PACType]PACInfoBuffer, len(bufs))
	for i, b := range bufs {
		desc := uint64(pacHeaderSize) + uint64(i)*pacInfoBufferSize
		binary.LittleEndian.PutUint32(raw[desc:desc+4], uint32(b.typ))
		binary.LittleEndian.PutUint32(raw[desc+4:desc+8], uint32(len(b.data)))
		binary.LittleEndian.PutUint64(raw[desc+8:desc+16], offsets[i])
		copy(raw[offsets[i]:], b.data)
		descs[b.typ] = PACInfoBuffer{Type: b.typ, Offset: offsets[i], Size: uint32(len(b.data))}
	}
	return raw, descs
}

func rawPAC(descs []PACInfoBuffer, total uint64) []byte {
	raw := make([]byte, total)
	binary.LittleEndian.PutUint32(raw[0:4], uint32(len(descs)))
	binary.LittleEndian.PutUint32(raw[4:8], 0)
	for i, d := range descs {
		desc := uint64(pacHeaderSize) + uint64(i)*pacInfoBufferSize
		binary.LittleEndian.PutUint32(raw[desc:desc+4], uint32(d.Type))
		binary.LittleEndian.PutUint32(raw[desc+4:desc+8], d.Size)
		binary.LittleEndian.PutUint64(raw[desc+8:desc+16], d.Offset)
	}
	return raw
}

func buildSignedPAC(t *testing.T, logon []byte, withKDC bool) ([]byte, map[PACType]PACInfoBuffer) {
	return buildSignedPACSig(t, logon, testSignatureType, withKDC)
}

func buildSignedPACSig(t *testing.T, logon []byte, sigType uint32, withKDC bool) ([]byte, map[PACType]PACInfoBuffer) {
	t.Helper()

	bufs := []testPACBuffer{
		{typ: PACTypeLogonInfo, data: logon},
		{typ: PACTypeServerChecksum, data: newSignatureBuffer(sigType)},
	}
	if withKDC {
		bufs = append(bufs, testPACBuffer{typ: PACTypeKDCChecksum, data: newSignatureBuffer(sigType)})
	}
	raw, descs := layoutPAC(bufs)
	writeServerSignature(t, raw, descs)
	if withKDC {
		writeKDCSignature(t, raw, descs)
	}
	return raw, descs
}

func newSignatureBuffer(sigType uint32) []byte {
	size, err := SignatureSize(sigType)
	if err != nil {
		// Tests only use this helper with supported types.
		size = 32
	}
	data := make([]byte, 4+size)
	binary.LittleEndian.PutUint32(data[:4], sigType)
	return data
}

func writeServerSignature(t *testing.T, raw []byte, descs map[PACType]PACInfoBuffer) {
	t.Helper()
	server := descs[PACTypeServerChecksum]
	sigType := binary.LittleEndian.Uint32(raw[server.Offset : server.Offset+4])

	signed := append([]byte(nil), raw...)
	zeroSignature(t, signed, descs[PACTypeServerChecksum])
	if kdc, ok := descs[PACTypeKDCChecksum]; ok {
		zeroSignature(t, signed, kdc)
	}
	sum, err := DefaultChecksum(sigType, testServerKey, signed, PACKeyUsage)
	if err != nil {
		t.Fatalf("server checksum: %v", err)
	}
	copy(raw[server.Offset+4:server.Offset+uint64(server.Size)], sum)
}

func writeKDCSignature(t *testing.T, raw []byte, descs map[PACType]PACInfoBuffer) {
	t.Helper()
	kdc := descs[PACTypeKDCChecksum]
	server := descs[PACTypeServerChecksum]
	sigType := binary.LittleEndian.Uint32(raw[kdc.Offset : kdc.Offset+4])

	sum, err := DefaultChecksum(sigType, testKDCKey, raw[server.Offset:server.Offset+uint64(server.Size)], PACKeyUsage)
	if err != nil {
		t.Fatalf("KDC checksum: %v", err)
	}
	copy(raw[kdc.Offset+4:kdc.Offset+uint64(kdc.Size)], sum)
}

func zeroSignature(t *testing.T, raw []byte, desc PACInfoBuffer) {
	t.Helper()
	start := desc.Offset + 4
	end := desc.Offset + uint64(desc.Size)
	if end > uint64(len(raw)) {
		t.Fatalf("signature descriptor is out of bounds")
	}
	clear(raw[start:end])
}

func buildLogonInfo(t *testing.T, userID, primaryGroup uint32, groups []uint32, extras []SID) []byte {
	t.Helper()

	groupCount := uint64(len(groups))
	domainOff := uint64(validationGroupIDsOffset) + 4*groupCount + 36
	domainBytes := testDomainSID.Bytes()
	sidEnd := domainOff + uint64(len(domainBytes))

	const tailSize = 56 // fields after LogonDomainId through ResourceGroupIds
	extraArrayOff := sidEnd + tailSize
	extraArraySize := uint64(8 * len(extras))
	extraSIDOff := extraArrayOff + extraArraySize
	total := extraSIDOff
	for _, sid := range extras {
		total += uint64(len(sid.Bytes()))
	}

	data := make([]byte, total)
	binary.LittleEndian.PutUint32(data[84:88], userID)
	binary.LittleEndian.PutUint32(data[88:92], primaryGroup)
	binary.LittleEndian.PutUint32(data[validationGroupsCountOffset:validationGroupsCountOffset+4], uint32(groupCount))
	for i, rid := range groups {
		off := uint64(validationGroupIDsOffset) + 4*uint64(i)
		binary.LittleEndian.PutUint32(data[off:off+4], rid)
	}

	copy(data[domainOff:], domainBytes)
	binary.LittleEndian.PutUint32(data[sidEnd+36:sidEnd+40], uint32(len(extras)))
	if len(extras) > 0 {
		binary.LittleEndian.PutUint32(data[sidEnd+40:sidEnd+44], uint32(extraArrayOff))
	}

	nextSID := extraSIDOff
	for i, sid := range extras {
		entry := extraArrayOff + uint64(8*i)
		binary.LittleEndian.PutUint32(data[entry:entry+4], uint32(nextSID))
		binary.LittleEndian.PutUint32(data[entry+4:entry+8], 0)
		sidBytes := sid.Bytes()
		copy(data[nextSID:], sidBytes)
		nextSID += uint64(len(sidBytes))
	}

	return data
}

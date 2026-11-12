package netsec

import (
	"encoding/binary"
	"errors"
	"testing"
)

func encodeAVP(code uint32, flags uint8, vendorID uint32, value []byte) []byte {
	headerLen := AVPHeaderLen
	if flags&FlagVendor != 0 {
		headerLen = AVPVendorHeaderLen
	}
	length := headerLen + len(value)
	out := make([]byte, pad4(length))
	binary.BigEndian.PutUint32(out[0:4], code)
	out[4] = flags
	putUint24(out[5:8], uint32(length))
	if flags&FlagVendor != 0 {
		binary.BigEndian.PutUint32(out[8:12], vendorID)
	}
	copy(out[headerLen:length], value)
	return out
}

func nestedGroups(levels int, innermost []byte) []byte {
	out := innermost
	for i := 0; i < levels; i++ {
		out = encodeAVP(CodeFailedAVP, FlagMandatory, 0, out)
	}
	return out
}

func TestDecodeValidMessageKeepsUnknownOptionalAVP(t *testing.T) {
	sessionID := encodeAVP(263, FlagMandatory, 0, []byte("session-1"))
	originHost := encodeAVP(264, FlagMandatory, 0, []byte("proxy.example.net"))
	unknownOptional := encodeAVP(9999, 0, 0, []byte("keep"))
	unknownVendorOptional := encodeAVP(700, FlagVendor, 4242, []byte{1, 2, 3, 4})

	message := append(append(append(sessionID, originHost...), unknownOptional...), unknownVendorOptional...)

	avps, err := DecodeAVPs(message)
	if err != nil {
		t.Fatalf("DecodeAVPs returned error: %v", err)
	}
	if len(avps) != 4 {
		t.Fatalf("got %d AVPs, want 4", len(avps))
	}
	if avps[0].Code != 263 || avps[0].Value == nil || string(avps[0].Value) != "session-1" {
		t.Fatalf("unexpected Session-Id AVP: %+v", avps[0])
	}
	if avps[2].Code != 9999 || avps[2].Mandatory() {
		t.Fatalf("unknown optional AVP was not preserved: %+v", avps[2])
	}
	if string(avps[2].Value) != "keep" {
		t.Fatalf("unknown optional value changed: %q", avps[2].Value)
	}
	if avps[3].Code != 700 || avps[3].VendorID != 4242 || avps[3].Mandatory() {
		t.Fatalf("unknown vendor optional AVP was not preserved: %+v", avps[3])
	}
	if string(avps[2].Raw) != string(unknownOptional) {
		t.Fatalf("unknown optional raw bytes changed")
	}
}

func TestUnknownMandatoryReturns5001WithFailedAVP(t *testing.T) {
	offending := encodeAVP(9999, FlagMandatory, 0, []byte("required"))

	_, err := Decode(offending)
	if err == nil {
		t.Fatal("Decode accepted an unknown mandatory AVP")
	}

	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error type = %T, want *DecodeError", err)
	}
	if decodeErr.ResultCode != ResultCodeAVPUnsupported || decodeErr.Code != 5001 {
		t.Fatalf("Result-Code = %d/%d, want 5001", decodeErr.ResultCode, decodeErr.Code)
	}
	if !errors.Is(decodeErr, ErrUnsupportedAVP) {
		t.Fatalf("cause = %v, want ErrUnsupportedAVP", decodeErr.Unwrap())
	}
	if decodeErr.OffendingAVP == nil || decodeErr.OffendingAVP.Code != 9999 {
		t.Fatalf("offending AVP = %+v, want code 9999", decodeErr.OffendingAVP)
	}
	if decodeErr.FailedAVP == nil || decodeErr.FailedAVP.Code != CodeFailedAVP {
		t.Fatalf("Failed-AVP = %+v, want code 279", decodeErr.FailedAVP)
	}
	if !decodeErr.FailedAVP.Mandatory() {
		t.Fatal("Failed-AVP does not have the M bit set")
	}

	failed := decodeErr.FailedAVPBytes()
	if len(failed) == 0 {
		t.Fatal("Failed-AVP bytes are empty")
	}
	if binary.BigEndian.Uint32(failed[0:4]) != CodeFailedAVP {
		t.Fatalf("Failed-AVP outer code = %d", binary.BigEndian.Uint32(failed[0:4]))
	}
	if binary.BigEndian.Uint32(failed[8:12]) != 9999 {
		t.Fatalf("Failed-AVP inner code = %d, want 9999", binary.BigEndian.Uint32(failed[8:12]))
	}

	response := decodeErr.ResponseAVPs()
	if len(response) != 2 || response[0].Code != CodeResultCode || response[1].Code != CodeFailedAVP {
		t.Fatalf("unexpected response AVPs: %+v", response)
	}
	if got := binary.BigEndian.Uint32(response[0].Value); got != 5001 {
		t.Fatalf("Result-Code AVP value = %d, want 5001", got)
	}
}

func TestUnknownMandatoryWithClearedMBitIsForwarded(t *testing.T) {
	optional := encodeAVP(9999, 0, 0, []byte("optional"))

	avps, err := Decode(optional)
	if err != nil {
		t.Fatalf("Decode rejected an unknown optional AVP: %v", err)
	}
	if len(avps) != 1 {
		t.Fatalf("got %d AVPs, want 1", len(avps))
	}
	if avps[0].Code != 9999 || avps[0].Flags != 0 {
		t.Fatalf("optional AVP was not preserved: %+v", avps[0])
	}
	if string(avps[0].Raw) != string(optional) {
		t.Fatal("unknown optional AVP raw bytes changed")
	}
}

func TestAVPLengthBeyondBufferIsRejected(t *testing.T) {
	raw := encodeAVP(9999, 0, 0, []byte("short"))
	// Claim a payload far beyond the available buffer while keeping M clear.
	putUint24(raw[5:8], uint32(len(raw)+100))

	_, err := Decode(raw)
	if err == nil {
		t.Fatal("Decode accepted an AVP whose length exceeds the buffer")
	}
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error type = %T, want *DecodeError", err)
	}
	if decodeErr.ResultCode != ResultCodeInvalidAVPLength {
		t.Fatalf("Result-Code = %d, want %d", decodeErr.ResultCode, ResultCodeInvalidAVPLength)
	}
	if decodeErr.FailedAVP == nil {
		t.Fatal("invalid-length error has no Failed-AVP")
	}
	if !errors.Is(decodeErr, ErrInvalidAVPLength) {
		t.Fatalf("cause = %v, want ErrInvalidAVPLength", decodeErr.Unwrap())
	}
}

func TestNestedAVPCannotCrossParentBoundary(t *testing.T) {
	// The child claims a 64-byte length but the parent only carries its header.
	child := make([]byte, AVPHeaderLen)
	binary.BigEndian.PutUint32(child[0:4], 263)
	child[4] = FlagMandatory
	putUint24(child[5:8], 64)

	parent := encodeAVP(CodeFailedAVP, FlagMandatory, 0, child)

	_, err := Decode(parent)
	if err == nil {
		t.Fatal("Decode accepted a nested AVP that crosses its parent boundary")
	}
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error type = %T, want *DecodeError", err)
	}
	if decodeErr.ResultCode != ResultCodeInvalidAVPLength {
		t.Fatalf("Result-Code = %d, want %d", decodeErr.ResultCode, ResultCodeInvalidAVPLength)
	}
	if decodeErr.OffendingAVP == nil || decodeErr.OffendingAVP.Code != 263 {
		t.Fatalf("offending AVP = %+v, want code 263", decodeErr.OffendingAVP)
	}
}

func TestNestedAVPDoesNotConsumeFollowingTopLevelBytes(t *testing.T) {
	// The child claims 16 bytes, which are available in the total message but
	// belong to the next top-level AVP. Only the parent boundary is relevant.
	child := make([]byte, AVPHeaderLen)
	binary.BigEndian.PutUint32(child[0:4], 263)
	child[4] = FlagMandatory
	putUint24(child[5:8], 16)

	parent := encodeAVP(CodeFailedAVP, FlagMandatory, 0, child)
	following := encodeAVP(263, FlagMandatory, 0, []byte("outside"))
	message := append(parent, following...)

	_, err := Decode(message)
	if err == nil {
		t.Fatal("Decode let a nested AVP read into the following top-level AVP")
	}
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error type = %T, want *DecodeError", err)
	}
	if decodeErr.ResultCode != ResultCodeInvalidAVPLength {
		t.Fatalf("Result-Code = %d, want %d", decodeErr.ResultCode, ResultCodeInvalidAVPLength)
	}
}

func TestGroupedDepthLimit(t *testing.T) {
	leaf := encodeAVP(263, FlagMandatory, 0, []byte("leaf"))

	ok := nestedGroups(MaxGroupedDepth, leaf)
	avps, err := Decode(ok)
	if err != nil {
		t.Fatalf("Decode rejected %d Grouped levels: %v", MaxGroupedDepth, err)
	}
	if got := groupedDepth(avps[0]); got != MaxGroupedDepth {
		t.Fatalf("decoded grouped depth = %d, want %d", got, MaxGroupedDepth)
	}

	tooDeep := nestedGroups(MaxGroupedDepth+1, leaf)
	_, err = Decode(tooDeep)
	if err == nil {
		t.Fatal("Decode accepted more than the maximum Grouped nesting depth")
	}
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error type = %T, want *DecodeError", err)
	}
	if !errors.Is(decodeErr, ErrGroupedTooDeep) {
		t.Fatalf("cause = %v, want ErrGroupedTooDeep", decodeErr.Unwrap())
	}
	if decodeErr.FailedAVP == nil {
		t.Fatal("nesting-depth error has no Failed-AVP")
	}
}

func groupedDepth(a *AVP) int {
	if a == nil || !a.Grouped {
		return 0
	}
	maxChild := 0
	for _, child := range a.Children {
		if d := groupedDepth(child); d > maxChild {
			maxChild = d
		}
	}
	return 1 + maxChild
}

func TestLengthIncludesHeaderAndPadding(t *testing.T) {
	// Three payload bytes make an 11-byte AVP, which must occupy 12 bytes.
	first := encodeAVP(9999, 0, 0, []byte{1, 2, 3})
	second := encodeAVP(263, FlagMandatory, 0, []byte("next"))
	message := append(first, second...)

	avps, err := Decode(message)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(avps) != 2 {
		t.Fatalf("got %d AVPs, want 2", len(avps))
	}
	if len(avps[0].Raw) != 12 {
		t.Fatalf("padded AVP length = %d, want 12", len(avps[0].Raw))
	}
	if got := getUint24(avps[0].Raw[5:8]); got != 11 {
		t.Fatalf("AVP length field = %d, want 11 (header included, padding excluded)", got)
	}
	if avps[1].Code != 263 {
		t.Fatalf("second AVP code = %d, want 263", avps[1].Code)
	}

	// The declared length is valid but the final padding byte is absent.
	if _, err := Decode(first[:11]); err == nil {
		t.Fatal("Decode accepted an AVP without its required padding")
	}
}

func TestVendorFlagAndVendorIDMustAgree(t *testing.T) {
	bad := make([]byte, AVPVendorHeaderLen)
	binary.BigEndian.PutUint32(bad[0:4], 9999)
	bad[4] = FlagVendor // V set, but Vendor-Id remains zero.
	putUint24(bad[5:8], AVPVendorHeaderLen)

	_, err := Decode(bad)
	if err == nil {
		t.Fatal("Decode accepted the V bit with Vendor-Id 0")
	}
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error type = %T, want *DecodeError", err)
	}
	if !errors.Is(decodeErr, ErrInvalidVendorID) {
		t.Fatalf("cause = %v, want ErrInvalidVendorID", decodeErr.Unwrap())
	}

	good := encodeAVP(700, FlagVendor, 4242, []byte{1, 2, 3, 4})
	avps, err := Decode(good)
	if err != nil {
		t.Fatalf("Decode rejected a consistent vendor AVP: %v", err)
	}
	if avps[0].VendorID != 4242 || !avps[0].HasVendor() {
		t.Fatalf("decoded vendor AVP = %+v", avps[0])
	}
}

func TestGroupedAVPParsesChildrenWithinParent(t *testing.T) {
	child := encodeAVP(263, FlagMandatory, 0, []byte("child"))
	parent := encodeAVP(CodeFailedAVP, FlagMandatory, 0, child)

	avps, err := Decode(parent)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if !avps[0].Grouped || len(avps[0].Children) != 1 {
		t.Fatalf("grouped AVP = %+v, want one child", avps[0])
	}
	if avps[0].Children[0].Code != 263 {
		t.Fatalf("child code = %d, want 263", avps[0].Children[0].Code)
	}
}

func TestKnownScalarAVPIsNotParsedAsGrouped(t *testing.T) {
	// CC-Total-Octets (421) is a scalar; its value is not an AVP list.
	scalar := encodeAVP(421, FlagMandatory, 0, []byte{0, 0, 0, 0, 0, 0, 1, 44})

	avps, err := Decode(scalar)
	if err != nil {
		t.Fatalf("Decode rejected a known scalar AVP: %v", err)
	}
	if avps[0].Grouped || len(avps[0].Children) != 0 {
		t.Fatalf("scalar AVP was parsed as Grouped: %+v", avps[0])
	}
}

func TestCustomDictionarySupportsApplicationGroupedAVP(t *testing.T) {
	const appGroupCode uint32 = 1234
	decoder := NewDecoderWithKnown(map[AVPKey]AVPInfo{
		{Code: appGroupCode}: {Name: "Application-Group", Grouped: true},
		{Code: 263}:          {Name: "Session-Id"},
	})

	child := encodeAVP(263, FlagMandatory, 0, []byte("known-after-custom"))
	parent := encodeAVP(appGroupCode, FlagMandatory, 0, child)

	avps, err := decoder.Decode(parent)
	if err != nil {
		t.Fatalf("custom Grouped AVP was rejected: %v", err)
	}
	if !avps[0].Grouped || len(avps[0].Children) != 1 {
		t.Fatalf("custom Grouped AVP children = %+v", avps[0].Children)
	}

	// The supplied dictionary replaces the built-in one.
	_, err = decoder.Decode(encodeAVP(264, FlagMandatory, 0, []byte("origin")))
	if err == nil {
		t.Fatal("custom dictionary unexpectedly retained a built-in known AVP")
	}
}

func TestZeroValueDecoderUsesBuiltinDictionary(t *testing.T) {
	sessionID := encodeAVP(263, FlagMandatory, 0, []byte("session"))
	var decoder Decoder
	if _, err := decoder.Decode(sessionID); err != nil {
		t.Fatalf("zero-value Decoder rejected a base AVP: %v", err)
	}
}

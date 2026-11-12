package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

var testSessionBaseKey = []byte("0123456789abcdef")

func testNegotiate(flags uint32) []byte {
	raw := make([]byte, 32)
	copy(raw[:8], ntlmSignature)
	binary.LittleEndian.PutUint32(raw[8:12], MessageTypeNegotiate)
	binary.LittleEndian.PutUint32(raw[12:16], flags)
	return raw
}

func utf16LE(value string) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

func avPair(id uint16, value []byte) []byte {
	out := make([]byte, 4+len(value))
	binary.LittleEndian.PutUint16(out[:2], id)
	binary.LittleEndian.PutUint16(out[2:4], uint16(len(value)))
	copy(out[4:], value)
	return out
}

func targetInfo(spn string, flags uint32, timestamp bool) []byte {
	var out []byte
	if flags != 0 {
		flagValue := make([]byte, 4)
		binary.LittleEndian.PutUint32(flagValue, flags)
		out = append(out, avPair(MsvAvIDFlags, flagValue)...)
	}
	if timestamp {
		out = append(out, avPair(MsvAvIDTimestamp, []byte{0, 1, 2, 3, 4, 5, 6, 7})...)
	}
	if spn != "" {
		out = append(out, avPair(MsvAvIDTargetName, utf16LE(spn))...)
	}
	out = append(out, avPair(MsvAvIDEnd, nil)...)
	return out
}

func testChallenge(info []byte) []byte {
	raw := make([]byte, 48+len(info))
	copy(raw[:8], ntlmSignature)
	binary.LittleEndian.PutUint32(raw[8:12], MessageTypeChallenge)
	binary.LittleEndian.PutUint32(raw[20:24], 0x00000201)
	copy(raw[24:32], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	binary.LittleEndian.PutUint16(raw[40:42], uint16(len(info)))
	binary.LittleEndian.PutUint16(raw[42:44], uint16(len(info)))
	binary.LittleEndian.PutUint32(raw[44:48], 48)
	copy(raw[48:], info)
	return raw
}

func ntlmv2Response(info []byte) []byte {
	proof := bytes.Repeat([]byte{0xaa}, 16)
	blob := make([]byte, 28)
	blob[0] = 1
	blob[1] = 1
	// Timestamp and client challenge are deliberately arbitrary; proof
	// verification is outside this package's scope.
	copy(blob[16:24], []byte{9, 8, 7, 6, 5, 4, 3, 2})
	return append(proof, append(blob, info...)...)
}

type authSpec struct {
	withMIC     bool
	withVersion bool
	otherInfo   []byte
	negotiate   []byte
	challenge   []byte
	sessionKey  []byte
}

func buildAuthenticate(spec authSpec) []byte {
	headerEnd := 64
	if spec.withVersion {
		headerEnd = 72
	}
	if spec.withMIC {
		headerEnd += 16
	}

	domain := utf16LE("EXAMPLE")
	user := utf16LE("alice")
	workstation := utf16LE("WORKSTATION")
	ntResponse := ntlmv2Response(spec.otherInfo)

	payload := make([]byte, 0, len(domain)+len(user)+len(workstation)+len(ntResponse))
	payload = append(payload, domain...)
	payload = append(payload, user...)
	payload = append(payload, workstation...)
	payload = append(payload, ntResponse...)

	raw := make([]byte, headerEnd, headerEnd+len(payload))
	copy(raw[:8], ntlmSignature)
	binary.LittleEndian.PutUint32(raw[8:12], MessageTypeAuthenticate)

	flags := uint32(0x00000001)
	if spec.withVersion {
		flags |= NegotiateFlagVersion
		copy(raw[64:72], []byte{0x0a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0f})
	}
	if spec.withMIC {
		flags |= NegotiateFlagSign
	}
	binary.LittleEndian.PutUint32(raw[60:64], flags)

	offset := headerEnd
	putBuffer := func(field int, value []byte) {
		if len(value) == 0 {
			return
		}
		binary.LittleEndian.PutUint16(raw[field:field+2], uint16(len(value)))
		binary.LittleEndian.PutUint16(raw[field+2:field+4], uint16(len(value)))
		binary.LittleEndian.PutUint32(raw[field+4:field+8], uint32(offset))
		offset += len(value)
	}
	putBuffer(28, domain)
	putBuffer(36, user)
	putBuffer(44, workstation)
	putBuffer(20, ntResponse)

	raw = append(raw, payload...)

	if spec.withMIC {
		mic := referenceMIC(spec.negotiate, spec.challenge, raw, headerEnd-16, spec.sessionKey)
		copy(raw[headerEnd-16:headerEnd], mic)
	}
	return raw
}

func referenceMIC(negotiate, challenge, authenticate []byte, micOffset int, sessionBaseKey []byte) []byte {
	zeroed := append([]byte(nil), authenticate...)
	for i := micOffset; i < micOffset+16; i++ {
		zeroed[i] = 0
	}
	mac := hmac.New(md5.New, sessionBaseKey)
	mac.Write(negotiate)
	mac.Write(challenge)
	mac.Write(zeroed)
	return mac.Sum(nil)
}

func TestValidateAuthenticateLegitimateHandshake(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	if err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "HTTP/server.example.com"); err != nil {
		t.Fatalf("valid handshake rejected: %v", err)
	}
	result, err := ValidateAuthenticateDetailed(neg, chal, auth, ValidationOptions{
		SessionBaseKey: testSessionBaseKey,
		ServiceSPN:     "HTTP/server.example.com",
	})
	if err != nil {
		t.Fatalf("detailed validation failed: %v", err)
	}
	if !result.MICVerified || !result.MICRequired {
		t.Fatalf("expected a required, verified MIC, got required=%v verified=%v", result.MICRequired, result.MICVerified)
	}
	if result.UserName != "alice" || result.DomainName != "EXAMPLE" {
		t.Fatalf("unexpected identity: %q/%q", result.DomainName, result.UserName)
	}
	if len(result.TargetSPNs) != 1 || !strings.EqualFold(result.TargetSPNs[0], "HTTP/server.example.com") {
		t.Fatalf("unexpected target SPNs: %#v", result.TargetSPNs)
	}
}

func TestValidateAuthenticateRejectsStrippedMIC(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     false,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrMICMissing) {
		t.Fatalf("stripped MIC error = %v, want ErrMICMissing", err)
	}
}

func TestValidateAuthenticateRejectsPhysicallyDeletedMIC(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	// Remove the 16 MIC bytes and shift every non-empty payload offset
	// back by 16 bytes, exactly as a relay stripping the field would.
	stripped := append([]byte(nil), auth[:72]...)
	stripped = append(stripped, auth[88:]...)
	for _, field := range []int{12, 20, 28, 36, 44, 52} {
		length := binary.LittleEndian.Uint16(stripped[field : field+2])
		if length == 0 {
			continue
		}
		offset := binary.LittleEndian.Uint32(stripped[field+4 : field+8])
		binary.LittleEndian.PutUint32(stripped[field+4:field+8], offset-16)
	}

	err := ValidateAuthenticate(neg, chal, stripped, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrMICMissing) {
		t.Fatalf("physically deleted MIC error = %v, want ErrMICMissing", err)
	}
}

func TestValidateAuthenticateRejectsZeroedMIC(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})
	copy(auth[72:88], make([]byte, 16))

	err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrMICMismatch) {
		t.Fatalf("zeroed MIC error = %v, want ErrMICMismatch", err)
	}
}

func TestValidateAuthenticateRejectsModifiedNegotiate(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	// Simulate a relay that changes the negotiated flags after the
	// client calculated the MIC.
	changed := append([]byte(nil), neg...)
	changed[12] ^= 0x01
	err := ValidateAuthenticate(changed, chal, auth, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrMICMismatch) {
		t.Fatalf("modified NEGOTIATE error = %v, want ErrMICMismatch", err)
	}
}

func TestValidateAuthenticateRejectsTamperedTargetInfo(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	// Change the target name in the CHALLENGE while leaving the MIC
	// calculated over the original message.
	old := []byte("HTTP/server.example.com")
	tampered := append([]byte(nil), chal...)
	value := bytes.Index(tampered, utf16LE(string(old)))
	if value < 0 {
		t.Fatal("target name not found in challenge")
	}
	copy(tampered[value:], utf16LE("HTTP/evil.example.com"))
	err := ValidateAuthenticate(neg, tampered, auth, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrMICMismatch) {
		t.Fatalf("tampered AV_PAIR error = %v, want ErrMICMismatch", err)
	}
}

func TestValidateAuthenticateRejectsDifferentServiceWithValidMIC(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/evil.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	// A relay that knows the SessionBaseKey can recompute the MIC
	// after changing the AV_PAIR; the SPN binding must still reject it.
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrSPNMismatch) {
		t.Fatalf("wrong SPN error = %v, want ErrSPNMismatch", err)
	}
}

func TestValidateAuthenticateRejectsTimestampWithoutMIC(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", 0, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     false,
		withVersion: true,
		otherInfo:   targetInfo("HTTP/server.example.com", 0, true),
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrMICMissing) {
		t.Fatalf("timestamp without MIC error = %v, want ErrMICMissing", err)
	}
}

func TestValidateAuthenticateAllowsNoMICWithoutIndicators(t *testing.T) {
	neg := testNegotiate(0)
	info := targetInfo("HTTP/server.example.com", 0, false)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     false,
		withVersion: false,
		otherInfo:   targetInfo("HTTP/server.example.com", 0, false),
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	if err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "HTTP/server.example.com"); err != nil {
		t.Fatalf("MIC-less handshake without MIC indicators rejected: %v", err)
	}
}

func TestValidateAuthenticateMICWithoutVersionField(t *testing.T) {
	neg := testNegotiate(NegotiateFlagSign)
	info := targetInfo("SMB/server.example.com", MsvAvFlagMICPresent, false)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: false,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	if err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "SMB/server.example.com"); err != nil {
		t.Fatalf("MIC at offset 64 rejected: %v", err)
	}
}

func TestValidateAuthenticateRequiresSPN(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("", MsvAvFlagMICPresent, false)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	err := ValidateAuthenticate(neg, chal, auth, testSessionBaseKey, "HTTP/server.example.com")
	if !errors.Is(err, ErrSPNMissing) {
		t.Fatalf("missing SPN error = %v, want ErrSPNMissing", err)
	}
}

func TestValidateAuthenticateRequireMICOption(t *testing.T) {
	neg := testNegotiate(0)
	info := targetInfo("HTTP/server.example.com", 0, false)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     false,
		withVersion: false,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	err := ValidateAuthenticateWithOptions(neg, chal, auth, ValidationOptions{
		SessionBaseKey: testSessionBaseKey,
		ServiceSPN:     "HTTP/server.example.com",
		RequireMIC:     true,
	})
	if !errors.Is(err, ErrMICMissing) {
		t.Fatalf("RequireMIC error = %v, want ErrMICMissing", err)
	}
}

func TestValidateAuthenticateAcceptsReorderedArguments(t *testing.T) {
	neg := testNegotiate(NegotiateFlagVersion | NegotiateFlagSign)
	info := targetInfo("HTTP/server.example.com", MsvAvFlagMICPresent, true)
	chal := testChallenge(info)
	auth := buildAuthenticate(authSpec{
		withMIC:     true,
		withVersion: true,
		otherInfo:   info,
		negotiate:   neg,
		challenge:   chal,
		sessionKey:  testSessionBaseKey,
	})

	if err := ValidateAuthenticate(auth, chal, neg, testSessionBaseKey, "HTTP/server.example.com"); err != nil {
		t.Fatalf("reordered messages rejected: %v", err)
	}
	if err := ValidateAuthenticate(testSessionBaseKey, neg, chal, auth, "HTTP/server.example.com"); err != nil {
		t.Fatalf("key-first arguments rejected: %v", err)
	}
}

func TestParseAVPairsRejectsMalformed(t *testing.T) {
	_, err := ParseAVPairs([]byte{0x06, 0x00, 0x03, 0x00, 0x01})
	if !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("truncated AV_PAIR error = %v, want ErrMalformedMessage", err)
	}
	_, err = ParseAVPairs([]byte{0, 0, 2, 0})
	if !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("bad AV_PAIR terminator error = %v, want ErrMalformedMessage", err)
	}
}

func TestDecodeNTLMHeader(t *testing.T) {
	raw := testNegotiate(0)
	header := "NTLM " + base64Of(raw)
	got, err := DecodeNTLMHeader(header)
	if err != nil {
		t.Fatalf("DecodeNTLMHeader: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("decoded message mismatch")
	}
	if _, err := DecodeNTLMHeader("Basic abc"); !errors.Is(err, ErrUnsupportedMessage) {
		t.Fatalf("non-NTLM scheme error = %v, want ErrUnsupportedMessage", err)
	}
}

func base64Of(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

package radius

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"errors"
	"testing"
)

var testSecret = []byte("s3cr3t-shared-secret")

func mustRequest(t *testing.T, identifier byte, proxyStates ...[]byte) *Packet {
	t.Helper()
	authenticator, err := NewRandomRequestAuthenticator()
	if err != nil {
		t.Fatalf("random authenticator: %v", err)
	}
	attrs := []Attribute{{Type: TypeUserName, Value: []byte("alice@example.com")}}
	for _, state := range proxyStates {
		attrs = append(attrs, Attribute{Type: TypeProxyState, Value: state})
	}
	request, err := NewAccessRequest(identifier, authenticator, testSecret, attrs)
	if err != nil {
		t.Fatalf("NewAccessRequest: %v", err)
	}
	return request
}

// signResponse 模拟服务端：填充 MA（以请求 Request Authenticator 做 HMAC-MD5），
// 再按 RFC 2865 计算 Response Authenticator。
func signResponse(t *testing.T, request *Packet, secret []byte, code Code, attrs []Attribute) []byte {
	t.Helper()
	body, err := encodeAttributes(attrs)
	if err != nil {
		t.Fatalf("encodeAttributes: %v", err)
	}
	length := headerLength + len(body)
	raw := make([]byte, length)
	raw[0] = byte(code)
	raw[1] = request.Identifier
	raw[2] = byte(length >> 8)
	raw[3] = byte(length)
	copy(raw[headerLength:], body)

	if offset, ok := messageAuthenticatorValueOffset(raw); ok {
		sum := messageAuthenticatorSum(raw, request.Authenticator[:], secret)
		copy(raw[offset:offset+authenticatorLength], sum)
	}

	hash := md5.New()
	hash.Write(raw[:4])
	hash.Write(request.Authenticator[:])
	hash.Write(raw[headerLength:])
	hash.Write(secret)
	copy(raw[4:headerLength], hash.Sum(nil))
	return raw
}

func maPlaceholder() Attribute {
	return Attribute{Type: TypeMessageAuthenticator, Value: make([]byte, authenticatorLength)}
}

func echoProxyStates(request *Packet) []Attribute {
	var attrs []Attribute
	for _, state := range request.ProxyStates() {
		attrs = append(attrs, Attribute{Type: TypeProxyState, Value: state})
	}
	return attrs
}

func legalResponseAttrs(request *Packet, extra ...Attribute) []Attribute {
	attrs := append([]Attribute{maPlaceholder()}, echoProxyStates(request)...)
	return append(attrs, extra...)
}

func parseVerify(t *testing.T, request *Packet, raw []byte) error {
	t.Helper()
	response, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse response: %v", err)
	}
	return VerifyResponse(request, response, testSecret)
}

func TestAccessRequestMarshalPutsMessageAuthenticatorFirst(t *testing.T) {
	request := mustRequest(t, 7, []byte("state-aaa"), []byte("state-bbb"))
	raw, err := request.Marshal(testSecret)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if Type(raw[headerLength]) != TypeMessageAuthenticator {
		t.Fatalf("first attribute type = %d, want Message-Authenticator(80)", raw[headerLength])
	}
	if int(raw[headerLength+1]) != attrHeaderLength+authenticatorLength {
		t.Fatalf("MA attribute length = %d, want 18", raw[headerLength+1])
	}

	// 独立按 RFC 2869 复算 HMAC，验证线上 MA 值。
	scrubbed := bytes.Clone(raw)
	for index := headerLength + attrHeaderLength; index < headerLength+attrHeaderLength+authenticatorLength; index++ {
		scrubbed[index] = 0
	}
	mac := hmac.New(md5.New, testSecret)
	mac.Write(scrubbed)
	want := mac.Sum(nil)
	got := raw[headerLength+attrHeaderLength : headerLength+attrHeaderLength+authenticatorLength]
	if !hmac.Equal(got, want) {
		t.Fatalf("wire Message-Authenticator does not match HMAC-MD5")
	}

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("re-parse request: %v", err)
	}
	if err := parsed.checkMessageAuthenticatorStructure(); err != nil {
		t.Fatalf("request MA structure: %v", err)
	}
	if len(parsed.ProxyStates()) != 2 {
		t.Fatalf("proxy states = %d, want 2", len(parsed.ProxyStates()))
	}
}

func TestVerifyResponseLegal(t *testing.T) {
	request := mustRequest(t, 42, []byte("proxy-state-1"), []byte("proxy-state-2"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, code := range []Code{CodeAccessAccept, CodeAccessReject, CodeAccessChallenge} {
		raw := signResponse(t, request, testSecret, code, legalResponseAttrs(request))
		if err := parseVerify(t, request, raw); err != nil {
			t.Fatalf("code %d: legal response rejected: %v", code, err)
		}
	}
}

func TestVerifyResponseMissingMessageAuthenticator(t *testing.T) {
	request := mustRequest(t, 1, []byte("state-x"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 应答属性里完全没有 MA。
	raw := signResponse(t, request, testSecret, CodeAccessAccept, echoProxyStates(request))
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrMessageAuthenticatorMissing) {
		t.Fatalf("err = %v, want ErrMessageAuthenticatorMissing", err)
	}
}

func TestVerifyResponseAppendedProxyStateRejected(t *testing.T) {
	request := mustRequest(t, 2, []byte("state-original"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 攻击者/代理在应答尾部附加一个我方从未发出的 Proxy-State。
	attrs := legalResponseAttrs(request, Attribute{Type: TypeProxyState, Value: []byte("injected-state")})
	raw := signResponse(t, request, testSecret, CodeAccessAccept, attrs)
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrProxyStateMismatch) {
		t.Fatalf("err = %v, want ErrProxyStateMismatch", err)
	}
}

func TestVerifyResponseChangedIdentifierRejected(t *testing.T) {
	request := mustRequest(t, 9, []byte("state-id"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, legalResponseAttrs(request))
	raw[1] = request.Identifier + 1
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrIdentifierMismatch) {
		t.Fatalf("err = %v, want ErrIdentifierMismatch", err)
	}
}

func TestVerifyResponseMessageAuthenticatorNotFirst(t *testing.T) {
	request := mustRequest(t, 3)
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	attrs := []Attribute{
		{Type: TypeUserName, Value: []byte("bob")},
		maPlaceholder(),
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, attrs)
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrMessageAuthenticatorPosition) {
		t.Fatalf("err = %v, want ErrMessageAuthenticatorPosition", err)
	}
}

func TestVerifyResponseDuplicateMessageAuthenticator(t *testing.T) {
	request := mustRequest(t, 4)
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, []Attribute{maPlaceholder(), maPlaceholder()})
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrMessageAuthenticatorDuplicate) {
		t.Fatalf("err = %v, want ErrMessageAuthenticatorDuplicate", err)
	}
}

func TestVerifyResponseBadMessageAuthenticatorLength(t *testing.T) {
	request := mustRequest(t, 5)
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	attrs := []Attribute{{Type: TypeMessageAuthenticator, Value: []byte("short")}}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, attrs)
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrMessageAuthenticatorLength) {
		t.Fatalf("err = %v, want ErrMessageAuthenticatorLength", err)
	}
}

func TestVerifyResponseTamperedMessageAuthenticator(t *testing.T) {
	request := mustRequest(t, 6, []byte("state-ma"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, legalResponseAttrs(request))
	raw[headerLength+attrHeaderLength] ^= 0xFF
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrMessageAuthenticatorMismatch) {
		t.Fatalf("err = %v, want ErrMessageAuthenticatorMismatch", err)
	}
}

func TestVerifyResponseTamperedResponseAuthenticator(t *testing.T) {
	request := mustRequest(t, 7, []byte("state-ra"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, legalResponseAttrs(request))
	raw[4] ^= 0x01
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrResponseAuthenticatorMismatch) {
		t.Fatalf("err = %v, want ErrResponseAuthenticatorMismatch", err)
	}
}

func TestVerifyResponseModifiedProxyStateRejected(t *testing.T) {
	request := mustRequest(t, 8, []byte("state-original"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	attrs := legalResponseAttrs(request)
	// 数量一致但内容被改动。
	for index := range attrs {
		if attrs[index].Type == TypeProxyState {
			attrs[index].Value = []byte("state-changed")
		}
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, attrs)
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrProxyStateMismatch) {
		t.Fatalf("err = %v, want ErrProxyStateMismatch", err)
	}
}

func TestVerifyResponseMissingProxyStateRejected(t *testing.T) {
	request := mustRequest(t, 10, []byte("state-a"), []byte("state-b"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 应答只回显一个，请求发出两个。
	attrs := []Attribute{maPlaceholder(), {Type: TypeProxyState, Value: []byte("state-a")}}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, attrs)
	err := parseVerify(t, request, raw)
	if !errors.Is(err, ErrProxyStateMismatch) {
		t.Fatalf("err = %v, want ErrProxyStateMismatch", err)
	}
}

func TestVerifyResponseWrongSecretRejected(t *testing.T) {
	request := mustRequest(t, 11, []byte("state-s"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, legalResponseAttrs(request))
	response, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := VerifyResponse(request, response, []byte("attacker-secret")); !errors.Is(err, ErrMessageAuthenticatorMismatch) {
		t.Fatalf("err = %v, want ErrMessageAuthenticatorMismatch", err)
	}
	if err := VerifyResponse(request, response, nil); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("err = %v, want ErrEmptySecret", err)
	}
}

func TestNewAccessRequestRejectsSuppliedMessageAuthenticator(t *testing.T) {
	var authenticator [authenticatorLength]byte
	if _, err := NewAccessRequest(1, authenticator, testSecret, []Attribute{maPlaceholder()}); err == nil {
		t.Fatal("expected error when caller supplies Message-Authenticator")
	}
	if _, err := NewAccessRequest(1, authenticator, nil, nil); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("err = %v, want ErrEmptySecret", err)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse(make([]byte, headerLength-1)); !errors.Is(err, ErrPacketTooShort) {
		t.Fatalf("err = %v, want ErrPacketTooShort", err)
	}

	request := mustRequest(t, 12)
	raw, err := request.Marshal(testSecret)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	badLength := bytes.Clone(raw)
	badLength[3]++ // Length 字段与实际不符。
	if _, err := Parse(badLength); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}

	badAttr := bytes.Clone(raw)
	badAttr[headerLength+1] = 250 // 声明属性超长，越过报文边界。
	badAttr[2], badAttr[3] = byte(len(badAttr)>>8), byte(len(badAttr))
	if _, err := Parse(badAttr); !errors.Is(err, ErrMalformedAttribute) {
		t.Fatalf("err = %v, want ErrMalformedAttribute", err)
	}
}

func TestMarshalRejectsNonRequest(t *testing.T) {
	request := mustRequest(t, 13)
	request.Code = CodeAccessAccept
	if _, err := request.Marshal(testSecret); !errors.Is(err, ErrUnsupportedCode) {
		t.Fatalf("err = %v, want ErrUnsupportedCode", err)
	}
}

func TestDefensiveBranches(t *testing.T) {
	var nilPacket *Packet
	if _, err := nilPacket.Marshal(testSecret); err == nil {
		t.Fatal("nil packet Marshal should fail")
	}
	if states := nilPacket.ProxyStates(); states != nil {
		t.Fatalf("nil packet ProxyStates = %v, want nil", states)
	}
	if err := VerifyResponse(nil, mustRequest(t, 15), testSecret); err == nil {
		t.Fatal("VerifyResponse with nil request should fail")
	}

	request := mustRequest(t, 16)
	accounting := &Packet{Code: CodeAccountingResp, Identifier: request.Identifier, Attributes: []Attribute{maPlaceholder()}}
	if err := VerifyResponse(request, accounting, testSecret); !errors.Is(err, ErrUnsupportedCode) {
		t.Fatalf("err = %v, want ErrUnsupportedCode", err)
	}

	badRequest := &Packet{Code: CodeAccessAccept}
	if err := VerifyResponse(badRequest, accounting, testSecret); !errors.Is(err, ErrUnsupportedCode) {
		t.Fatalf("err = %v, want ErrUnsupportedCode", err)
	}

	// 单属性超过 253 字节值上限，编码应拒绝。
	oversized := mustRequest(t, 17)
	oversized.Attributes = append(oversized.Attributes, Attribute{
		Type:  TypeUserName,
		Value: make([]byte, maxAttrLength-attrHeaderLength+1),
	})
	if _, err := oversized.Marshal(testSecret); !errors.Is(err, ErrMalformedAttribute) {
		t.Fatalf("err = %v, want ErrMalformedAttribute", err)
	}
}

// TestResponseMessageAuthenticatorUsesRequestAuthenticator 独立复算 RFC 3579 §3.2：
// 应答 MA = HMAC-MD5(secret, 应答整包且头部替换为请求 Request Authenticator、MA 值清零)。
func TestResponseMessageAuthenticatorUsesRequestAuthenticator(t *testing.T) {
	request := mustRequest(t, 14, []byte("state-rfc3579"))
	if _, err := request.Marshal(testSecret); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := signResponse(t, request, testSecret, CodeAccessAccept, legalResponseAttrs(request))

	scrubbed := bytes.Clone(raw)
	copy(scrubbed[4:headerLength], request.Authenticator[:])
	maOffset := headerLength + attrHeaderLength
	for index := maOffset; index < maOffset+authenticatorLength; index++ {
		scrubbed[index] = 0
	}
	mac := hmac.New(md5.New, testSecret)
	mac.Write(scrubbed)
	if !hmac.Equal(raw[maOffset:maOffset+authenticatorLength], mac.Sum(nil)) {
		t.Fatal("response MA not derived from request authenticator per RFC 3579")
	}

	// 若误用应答自身的 Response Authenticator 参与 HMAC，结果必须不同。
	wrong := bytes.Clone(raw)
	ma := hmac.New(md5.New, testSecret)
	ma.Write(zeroMA(wrong))
	if hmac.Equal(raw[maOffset:maOffset+authenticatorLength], ma.Sum(nil)) {
		t.Fatal("response MA unexpectedly matches HMAC keyed by response authenticator")
	}
}

func zeroMA(packet []byte) []byte {
	maOffset := headerLength + attrHeaderLength
	for index := maOffset; index < maOffset+authenticatorLength; index++ {
		packet[index] = 0
	}
	return packet
}

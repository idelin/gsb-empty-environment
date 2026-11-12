package netsec_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"testing"

	netsec "example.org/netsec/q0122"
)

var testSecret = []byte("radius-shared-secret")

func TestVerifyRequestAcceptsValidRequest(t *testing.T) {
	request := validRequest(t, tlv(1, []byte("alice")), tlv(4, []byte{10, 0, 0, 1}))

	p, err := netsec.VerifyRequest(request, testSecret)
	if err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
	if p.Code != netsec.CodeAccessRequest {
		t.Fatalf("Code = %d, want %d", p.Code, netsec.CodeAccessRequest)
	}
	if p.Identifier != 7 {
		t.Fatalf("Identifier = %d, want 7", p.Identifier)
	}
	if len(p.Attributes) != 3 || p.Attributes[0].Type != netsec.AttrMessageAuthenticator {
		t.Fatalf("attributes = %#v, want Message-Authenticator first", p.Attributes)
	}
}

func TestVerifyRequestRejectsTamperedAttribute(t *testing.T) {
	request := validRequest(t, tlv(1, []byte("alice")))

	tampered := bytes.Clone(request)
	tampered[20+18+2] ^= 0xff

	assertErrorIs(t, verifyRequestError(t, tampered), netsec.ErrMessageAuthenticatorMismatch)
}

func TestVerifyRequestRejectsTamperedMessageAuthenticator(t *testing.T) {
	request := validRequest(t, tlv(1, []byte("alice")))

	tampered := bytes.Clone(request)
	tampered[22] ^= 0x01

	assertErrorIs(t, verifyRequestError(t, tampered), netsec.ErrMessageAuthenticatorMismatch)
}

func TestVerifyRequestRejectsMissingMessageAuthenticator(t *testing.T) {
	request := packet(netsec.CodeAccessRequest, 7, authenticator(0xa0), tlv(1, []byte("alice"))...)

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrMessageAuthenticatorMissing)
}

func TestVerifyRequestRejectsRequestWithNoAttributes(t *testing.T) {
	request := packet(netsec.CodeAccessRequest, 7, authenticator(0xa0))

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrMessageAuthenticatorMissing)
}

func TestVerifyRequestRejectsDuplicateMessageAuthenticator(t *testing.T) {
	attrs := append(
		messageAuthenticatorAttribute(16),
		append(tlv(1, []byte("alice")), messageAuthenticatorAttribute(16)...)...,
	)
	request := packet(netsec.CodeAccessRequest, 7, authenticator(0xa0), attrs...)
	signRequestMessageAuthenticator(t, request, testSecret)

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrMessageAuthenticatorDuplicate)
}

func TestVerifyRequestRejectsWrongMessageAuthenticatorLength(t *testing.T) {
	request := packet(
		netsec.CodeAccessRequest, 7, authenticator(0xa0),
		messageAuthenticatorAttribute(15)...,
	)

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrMessageAuthenticatorLength)
}

func TestVerifyRequestRejectsMessageAuthenticatorThatIsNotFirst(t *testing.T) {
	attrs := append(tlv(1, []byte("alice")), messageAuthenticatorAttribute(16)...)
	request := packet(netsec.CodeAccessRequest, 7, authenticator(0xa0), attrs...)
	signRequestMessageAuthenticator(t, request, testSecret)

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrMessageAuthenticatorNotFirst)
}

func TestVerifyRequestRejectsMalformedAttribute(t *testing.T) {
	request := packet(netsec.CodeAccessRequest, 7, authenticator(0xa0), 1, 1)

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrMalformedPacket)
}

func TestVerifyRequestRejectsTruncatedPacket(t *testing.T) {
	request := validRequest(t, tlv(1, []byte("alice")))
	binary.BigEndian.PutUint16(request[2:4], uint16(len(request)+1))

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrMalformedPacket)
}

func TestVerifyRequestRejectsWrongCode(t *testing.T) {
	request := validRequest(t, tlv(1, []byte("alice")))
	request[0] = netsec.CodeAccessAccept

	assertErrorIs(t, verifyRequestError(t, request), netsec.ErrUnexpectedCode)
}

func TestVerifyRequestIgnoresTrailingPadding(t *testing.T) {
	request := append(validRequest(t, tlv(1, []byte("alice"))), 0xde, 0xad)
	if _, err := netsec.VerifyRequest(request, testSecret); err != nil {
		t.Fatalf("VerifyRequest() with trailing padding error = %v", err)
	}
}

func TestLegitimateExchange(t *testing.T) {
	request := validRequest(t, tlv(1, []byte("alice")), tlv(2, []byte{0, 0, 0, 2}))

	req, err := netsec.VerifyRequest(request, testSecret)
	if err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}

	response, err := netsec.NewResponse(
		netsec.CodeAccessAccept,
		req.Identifier,
		req.Authenticator[:],
		tlv(1, []byte("alice")),
		testSecret,
	)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}

	resp, err := netsec.VerifyResponse(response, req.Authenticator[:], testSecret)
	if err != nil {
		t.Fatalf("VerifyResponse() error = %v", err)
	}
	if resp.Code != netsec.CodeAccessAccept || resp.Identifier != req.Identifier {
		t.Fatalf("response code/id = %d/%d, want %d/%d",
			resp.Code, resp.Identifier, netsec.CodeAccessAccept, req.Identifier)
	}

	wantMA := independentResponseMessageAuthenticator(response, req.Authenticator[:], testSecret)
	if got := resp.Attributes[0].Value; !hmac.Equal(got, wantMA) {
		t.Fatalf("Message-Authenticator = %x, want %x", got, wantMA)
	}

	wantAuth := independentResponseAuthenticator(response, req.Authenticator[:], testSecret)
	if !hmac.Equal(resp.Authenticator[:], wantAuth[:]) {
		t.Fatalf("Response Authenticator = %x, want %x", resp.Authenticator, wantAuth)
	}
}

func TestVerifyResponseRejectsTamperedAttribute(t *testing.T) {
	response := signedResponse(t)

	tampered := bytes.Clone(response)
	tampered[20+18+2] ^= 0x80

	assertErrorIs(t, verifyResponseError(t, tampered), netsec.ErrMessageAuthenticatorMismatch)
}

func TestVerifyResponseRejectsTamperedMessageAuthenticator(t *testing.T) {
	response := signedResponse(t)

	tampered := bytes.Clone(response)
	tampered[22] ^= 0x01

	assertErrorIs(t, verifyResponseError(t, tampered), netsec.ErrMessageAuthenticatorMismatch)
}

func TestVerifyResponseRejectsWrongRequestAuthenticator(t *testing.T) {
	response := signedResponse(t)

	wrongRequestAuthenticator := authenticator(0xb0)
	assertErrorIs(
		t,
		verifyResponseErrorWithAuthenticator(t, response, wrongRequestAuthenticator),
		netsec.ErrMessageAuthenticatorMismatch,
	)
}

func TestVerifyResponseRejectsMissingMessageAuthenticator(t *testing.T) {
	response := packet(netsec.CodeAccessAccept, 7, authenticator(0x11), tlv(1, []byte("alice"))...)

	assertErrorIs(t, verifyResponseError(t, response), netsec.ErrMessageAuthenticatorMissing)
}

func TestVerifyResponseRejectsDuplicateMessageAuthenticator(t *testing.T) {
	attrs := append(
		tlv(1, []byte("alice")),
		append(messageAuthenticatorAttribute(16), messageAuthenticatorAttribute(16)...)...,
	)
	response := packet(netsec.CodeAccessAccept, 7, authenticator(0x11), attrs...)

	assertErrorIs(t, verifyResponseError(t, response), netsec.ErrMessageAuthenticatorDuplicate)
}

func TestVerifyResponseRejectsWrongMessageAuthenticatorLength(t *testing.T) {
	response := packet(
		netsec.CodeAccessAccept, 7, authenticator(0x11),
		messageAuthenticatorAttribute(15)...,
	)

	assertErrorIs(t, verifyResponseError(t, response), netsec.ErrMessageAuthenticatorLength)
}

func TestVerifyResponseRejectsInvalidRequestAuthenticatorLength(t *testing.T) {
	response := signedResponse(t)

	_, err := netsec.VerifyResponse(response, []byte{0x01}, testSecret)
	assertErrorIs(t, err, netsec.ErrInvalidRequestAuthenticator)
}

func TestVerifyResponseRejectsRequestCode(t *testing.T) {
	request := validRequest(t, tlv(1, []byte("alice")))

	assertErrorIs(t, verifyResponseError(t, request), netsec.ErrUnexpectedCode)
}

func TestVerifyResponseAllowsMessageAuthenticatorNotFirst(t *testing.T) {
	reqAuth := authenticator(0x42)
	attrs := append(tlv(1, []byte("alice")), messageAuthenticatorAttribute(16)...)
	response := packet(netsec.CodeAccessReject, 7, authenticator(0), attrs...)

	if err := netsec.SignResponse(response, reqAuth, testSecret); err != nil {
		t.Fatalf("SignResponse() error = %v", err)
	}
	if _, err := netsec.VerifyResponse(response, reqAuth, testSecret); err != nil {
		t.Fatalf("VerifyResponse() with later Message-Authenticator error = %v", err)
	}
}

func TestSignResponseRequiresMessageAuthenticator(t *testing.T) {
	response := packet(netsec.CodeAccessAccept, 7, authenticator(0), tlv(1, []byte("alice"))...)

	err := netsec.SignResponse(response, authenticator(0x42), testSecret)
	assertErrorIs(t, err, netsec.ErrMessageAuthenticatorMissing)
}

func TestNewResponseRejectsRequestCode(t *testing.T) {
	_, err := netsec.NewResponse(
		netsec.CodeAccessRequest, 7, authenticator(0x42), tlv(1, []byte("alice")), testSecret,
	)
	assertErrorIs(t, err, netsec.ErrUnexpectedCode)
}

func validRequest(t *testing.T, attrs ...[]byte) []byte {
	t.Helper()

	attributes := append(messageAuthenticatorAttribute(16), flatten(attrs)...)
	request := packet(netsec.CodeAccessRequest, 7, authenticator(0xa0), attributes...)
	signRequestMessageAuthenticator(t, request, testSecret)
	return request
}

func signedResponse(t *testing.T) []byte {
	t.Helper()

	response, err := netsec.NewResponse(
		netsec.CodeAccessAccept,
		7,
		authenticator(0x42),
		tlv(1, []byte("alice")),
		testSecret,
	)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	return response
}

func verifyRequestError(t *testing.T, request []byte) error {
	t.Helper()

	_, err := netsec.VerifyRequest(request, testSecret)
	return err
}

func verifyResponseError(t *testing.T, response []byte) error {
	t.Helper()

	return verifyResponseErrorWithAuthenticator(t, response, authenticator(0x42))
}

func verifyResponseErrorWithAuthenticator(t *testing.T, response, requestAuthenticator []byte) error {
	t.Helper()

	_, err := netsec.VerifyResponse(response, requestAuthenticator, testSecret)
	return err
}

func packet(code, identifier byte, auth []byte, attributes ...byte) []byte {
	length := 20 + len(attributes)
	packet := make([]byte, length)
	packet[0] = code
	packet[1] = identifier
	binary.BigEndian.PutUint16(packet[2:4], uint16(length))
	copy(packet[4:20], auth)
	copy(packet[20:], attributes)
	return packet
}

func tlv(typ byte, value []byte) []byte {
	attribute := make([]byte, 2+len(value))
	attribute[0] = typ
	attribute[1] = byte(len(attribute))
	copy(attribute[2:], value)
	return attribute
}

func messageAuthenticatorAttribute(valueSize int) []byte {
	return tlv(netsec.AttrMessageAuthenticator, make([]byte, valueSize))
}

func signRequestMessageAuthenticator(t *testing.T, request, secret []byte) {
	t.Helper()

	offset := messageAuthenticatorValueOffset(t, request)
	input := bytes.Clone(request)
	clear(input[offset : offset+16])
	mac := hmac.New(md5.New, secret)
	_, _ = mac.Write(input)
	copy(request[offset:offset+16], mac.Sum(nil))
}

func messageAuthenticatorValueOffset(t *testing.T, packet []byte) int {
	t.Helper()

	length := int(binary.BigEndian.Uint16(packet[2:4]))
	for offset := 20; offset < length; {
		attrLength := int(packet[offset+1])
		if packet[offset] == netsec.AttrMessageAuthenticator {
			return offset + 2
		}
		offset += attrLength
	}
	t.Fatal("packet has no Message-Authenticator")
	return 0
}

func independentResponseMessageAuthenticator(
	response, requestAuthenticator, secret []byte,
) []byte {
	length := int(binary.BigEndian.Uint16(response[2:4]))
	input := bytes.Clone(response[:length])
	copy(input[4:20], requestAuthenticator)

	offset := 0
	for off := 20; off < length; {
		if input[off] == netsec.AttrMessageAuthenticator {
			offset = off + 2
			break
		}
		off += int(input[off+1])
	}
	clear(input[offset : offset+16])

	mac := hmac.New(md5.New, secret)
	_, _ = mac.Write(input)
	return mac.Sum(nil)
}

func independentResponseAuthenticator(
	response, requestAuthenticator, secret []byte,
) [16]byte {
	length := int(binary.BigEndian.Uint16(response[2:4]))
	input := bytes.Clone(response[:length])
	copy(input[4:20], requestAuthenticator)
	input = append(input, secret...)
	return md5.Sum(input)
}

func authenticator(seed byte) []byte {
	auth := make([]byte, 16)
	for i := range auth {
		auth[i] = seed + byte(i)
	}
	return auth
}

func flatten(parts [][]byte) []byte {
	var result []byte
	for _, part := range parts {
		result = append(result, part...)
	}
	return result
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()

	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

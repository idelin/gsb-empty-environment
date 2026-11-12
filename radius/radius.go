// Package radius 提供 RADIUS Access-Request 的构造发送与应答校验能力。
package radius

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"errors"
)

const headerLength = 20

const maxPacketLength = 4096

const (
	attrHeaderLength    = 2
	maxAttrLength       = 255
	authenticatorLength = 16
)

// Code 是 RADIUS 报文的 Code 字段。
type Code byte

const (
	CodeAccessRequest   Code = 1
	CodeAccessAccept    Code = 2
	CodeAccessReject    Code = 3
	CodeAccountingReq   Code = 4
	CodeAccountingResp  Code = 5
	CodeAccessChallenge Code = 11
)

// Type 是 RADIUS 属性类型编号。
type Type byte

const (
	TypeUserName             Type = 1
	TypeUserPassword         Type = 2
	TypeProxyState           Type = 33
	TypeMessageAuthenticator Type = 80
)

var (
	ErrEmptySecret                   = errors.New("radius: shared secret must not be empty")
	ErrPacketTooShort                = errors.New("radius: packet shorter than 20-byte header")
	ErrPacketTooLong                 = errors.New("radius: packet exceeds 4096-byte limit")
	ErrLengthMismatch                = errors.New("radius: length field does not match packet contents")
	ErrMalformedAttribute            = errors.New("radius: malformed attribute")
	ErrUnsupportedCode               = errors.New("radius: unsupported RADIUS code")
	ErrIdentifierMismatch            = errors.New("radius: response identifier does not match request")
	ErrMessageAuthenticatorMissing   = errors.New("radius: Message-Authenticator missing")
	ErrMessageAuthenticatorPosition  = errors.New("radius: Message-Authenticator must be the first attribute")
	ErrMessageAuthenticatorDuplicate = errors.New("radius: Message-Authenticator appears more than once")
	ErrMessageAuthenticatorLength    = errors.New("radius: Message-Authenticator value must be 16 octets")
	ErrMessageAuthenticatorMismatch  = errors.New("radius: Message-Authenticator verification failed")
	ErrResponseAuthenticatorMismatch = errors.New("radius: Response Authenticator verification failed")
	ErrProxyStateMismatch            = errors.New("radius: Proxy-State was not echoed back byte-for-byte")
)

// Attribute 表示一个 RADIUS 属性（TLV），Value 不含 Type/Length 头部。
type Attribute struct {
	Type  Type
	Value []byte
}

// Packet 表示一个已解析或待编码的 RADIUS 报文。
type Packet struct {
	Code          Code
	Identifier    byte
	Authenticator [authenticatorLength]byte
	Attributes    []Attribute

	// raw 保存解析得到的原始线上字节，校验时直接基于它做摘要，避免重编码歧义。
	raw []byte
}

// ProxyStates 返回报文中所有 Proxy-State 属性的值（按出现顺序），均为独立副本。
func (p *Packet) ProxyStates() [][]byte {
	if p == nil {
		return nil
	}
	var states [][]byte
	for _, attr := range p.Attributes {
		if attr.Type == TypeProxyState {
			value := make([]byte, len(attr.Value))
			copy(value, attr.Value)
			states = append(states, value)
		}
	}
	return states
}

// Parse 解析一段线上 RADIUS 报文，仅做结构校验，不做任何密码学校验。
func Parse(data []byte) (*Packet, error) {
	if len(data) < headerLength {
		return nil, ErrPacketTooShort
	}
	if len(data) > maxPacketLength {
		return nil, ErrPacketTooLong
	}
	declared := int(data[2])<<8 | int(data[3])
	if declared != len(data) {
		return nil, ErrLengthMismatch
	}

	packet := &Packet{
		Code:       Code(data[0]),
		Identifier: data[1],
		raw:        make([]byte, len(data)),
	}
	copy(packet.Authenticator[:], data[4:headerLength])
	copy(packet.raw, data)

	body := data[headerLength:]
	for offset := 0; offset < len(body); {
		if len(body)-offset < attrHeaderLength {
			return nil, ErrMalformedAttribute
		}
		attrType := Type(body[offset])
		attrLen := int(body[offset+1])
		if attrLen < attrHeaderLength || offset+attrLen > len(body) {
			return nil, ErrMalformedAttribute
		}
		value := make([]byte, attrLen-attrHeaderLength)
		copy(value, body[offset+attrHeaderLength:offset+attrLen])
		packet.Attributes = append(packet.Attributes, Attribute{Type: attrType, Value: value})
		offset += attrLen
	}
	return packet, nil
}

// NewAccessRequest 构造 Access-Request，Message-Authenticator 被强制放在属性首位。
// authenticator 为 16 字节随机 Request Authenticator；attributes 不得自带 Message-Authenticator。
func NewAccessRequest(identifier byte, authenticator [authenticatorLength]byte, secret []byte, attributes []Attribute) (*Packet, error) {
	if len(secret) == 0 {
		return nil, ErrEmptySecret
	}
	for _, attr := range attributes {
		if attr.Type == TypeMessageAuthenticator {
			return nil, errors.New("radius: Message-Authenticator is managed by the package and must not be supplied")
		}
	}

	packet := &Packet{
		Code:          CodeAccessRequest,
		Identifier:    identifier,
		Authenticator: authenticator,
		Attributes:    make([]Attribute, 0, len(attributes)+1),
	}
	packet.Attributes = append(packet.Attributes, Attribute{
		Type:  TypeMessageAuthenticator,
		Value: make([]byte, authenticatorLength),
	})
	packet.Attributes = append(packet.Attributes, attributes...)
	return packet, nil
}

// NewRandomRequestAuthenticator 返回密码学随机的 16 字节 Request Authenticator。
func NewRandomRequestAuthenticator() ([authenticatorLength]byte, error) {
	var authenticator [authenticatorLength]byte
	if _, err := rand.Read(authenticator[:]); err != nil {
		return authenticator, err
	}
	return authenticator, nil
}

// Marshal 把 Access-Request 编码为线上字节，并回填 Message-Authenticator。
func (p *Packet) Marshal(secret []byte) ([]byte, error) {
	if p == nil {
		return nil, errors.New("radius: nil packet")
	}
	if len(secret) == 0 {
		return nil, ErrEmptySecret
	}
	if p.Code != CodeAccessRequest {
		return nil, ErrUnsupportedCode
	}
	if err := p.checkMessageAuthenticatorStructure(); err != nil {
		return nil, err
	}
	return p.encode(secret)
}

// checkMessageAuthenticatorStructure 强制 MA 位于首位、仅出现一次且值长 16。
func (p *Packet) checkMessageAuthenticatorStructure() error {
	count := 0
	for _, attr := range p.Attributes {
		if attr.Type != TypeMessageAuthenticator {
			continue
		}
		count++
		if len(attr.Value) != authenticatorLength {
			return ErrMessageAuthenticatorLength
		}
	}
	switch {
	case count == 0:
		return ErrMessageAuthenticatorMissing
	case count > 1:
		return ErrMessageAuthenticatorDuplicate
	}
	if p.Attributes[0].Type != TypeMessageAuthenticator {
		return ErrMessageAuthenticatorPosition
	}
	return nil
}

func (p *Packet) encode(secret []byte) ([]byte, error) {
	body, err := encodeAttributes(p.Attributes)
	if err != nil {
		return nil, err
	}
	length := headerLength + len(body)
	if length > maxPacketLength {
		return nil, ErrPacketTooLong
	}

	raw := make([]byte, length)
	raw[0] = byte(p.Code)
	raw[1] = p.Identifier
	raw[2] = byte(length >> 8)
	raw[3] = byte(length)
	copy(raw[4:headerLength], p.Authenticator[:])
	copy(raw[headerLength:], body)

	offset, ok := messageAuthenticatorValueOffset(raw)
	if !ok {
		return nil, ErrMalformedAttribute
	}
	sum := messageAuthenticatorSum(raw, p.Authenticator[:], secret)
	copy(raw[offset:offset+authenticatorLength], sum)

	p.raw = make([]byte, len(raw))
	copy(p.raw, raw)
	return raw, nil
}

// VerifyResponse 校验 response 是否为 request 的合法应答，任一失败即拒绝。
func VerifyResponse(request, response *Packet, secret []byte) error {
	if request == nil || response == nil {
		return errors.New("radius: nil packet")
	}
	if len(secret) == 0 {
		return ErrEmptySecret
	}
	if request.Code != CodeAccessRequest {
		return ErrUnsupportedCode
	}
	switch response.Code {
	case CodeAccessAccept, CodeAccessReject, CodeAccessChallenge:
	default:
		return ErrUnsupportedCode
	}
	if response.Identifier != request.Identifier {
		return ErrIdentifierMismatch
	}
	if err := response.checkMessageAuthenticatorStructure(); err != nil {
		return err
	}

	raw := response.raw
	if len(raw) < headerLength {
		return ErrPacketTooShort
	}

	offset, ok := messageAuthenticatorValueOffset(raw)
	if !ok {
		return ErrMalformedAttribute
	}
	// RFC 3579 §3.2：应答 MA 的 HMAC 使用对应请求的 Request Authenticator。
	expectedMA := messageAuthenticatorSum(raw, request.Authenticator[:], secret)
	if subtle.ConstantTimeCompare(raw[offset:offset+authenticatorLength], expectedMA) != 1 {
		return ErrMessageAuthenticatorMismatch
	}

	// RFC 2865 §4.2：Response Authenticator 覆盖真实线上字节（含已填充的 MA）。
	hash := md5.New()
	hash.Write(raw[:4])
	hash.Write(request.Authenticator[:])
	hash.Write(raw[headerLength:])
	hash.Write(secret)
	if subtle.ConstantTimeCompare(raw[4:headerLength], hash.Sum(nil)) != 1 {
		return ErrResponseAuthenticatorMismatch
	}

	return verifyProxyState(request, response)
}

// verifyProxyState 要求应答 Proxy-State 与请求按序、逐字节完全一致。
func verifyProxyState(request, response *Packet) error {
	requestStates := request.ProxyStates()
	responseStates := response.ProxyStates()
	if len(requestStates) != len(responseStates) {
		return ErrProxyStateMismatch
	}
	for index := range requestStates {
		if subtle.ConstantTimeCompare(requestStates[index], responseStates[index]) != 1 {
			return ErrProxyStateMismatch
		}
	}
	return nil
}

// encodeAttributes 把属性序列化为线上 TLV 字节。
func encodeAttributes(attributes []Attribute) ([]byte, error) {
	total := 0
	for _, attr := range attributes {
		length := attrHeaderLength + len(attr.Value)
		if length > maxAttrLength {
			return nil, ErrMalformedAttribute
		}
		total += length
	}
	body := make([]byte, 0, total)
	for _, attr := range attributes {
		length := attrHeaderLength + len(attr.Value)
		body = append(body, byte(attr.Type), byte(length))
		body = append(body, attr.Value...)
	}
	return body, nil
}

// messageAuthenticatorValueOffset 返回线上报文中 MA 属性值的起始偏移。
// MA 必须是首个属性，否则返回 ok=false。
func messageAuthenticatorValueOffset(raw []byte) (int, bool) {
	if len(raw) < headerLength+attrHeaderLength {
		return 0, false
	}
	if Type(raw[headerLength]) != TypeMessageAuthenticator {
		return 0, false
	}
	attrLen := int(raw[headerLength+1])
	if attrLen != attrHeaderLength+authenticatorLength {
		return 0, false
	}
	return headerLength + attrHeaderLength, true
}

// messageAuthenticatorSum 按 RFC 2869 §5.14 计算 Message-Authenticator。
// 计算时把报文头部的 Authenticator 字段替换为 authenticator（请求即其随机
// Request Authenticator；应答按 RFC 3579 传入对应请求的 Request Authenticator），
// 同时把 MA 属性值清零，再以共享密钥做 HMAC-MD5。
func messageAuthenticatorSum(raw, authenticator []byte, secret []byte) []byte {
	scrubbed := make([]byte, len(raw))
	copy(scrubbed, raw)
	copy(scrubbed[4:headerLength], authenticator)
	if offset, ok := messageAuthenticatorValueOffset(scrubbed); ok {
		for index := offset; index < offset+authenticatorLength; index++ {
			scrubbed[index] = 0
		}
	}
	mac := hmac.New(md5.New, secret)
	mac.Write(scrubbed)
	return mac.Sum(nil)
}

package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
)

const (
	cookieMagic          = "FSK1"
	cookieFormatVersion  = uint16(1)
	cookieHandleSize     = 16
	cookieNonceSize      = 12
	cookieTagSize        = 16
	cookieHeaderSize     = len(cookieMagic) + 2 + 8 + cookieNonceSize
	maxCookiePayloadSize = 64 << 10
)

var errMalformedCookie = errors.New("malformed FAST cookie")

type requestBinding struct {
	kind RequestKind

	client      Principal
	outerRealm  string
	targetRealm string

	armor      ArmorTicket
	nonce      uint32
	enctype    int32
	bodyDigest RequestBodyDigest
}

type sessionBinding struct {
	requestBinding
	kdcInstance   string
	keyGeneration uint64
}

type sessionState struct {
	protocolVersion uint16
	handle          [cookieHandleSize]byte
	phase           Phase
	expiresAt       int64
	binding         sessionBinding
}

func bindingFromRequest(req Request) requestBinding {
	return requestBinding{
		kind:        req.Kind,
		client:      req.Client.clone(),
		outerRealm:  req.OuterRealm,
		targetRealm: req.TargetRealm,
		armor:       req.Armor.clone(),
		nonce:       req.Nonce,
		enctype:     req.Enctype,
		bodyDigest:  req.BodyDigest,
	}
}

func (b requestBinding) equal(other requestBinding) bool {
	return b.kind == other.kind &&
		principalsEqual(b.client, other.client) &&
		b.outerRealm == other.outerRealm &&
		b.targetRealm == other.targetRealm &&
		armorTicketsEqual(b.armor, other.armor) &&
		b.nonce == other.nonce &&
		b.enctype == other.enctype &&
		subtle.ConstantTimeCompare(b.bodyDigest[:], other.bodyDigest[:]) == 1
}

func armorTicketsEqual(a, b ArmorTicket) bool {
	return len(a.SessionID) == len(b.SessionID) &&
		subtle.ConstantTimeCompare(a.SessionID, b.SessionID) == 1 &&
		a.Realm == b.Realm &&
		principalsEqual(a.Client, b.Client)
}

func (b sessionBinding) equal(other sessionBinding) bool {
	return b.requestBinding.equal(other.requestBinding) &&
		b.kdcInstance == other.kdcInstance &&
		b.keyGeneration == other.keyGeneration
}

func cookieAAD(protocolVersion uint16, partition string, generation uint64) []byte {
	aad := []byte("netsec/FAST-cookie\x00")
	aad = appendUint16(aad, protocolVersion)
	aad = appendString(aad, partition)
	aad = appendUint64(aad, generation)
	return aad
}

func marshalSessionState(state sessionState) ([]byte, error) {
	if !state.phase.valid() || !state.binding.requestBinding.kind.valid() {
		return nil, errMalformedCookie
	}

	payload := make([]byte, 0, 256)
	payload = appendUint16(payload, state.protocolVersion)
	payload = append(payload, state.handle[:]...)
	payload = appendUint32(payload, uint32(state.phase))
	payload = appendUint64(payload, uint64(state.expiresAt))

	binding := state.binding.requestBinding
	payload = appendUint32(payload, uint32(binding.kind))
	payload = appendPrincipal(payload, binding.client)
	payload = appendString(payload, binding.outerRealm)
	payload = appendString(payload, binding.targetRealm)
	payload = appendString(payload, state.binding.kdcInstance)
	payload = appendUint64(payload, state.binding.keyGeneration)
	payload = appendBytes(payload, binding.armor.SessionID)
	payload = appendString(payload, binding.armor.Realm)
	payload = appendPrincipal(payload, binding.armor.Client)
	payload = appendUint32(payload, binding.nonce)
	payload = appendInt32(payload, binding.enctype)
	payload = append(payload, binding.bodyDigest[:]...)

	if len(payload) > maxCookiePayloadSize {
		return nil, errMalformedCookie
	}
	return payload, nil
}

func appendPrincipal(dst []byte, principal Principal) []byte {
	dst = appendInt32(dst, principal.NameType)
	dst = appendUint16(dst, uint16(len(principal.Components)))
	for _, component := range principal.Components {
		dst = appendString(dst, component)
	}
	return dst
}

func unmarshalSessionState(payload []byte) (sessionState, error) {
	var state sessionState
	if len(payload) > maxCookiePayloadSize {
		return state, errMalformedCookie
	}

	reader := payloadReader{data: payload}
	state.protocolVersion = reader.readUint16()
	copy(state.handle[:], reader.readFixed(cookieHandleSize))
	phase := reader.readUint32()
	state.phase = Phase(phase)
	state.expiresAt = int64(reader.readUint64())

	kindValue := reader.readUint32()
	if kindValue > 0xff {
		return state, errMalformedCookie
	}
	kind := RequestKind(kindValue)
	if !kind.valid() {
		return state, errMalformedCookie
	}
	state.binding.requestBinding.kind = kind
	state.binding.requestBinding.client = reader.readPrincipal()
	state.binding.requestBinding.outerRealm = reader.readString()
	state.binding.requestBinding.targetRealm = reader.readString()
	state.binding.kdcInstance = reader.readString()
	state.binding.keyGeneration = reader.readUint64()
	state.binding.requestBinding.armor.SessionID = reader.readBytes()
	state.binding.requestBinding.armor.Realm = reader.readString()
	state.binding.requestBinding.armor.Client = reader.readPrincipal()
	state.binding.requestBinding.nonce = reader.readUint32()
	state.binding.requestBinding.enctype = reader.readInt32()
	copy(state.binding.requestBinding.bodyDigest[:], reader.readFixed(sha256.Size))

	if reader.err != nil || reader.remaining() != 0 {
		return sessionState{}, errMalformedCookie
	}
	if !state.phase.valid() ||
		!state.binding.requestBinding.client.valid() ||
		!state.binding.requestBinding.armor.Client.valid() ||
		state.binding.requestBinding.outerRealm == "" ||
		len(state.binding.requestBinding.outerRealm) > 2048 ||
		state.binding.requestBinding.targetRealm == "" ||
		len(state.binding.requestBinding.targetRealm) > 2048 ||
		state.binding.kdcInstance == "" ||
		len(state.binding.kdcInstance) > 2048 ||
		state.binding.keyGeneration == 0 ||
		state.binding.requestBinding.enctype <= 0 ||
		!state.binding.requestBinding.armor.valid() {
		return sessionState{}, errMalformedCookie
	}
	return state, nil
}

func sealCookieWithNonce(
	state sessionState,
	key [keySize]byte,
	partition string,
	nonce [cookieNonceSize]byte,
	payload []byte,
) (Cookie, error) {
	aead, err := newCookieAEAD(key)
	if err != nil {
		return nil, err
	}

	header := make([]byte, cookieHeaderSize)
	copy(header[:4], cookieMagic)
	binary.BigEndian.PutUint16(header[4:6], cookieFormatVersion)
	binary.BigEndian.PutUint64(header[6:14], state.binding.keyGeneration)
	copy(header[14:], nonce[:])

	aad := cookieAAD(state.protocolVersion, partition, state.binding.keyGeneration)
	ciphertext := aead.Seal(nil, nonce[:], payload, aad)

	cookie := make([]byte, 0, len(header)+len(ciphertext))
	cookie = append(cookie, header...)
	cookie = append(cookie, ciphertext...)
	return Cookie(cookie), nil
}

func openCookie(
	cookie Cookie,
	key [keySize]byte,
	expectedGeneration uint64,
	expectedProtocolVersion uint16,
	partition string,
) (sessionState, error) {
	if len(cookie) < cookieHeaderSize+cookieTagSize ||
		len(cookie) > cookieHeaderSize+maxCookiePayloadSize+cookieTagSize {
		return sessionState{}, errMalformedCookie
	}

	header := cookie[:cookieHeaderSize]
	if !bytes.Equal(header[:4], []byte(cookieMagic)) {
		return sessionState{}, errMalformedCookie
	}
	if binary.BigEndian.Uint16(header[4:6]) != cookieFormatVersion {
		return sessionState{}, errMalformedCookie
	}
	generation := binary.BigEndian.Uint64(header[6:14])
	if generation != expectedGeneration {
		return sessionState{}, errMalformedCookie
	}
	var nonce [cookieNonceSize]byte
	copy(nonce[:], header[14:])

	aead, err := newCookieAEAD(key)
	if err != nil {
		return sessionState{}, errMalformedCookie
	}
	aad := cookieAAD(expectedProtocolVersion, partition, generation)
	plaintext, err := aead.Open(nil, nonce[:], cookie[cookieHeaderSize:], aad)
	if err != nil {
		return sessionState{}, errMalformedCookie
	}
	state, err := unmarshalSessionState(plaintext)
	if err != nil {
		return sessionState{}, errMalformedCookie
	}
	if state.protocolVersion != expectedProtocolVersion ||
		state.binding.keyGeneration != expectedGeneration {
		return sessionState{}, errMalformedCookie
	}
	return state, nil
}

func newCookieAEAD(key [keySize]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type payloadReader struct {
	data   []byte
	offset int
	err    error
}

func (r *payloadReader) remaining() int {
	if r.offset > len(r.data) {
		return 0
	}
	return len(r.data) - r.offset
}

func (r *payloadReader) take(size int) []byte {
	if r.err != nil {
		return nil
	}
	if size < 0 || size > r.remaining() {
		r.err = errMalformedCookie
		return nil
	}
	result := r.data[r.offset : r.offset+size]
	r.offset += size
	return result
}

func (r *payloadReader) readUint16() uint16 {
	data := r.take(2)
	if data == nil {
		return 0
	}
	return binary.BigEndian.Uint16(data)
}

func (r *payloadReader) readUint32() uint32 {
	data := r.take(4)
	if data == nil {
		return 0
	}
	return binary.BigEndian.Uint32(data)
}

func (r *payloadReader) readInt32() int32 {
	return int32(r.readUint32())
}

func (r *payloadReader) readUint64() uint64 {
	data := r.take(8)
	if data == nil {
		return 0
	}
	return binary.BigEndian.Uint64(data)
}

func (r *payloadReader) readFixed(size int) []byte {
	if size < 0 {
		r.err = errMalformedCookie
		return nil
	}
	return r.take(size)
}

func (r *payloadReader) readBytes() []byte {
	size := int(r.readUint32())
	if r.err != nil || size < 0 || size > r.remaining() {
		r.err = errMalformedCookie
		return nil
	}
	return cloneBytes(r.take(size))
}

func (r *payloadReader) readString() string {
	return string(r.readBytes())
}

func (r *payloadReader) readPrincipal() Principal {
	nameType := r.readInt32()
	count := int(r.readUint16())
	if r.err != nil || count == 0 || count > 64 {
		r.err = errMalformedCookie
		return Principal{}
	}
	components := make([]string, count)
	for i := range components {
		components[i] = r.readString()
		if len(components[i]) == 0 || len(components[i]) > 1024 {
			r.err = errMalformedCookie
			return Principal{}
		}
	}
	return Principal{NameType: nameType, Components: components}
}

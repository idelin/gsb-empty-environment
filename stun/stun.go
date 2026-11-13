// Package stun 对 STUN（RFC 5389 / RFC 8489）消息做严格的结构与完整性校验。
//
// 它解决的核心安全问题是：MESSAGE-INTEGRITY 之后的属性不受 HMAC 保护，
// 若服务端把这些追加属性（例如 TURN 的 XOR-PEER-ADDRESS）也交给业务处理，
// 攻击者就可以在截获的合法报文后面追加地址属性，把中继流量导向任意对端。
//
// Validate 的保证：
//   - 头部、长度字段、魔数（magic cookie）与 4 字节对齐全部合法；
//   - TLV 属性边界完整，补位字节必须为 0；
//   - MESSAGE-INTEGRITY（0x0008）恰好出现一次、长度为 20，且 HMAC-SHA1 校验通过；
//   - FINGERPRINT（0x8028）若存在，必须是最后一个属性且 CRC-32 校验通过；
//   - Message.Attrs 只返回 MESSAGE-INTEGRITY 之前的属性；其后除
//     MESSAGE-INTEGRITY-SHA256（0x001C）与 FINGERPRINT 之外的属性一律剥离，
//     永远不会进入业务处理（仅通过 IgnoredAfterIntegrity 计数暴露）。
package stun

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 头部与固定常量。
const (
	HeaderLength      = 20
	MagicCookie       = 0x2112A442
	AttributePadding  = 4
	IntegrityLength   = 20 // MESSAGE-INTEGRITY 的 HMAC-SHA1 输出长度。
	FingerprintLength = 4  // FINGERPRINT 的 CRC-32 输出长度。
	FingerprintXOR    = 0x5354554E
)

// 属性类型。
const (
	AttrMessageIntegrity       uint16 = 0x0008
	AttrMessageIntegritySHA256 uint16 = 0x001C
	AttrFingerprint            uint16 = 0x8028
)

// 可由 errors.Is 识别的校验失败原因。
var (
	ErrShortMessage         = errors.New("stun: 消息短于 20 字节头部")
	ErrBadMagicCookie       = errors.New("stun: magic cookie 不是 0x2112A442")
	ErrBadMessageLength     = errors.New("stun: 头部长度字段与属性总长度不符或不是 4 的倍数")
	ErrAttributeOverflow    = errors.New("stun: 属性 TLV 超出消息边界")
	ErrBadPadding           = errors.New("stun: 属性对齐补位字节非零")
	ErrMissingIntegrity     = errors.New("stun: 缺少 MESSAGE-INTEGRITY 属性")
	ErrDuplicateIntegrity   = errors.New("stun: MESSAGE-INTEGRITY 出现两次")
	ErrBadIntegrityLength   = errors.New("stun: MESSAGE-INTEGRITY 长度不是 20")
	ErrIntegrityMismatch    = errors.New("stun: MESSAGE-INTEGRITY HMAC-SHA1 校验失败")
	ErrFingerprintNotLast   = errors.New("stun: FINGERPRINT 必须是最后一个属性")
	ErrDuplicateFingerprint = errors.New("stun: FINGERPRINT 出现两次")
	ErrBadFingerprintLength = errors.New("stun: FINGERPRINT 长度不是 4")
	ErrBadFingerprint       = errors.New("stun: FINGERPRINT CRC-32 校验失败")
)

// Attribute 是一个已解析的 STUN TLV 属性。Value 是消息字节的独立拷贝，
// 调用方修改它不会影响原始输入。
type Attribute struct {
	Type  uint16
	Value []byte
}

// Message 是一次成功校验后的结果。
type Message struct {
	// Method 与 Class 取自消息类型字段的两个最高位必须为 0 的约定不做强制，
	// 这里原样返回头部前两个字节（消息类型，含方法与类别）。
	Method uint16

	// TransactionID 是头部末尾的 12 字节事务 ID（独立拷贝）。
	TransactionID []byte

	// Attrs 只包含 MESSAGE-INTEGRITY 之前的属性，业务逻辑应只消费这里的内容。
	Attrs []Attribute

	// IgnoredAfterIntegrity 是 MESSAGE-INTEGRITY 之后被剥离的、
	// 非 MESSAGE-INTEGRITY-SHA256/FINGERPRINT 的属性数量。
	IgnoredAfterIntegrity int

	// FingerprintPresent 表示消息是否携带 FINGERPRINT；携带时其 CRC-32 已校验通过。
	FingerprintPresent bool
}

// Validate 校验完整 STUN 消息。key 是长期凭据机制推导出的密钥
// （即 RFC 5389 的 HMAC 密钥本身；密钥推导由调用方完成）。
// 校验失败时返回包装在上述哨兵错误上的非 nil error，成功时返回解析结果。
// 输入消息不会被修改。
func Validate(message []byte, key []byte) (*Message, error) {
	if len(message) < HeaderLength {
		return nil, ErrShortMessage
	}

	msgType := binary.BigEndian.Uint16(message[0:2])
	lengthField := int(binary.BigEndian.Uint16(message[2:4]))
	cookie := binary.BigEndian.Uint32(message[4:8])

	if cookie != MagicCookie {
		return nil, ErrBadMagicCookie
	}

	// 长度字段必须等于属性区总长度、是 4 的倍数，且不能超出实际缓冲区。
	if lengthField%AttributePadding != 0 ||
		HeaderLength+lengthField != len(message) {
		return nil, ErrBadMessageLength
	}

	var (
		attrs          []locatedAttr
		integrityIdx   = -1
		fingerprintIdx = -1
	)

	// 按 TLV 顺序解析整个属性区。
	for offset := HeaderLength; offset < len(message); {
		if offset+4 > len(message) {
			return nil, ErrAttributeOverflow
		}
		attrType := binary.BigEndian.Uint16(message[offset : offset+2])
		valueLen := int(binary.BigEndian.Uint16(message[offset+2 : offset+4]))

		dataLo := offset + 4
		dataHi := dataLo + valueLen
		if dataHi > len(message) {
			return nil, ErrAttributeOverflow
		}

		// 值按 4 字节对齐补零，补位字节必须为 0。
		paddedHi := dataLo + paddedLength(valueLen)
		if paddedHi > len(message) {
			return nil, ErrAttributeOverflow
		}
		for _, pad := range message[dataHi:paddedHi] {
			if pad != 0 {
				return nil, ErrBadPadding
			}
		}

		index := len(attrs)
		switch attrType {
		case AttrMessageIntegrity:
			if integrityIdx >= 0 {
				return nil, ErrDuplicateIntegrity
			}
			if valueLen != IntegrityLength {
				return nil, ErrBadIntegrityLength
			}
			integrityIdx = index
		case AttrFingerprint:
			if fingerprintIdx >= 0 {
				return nil, ErrDuplicateFingerprint
			}
			if valueLen != FingerprintLength {
				return nil, ErrBadFingerprintLength
			}
			fingerprintIdx = index
		}

		attrs = append(attrs, locatedAttr{
			typ:    attrType,
			start:  offset,
			dataLo: dataLo,
			dataHi: dataHi,
		})
		offset = paddedHi
	}

	if integrityIdx < 0 {
		return nil, ErrMissingIntegrity
	}

	// FINGERPRINT 若存在必须是最后一个属性（RFC 5389 规定它只能位于消息末尾）。
	if fingerprintIdx >= 0 && fingerprintIdx != len(attrs)-1 {
		return nil, ErrFingerprintNotLast
	}

	if err := verifyIntegrity(message, key, attrs[integrityIdx]); err != nil {
		return nil, err
	}
	if fingerprintIdx >= 0 {
		if err := verifyFingerprint(message, attrs[fingerprintIdx]); err != nil {
			return nil, err
		}
	}

	result := &Message{
		Method:        msgType,
		TransactionID: append([]byte(nil), message[8:HeaderLength]...),
		Attrs:         make([]Attribute, 0, integrityIdx),
	}

	// 只把 MESSAGE-INTEGRITY 之前的属性交给业务；其后的未知属性
	// （包括攻击者追加的 XOR-PEER-ADDRESS 等）一律忽略。
	for index, attr := range attrs {
		switch {
		case index < integrityIdx:
			result.Attrs = append(result.Attrs, Attribute{
				Type:  attr.typ,
				Value: append([]byte(nil), message[attr.dataLo:attr.dataHi]...),
			})
		case index == integrityIdx:
			continue
		case attr.typ == AttrMessageIntegritySHA256 || attr.typ == AttrFingerprint:
			continue
		default:
			result.IgnoredAfterIntegrity++
		}
	}
	if fingerprintIdx >= 0 {
		result.FingerprintPresent = true
	}

	return result, nil
}

// paddedLength 返回补齐到 4 字节边界后的属性值长度。
func paddedLength(valueLen int) int {
	return (valueLen + AttributePadding - 1) &^ (AttributePadding - 1)
}

// verifyIntegrity 按 RFC 5389 15.4 校验 MESSAGE-INTEGRITY：
// 取头部到该属性之前的字节，把头部长度字段重算为“截止到该属性末尾”的长度，
// 再对这段内容计算 HMAC-SHA1。
func verifyIntegrity(message []byte, key []byte, integrity locatedAttr) error {
	// 重算后的长度 = MESSAGE-INTEGRITY 属性末尾偏移 - 20 字节头部。
	recomputedLength := integrity.start + 4 + IntegrityLength - HeaderLength

	signed := make([]byte, integrity.start)
	copy(signed, message[:integrity.start])
	binary.BigEndian.PutUint16(signed[2:4], uint16(recomputedLength))

	mac := hmac.New(sha1.New, key)
	mac.Write(signed)
	expected := mac.Sum(nil)

	got := message[integrity.dataLo:integrity.dataHi]
	if !hmac.Equal(got, expected) {
		return ErrIntegrityMismatch
	}
	return nil
}

// verifyFingerprint 按 RFC 5389 15.5 校验 FINGERPRINT：
// 对该属性之前的全部字节做 CRC-32（IEEE 802.3 多项式），再异或 0x5354554E。
func verifyFingerprint(message []byte, fingerprint locatedAttr) error {
	prefix := message[:fingerprint.start]
	checksum := crc32.ChecksumIEEE(prefix) ^ FingerprintXOR

	expected := make([]byte, FingerprintLength)
	binary.BigEndian.PutUint32(expected, checksum)

	got := message[fingerprint.dataLo:fingerprint.dataHi]
	if !bytes.Equal(got, expected) {
		return ErrBadFingerprint
	}
	return nil
}

// locatedAttr 仅在校验流程内部使用，描述属性在原始报文中的位置。
type locatedAttr struct {
	typ    uint16
	start  int // 属性头部在 message 中的偏移
	dataLo int // 值起始偏移
	dataHi int // 值结束偏移（不含补位）
}

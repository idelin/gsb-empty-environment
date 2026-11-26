// Package stunverify 对 ICE/TURN 使用的 STUN(RFC 5389/8489)消息做完整性与结构校验。
//
// 服务端处理 STUN 请求前必须先调用 Verify：只有通过 MESSAGE-INTEGRITY 校验、
// 且通过本包解析返回的属性才允许进入后续业务逻辑。MESSAGE-INTEGRITY 之后追加的
// 地址类属性（例如 XOR-PEER-ADDRESS）不处于 HMAC 保护范围内，本包会直接拒绝，
// 防止攻击者通过追加属性把中继流量导向任意对端。
package stunverify

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
)

// STUN 头部固定长度与固定魔数（RFC 5389 第 6 节）。
const (
	HeaderSize      = 20
	MagicCookie     = 0x2112A442
	FingerprintXOR  = 0x5354554E
	headerLengthOff = 2
)

// 完整性与指纹相关属性类型。
const (
	AttrMessageIntegrity       uint16 = 0x0008
	AttrMessageIntegritySHA256 uint16 = 0x001C
	AttrFingerprint            uint16 = 0x8028
)

// 完整性/指纹属性的固定长度。
const (
	messageIntegrityLength       = 20 // HMAC-SHA1 输出
	messageIntegritySHA256Length = 32 // HMAC-SHA256 输出
	fingerprintLength            = 4
)

var (
	// ErrShortMessage 消息不足 20 字节，无法构成 STUN 头部。
	ErrShortMessage = errors.New("stunverify: message shorter than 20-byte header")
	// ErrBadCookie 魔数不是 0x2112A442，可能是非 STUN 消息或旧版 RFC 5389 之前的格式。
	ErrBadCookie = errors.New("stunverify: magic cookie mismatch")
	// ErrLengthMismatch 头部长度字段与实际属性区长度不一致，或不是 4 的倍数。
	ErrLengthMismatch = errors.New("stunverify: header length does not match actual attributes or not 32-bit aligned")
	// ErrAttributeOverflow 属性声明长度越过消息边界。
	ErrAttributeOverflow = errors.New("stunverify: attribute length exceeds message boundary")
	// ErrBadPadding 属性补位不是全 0，或补位越界。
	ErrBadPadding = errors.New("stunverify: non-zero or truncated padding")
	// ErrIntegrityLength MESSAGE-INTEGRITY 长度不是 20 字节。
	ErrIntegrityLength = errors.New("stunverify: MESSAGE-INTEGRITY must be 20 bytes")
	// ErrIntegritySHA256Length MESSAGE-INTEGRITY-SHA256 长度不是 32 字节。
	ErrIntegritySHA256Length = errors.New("stunverify: MESSAGE-INTEGRITY-SHA256 must be 32 bytes")
	// ErrFingerprintLength FINGERPRINT 长度不是 4 字节。
	ErrFingerprintLength = errors.New("stunverify: FINGERPRINT must be 4 bytes")
	// ErrDuplicateIntegrity MESSAGE-INTEGRITY 出现两次。
	ErrDuplicateIntegrity = errors.New("stunverify: duplicate MESSAGE-INTEGRITY attribute")
	// ErrDuplicateIntegritySHA256 MESSAGE-INTEGRITY-SHA256 出现两次。
	ErrDuplicateIntegritySHA256 = errors.New("stunverify: duplicate MESSAGE-INTEGRITY-SHA256 attribute")
	// ErrDuplicateFingerprint FINGERPRINT 出现两次。
	ErrDuplicateFingerprint = errors.New("stunverify: duplicate FINGERPRINT attribute")
	// ErrAttributeAfterIntegrity MESSAGE-INTEGRITY 之后出现了只允许范围之外的属性。
	ErrAttributeAfterIntegrity = errors.New("stunverify: only MESSAGE-INTEGRITY-SHA256 and FINGERPRINT may follow MESSAGE-INTEGRITY")
	// ErrAttributeAfterSHA256 MESSAGE-INTEGRITY-SHA256 之后只允许 FINGERPRINT。
	ErrAttributeAfterSHA256 = errors.New("stunverify: only FINGERPRINT may follow MESSAGE-INTEGRITY-SHA256")
	// ErrTrailingAfterFingerprint FINGERPRINT 之后仍有属性，它必须是最后一个属性。
	ErrTrailingAfterFingerprint = errors.New("stunverify: FINGERPRINT must be the last attribute")
	// ErrMissingIntegrity 消息缺少 MESSAGE-INTEGRITY，无法验证长期凭据完整性。
	ErrMissingIntegrity = errors.New("stunverify: MESSAGE-INTEGRITY attribute is required")
	// ErrBadIntegrity HMAC 校验失败（密钥不符或消息被篡改）。
	ErrBadIntegrity = errors.New("stunverify: MESSAGE-INTEGRITY HMAC verification failed")
	// ErrBadIntegritySHA256 HMAC-SHA256 校验失败。
	ErrBadIntegritySHA256 = errors.New("stunverify: MESSAGE-INTEGRITY-SHA256 HMAC verification failed")
	// ErrBadFingerprint FINGERPRINT 的 CRC-32 校验失败，或其位置不合法。
	ErrBadFingerprint = errors.New("stunverify: FINGERPRINT CRC-32 verification failed")
)

// Attribute 是通过校验的 STUN TLV 属性（仅限 MESSAGE-INTEGRITY 之前的受保护属性）。
// Value 是独立拷贝，调用方修改不会影响原始消息。
type Attribute struct {
	Type  uint16
	Value []byte
}

// Result 是 Verify 成功后的结果。Attributes 按消息中的出现顺序排列，
// 且全部位于 MESSAGE-INTEGRITY 之前、处于 HMAC 保护范围内，可安全用于业务解析。
type Result struct {
	// MessageType 是头部前 2 字节的原始消息类型（含 method 与 class）。
	MessageType uint16
	// TransactionID 是头部最后 12 字节事务 ID 的拷贝。
	TransactionID []byte
	// Attributes 仅包含 MESSAGE-INTEGRITY 之前的属性。
	Attributes []Attribute
}

// Method 提取消息类型中的 12 位 method。
func (r *Result) Method() uint16 {
	t := r.MessageType
	return (t & 0x000F) | ((t & 0x00E0) >> 1) | ((t & 0x0E00) >> 2) | ((t & 0x3000) >> 2)
}

// Class 提取消息类型中的 2 位 class（0=request,1=indication,2=success,3=error）。
func (r *Result) Class() uint16 {
	t := r.MessageType
	return ((t >> 4) & 0x1) | ((t >> 7) & 0x2)
}

// Attribute 按类型查找第一个受保护属性；不存在时返回 nil, false。
func (r *Result) Attribute(typ uint16) (*Attribute, bool) {
	for i := range r.Attributes {
		if r.Attributes[i].Type == typ {
			return &r.Attributes[i], true
		}
	}
	return nil, false
}

// 解析阶段：属性相对于 MESSAGE-INTEGRITY 的位置。
const (
	stageBeforeIntegrity = iota
	stageAfterSHA1
	stageAfterSHA256
	stageAfterFingerprint
)

// Verify 校验一条 STUN 消息。
//
// message 为完整消息字节；key 为长期凭据机制（RFC 5389 第 15.4 节）派生出的
// HMAC 密钥（例如 TURN 的长期凭据密钥）。校验通过后返回的 Result 只暴露
// MESSAGE-INTEGRITY 之前的属性，完整性之后的非法属性会导致整条消息被拒绝。
func Verify(message, key []byte) (*Result, error) {
	if len(message) < HeaderSize {
		return nil, ErrShortMessage
	}
	if binary.BigEndian.Uint32(message[4:8]) != MagicCookie {
		return nil, ErrBadCookie
	}

	declared := int(binary.BigEndian.Uint16(message[headerLengthOff : headerLengthOff+2]))
	if declared%4 != 0 || HeaderSize+declared != len(message) {
		return nil, ErrLengthMismatch
	}

	result := &Result{
		MessageType:   binary.BigEndian.Uint16(message[0:2]),
		TransactionID: append([]byte(nil), message[8:20]...),
		Attributes:    make([]Attribute, 0),
	}

	stage := stageBeforeIntegrity
	offset := HeaderSize
	end := len(message)

	for offset < end {
		if end-offset < 4 {
			return nil, ErrAttributeOverflow
		}
		attrType := binary.BigEndian.Uint16(message[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(message[offset+2 : offset+4]))
		valueStart := offset + 4
		valueEnd := valueStart + attrLen
		if valueEnd > end {
			return nil, ErrAttributeOverflow
		}

		pad := (4 - attrLen%4) % 4
		if valueEnd+pad > end {
			return nil, ErrBadPadding
		}
		for _, b := range message[valueEnd : valueEnd+pad] {
			if b != 0 {
				return nil, ErrBadPadding
			}
		}

		switch attrType {
		case AttrMessageIntegrity:
			if stage != stageBeforeIntegrity {
				return nil, ErrDuplicateIntegrity
			}
			if attrLen != messageIntegrityLength {
				return nil, ErrIntegrityLength
			}
			if err := checkHMAC(sha1.New, messageIntegrityLength, message, offset, key, ErrBadIntegrity); err != nil {
				return nil, err
			}
			stage = stageAfterSHA1

		case AttrMessageIntegritySHA256:
			switch stage {
			case stageBeforeIntegrity:
				// 没有 SHA1 的消息也允许仅用 SHA256 完整性（RFC 8489）。
			case stageAfterSHA1:
			default:
				return nil, ErrDuplicateIntegritySHA256
			}
			if attrLen != messageIntegritySHA256Length {
				return nil, ErrIntegritySHA256Length
			}
			if err := checkHMAC(sha256.New, messageIntegritySHA256Length, message, offset, key, ErrBadIntegritySHA256); err != nil {
				return nil, err
			}
			stage = stageAfterSHA256

		case AttrFingerprint:
			switch stage {
			case stageBeforeIntegrity:
				return nil, ErrMissingIntegrity
			case stageAfterFingerprint:
				return nil, ErrDuplicateFingerprint
			}
			if attrLen != fingerprintLength {
				return nil, ErrFingerprintLength
			}
			if err := checkFingerprint(message, offset, valueStart); err != nil {
				return nil, err
			}
			stage = stageAfterFingerprint

		default:
			switch stage {
			case stageBeforeIntegrity:
				result.Attributes = append(result.Attributes, Attribute{
					Type:  attrType,
					Value: append([]byte(nil), message[valueStart:valueEnd]...),
				})
			case stageAfterSHA1:
				return nil, ErrAttributeAfterIntegrity
			case stageAfterSHA256:
				return nil, ErrAttributeAfterSHA256
			case stageAfterFingerprint:
				return nil, ErrTrailingAfterFingerprint
			}
		}

		offset = valueEnd + pad
	}

	if stage == stageBeforeIntegrity {
		return nil, ErrMissingIntegrity
	}
	return result, nil
}

// checkHMAC 按 RFC 5389 第 15.4 节计算完整性属性的 HMAC。
//
// HMAC 输入为“头部到该属性之前”的字节，但头部长度字段要临时改写为
// “截止到该完整性属性末尾”的长度（即把当前属性自身的 4 字节 TLV 头与
// macLen 字节 MAC 计入）。为避免修改入参消息，这里分两段写入哈希：
// 先写改写过长度字段的前 20 字节头部，再写属性区到完整性属性之前的部分。
func checkHMAC(newHash func() hash.Hash, macLen int, message []byte, attrOffset int, key []byte, mismatch error) error {
	mac := hmac.New(newHash, key)

	var header [HeaderSize]byte
	copy(header[:], message[:HeaderSize])
	binary.BigEndian.PutUint16(header[headerLengthOff:headerLengthOff+2],
		uint16(attrOffset+4+macLen-HeaderSize))
	mac.Write(header[:])
	mac.Write(message[HeaderSize:attrOffset])

	expected := mac.Sum(nil)
	got := message[attrOffset+4 : attrOffset+4+macLen]
	if !hmac.Equal(got, expected) {
		return mismatch
	}
	return nil
}

// checkFingerprint 按 RFC 5389 第 15.5 节校验 FINGERPRINT：对该属性之前的
// 全部字节计算 IEEE CRC-32，再与 0x5354554E 异或比较。生成方是先在不含
// FINGERPRINT 的消息上算 CRC 再追加该属性，因此这里要把头部长度字段临时
// 改写为剔除 FINGERPRINT 自身 8 字节 TLV 后的值。FINGERPRINT 必须是最后一个
// 属性这一点由调用方的阶段状态机保证。
func checkFingerprint(message []byte, attrOffset, valueStart int) error {
	var header [HeaderSize]byte
	copy(header[:], message[:HeaderSize])
	binary.BigEndian.PutUint16(header[headerLengthOff:headerLengthOff+2],
		uint16(attrOffset-HeaderSize))

	crc := crc32.NewIEEE()
	crc.Write(header[:])
	crc.Write(message[HeaderSize:attrOffset])
	fp := crc.Sum32() ^ FingerprintXOR
	got := binary.BigEndian.Uint32(message[valueStart : valueStart+fingerprintLength])
	if got != fp {
		return ErrBadFingerprint
	}
	return nil
}

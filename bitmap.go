package netsec

import (
	"errors"
	"fmt"
	"io"
	"log"
)

// FieldKind 描述 ISO 8583 数据元素的长度编码方式。
type FieldKind int

const (
	// Fixed 为定长字段，字段数据固定占 MaxLength 字节。
	Fixed FieldKind = iota
	// LLVAR 为二变长字段，长度前缀占 1 字节（LL，二进制计数）。
	LLVAR
	// LLLVAR 为三变长字段，长度前缀占 2 字节（LLL，大端二进制计数）。
	LLLVAR
)

const (
	minFieldNumber = 2
	maxFieldNumber = 128
	primaryBits    = 64
	secondaryBits  = 64
	primaryBytes   = primaryBits / 8
	secondaryBytes = secondaryBits / 8
	pinFieldNumber = 52
	pinBlockLength = 8
)

var (
	// ErrTruncated 表示报文在读完位图或某个字段前耗尽。
	ErrTruncated = errors.New("netsec: truncated message")
	// ErrUndefinedBit 表示位图置上了调用方字段表中未登记的字段。
	ErrUndefinedBit = errors.New("netsec: undefined bitmap field")
	// ErrLengthOverflow 表示 LLVAR/LLLVAR 声明长度超过剩余字节或字段上限。
	ErrLengthOverflow = errors.New("netsec: variable length exceeds remaining bytes or field limit")
	// ErrPINForbidden 表示 52 域出现在未登记的消息类型中，或长度不合法。
	ErrPINForbidden = errors.New("netsec: PIN block field not allowed for message type")
	// ErrInvalidConfig 表示调用方提供的字段表或消息类型配置不合法。
	ErrInvalidConfig = errors.New("netsec: invalid admission configuration")
	// ErrInvalidMTI 表示消息类型标识符不是 4 位数字。
	ErrInvalidMTI = errors.New("netsec: invalid message type identifier")
)

// logger 是包内唯一的日志出口，默认丢弃全部输出。
// 该变量不对外暴露，调用方无法重新启用日志，保证敏感报文不落日志。
var logger = log.New(io.Discard, "", 0)

// FieldSpec 是调用方登记的单个数据元素定义。
type FieldSpec struct {
	// Number 为 ISO 8583 字段号，范围 2..128；字段 1 是次级位图标志，不可登记。
	Number int
	// Kind 为定长或变长编码方式。
	Kind FieldKind
	// MaxLength 为字段允许的最大字节数；定长字段必须恰好占该长度。
	MaxLength int
}

// Admission 是供报文解码阶段调用的位图准入器。
// 字段表与允许携带 PIN 块的消息类型均由调用方在构造时给出，运行期不可变。
type Admission struct {
	fields map[int]FieldSpec
	pinMTI map[string]bool
}

// NewAdmission 用调用方提供的字段表和允许 52 域的消息类型构造准入器。
func NewAdmission(fields []FieldSpec, pinMTIs []string) (*Admission, error) {
	a := &Admission{
		fields: make(map[int]FieldSpec, len(fields)),
		pinMTI: make(map[string]bool, len(pinMTIs)),
	}
	for _, spec := range fields {
		if spec.Number < minFieldNumber || spec.Number > maxFieldNumber {
			return nil, fieldError(spec.Number, "field number out of range 2..128")
		}
		if spec.MaxLength <= 0 {
			return nil, fieldError(spec.Number, "field max length must be positive")
		}
		switch spec.Kind {
		case Fixed, LLVAR, LLLVAR:
		default:
			return nil, fieldError(spec.Number, "unknown field kind")
		}
		if _, exists := a.fields[spec.Number]; exists {
			return nil, fieldError(spec.Number, "duplicate field definition")
		}
		a.fields[spec.Number] = spec
	}
	if pin, ok := a.fields[pinFieldNumber]; ok {
		if pin.Kind != Fixed || pin.MaxLength != pinBlockLength {
			return nil, fieldError(pinFieldNumber, "PIN block must be a fixed 8-byte field")
		}
	}
	for _, mti := range pinMTIs {
		if !validMTI(mti) {
			return nil, mtiError(mti, "registered MTI must be 4 digits")
		}
		a.pinMTI[mti] = true
	}
	return a, nil
}

// Admit 在解码前校验报文位图与字段长度是否满足准入策略。
// mti 为 4 位数字消息类型标识符，body 为去掉 MTI 与消息头之后、
// 从主位图开始的原始报文体。返回的 error 包装下列哨兵错误之一：
// ErrTruncated、ErrUndefinedBit、ErrLengthOverflow、
// ErrPINForbidden、ErrInvalidMTI。
func (a *Admission) Admit(mti string, body []byte) error {
	if !validMTI(mti) {
		return mtiError(mti, "MTI must be 4 digits")
	}

	bits, offset, err := parseBitmap(body)
	if err != nil {
		return err
	}

	for _, number := range bits {
		// 字段 1 表示次级位图存在，本身不是数据元素，无需在字段表登记。
		if number == 1 {
			continue
		}
		spec, ok := a.fields[number]
		if !ok {
			return undefinedFieldError(number, "bit set but field is not defined for this gateway")
		}
		if number == pinFieldNumber {
			if !a.pinMTI[mti] {
				return pinError("52 field present in message type that may not carry a PIN block")
			}
		}

		switch spec.Kind {
		case Fixed:
			if offset+spec.MaxLength > len(body) {
				return truncatedFieldError(number, "fixed field exceeds remaining bytes")
			}
			offset += spec.MaxLength
		case LLVAR:
			offset, err = a.consumeVariable(number, spec, body, offset, 1)
			if err != nil {
				return err
			}
		case LLLVAR:
			offset, err = a.consumeVariable(number, spec, body, offset, 2)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// consumeVariable 读取变长字段的长度前缀并校验声明长度，
// 返回包含该字段之后的新偏移量。
func (a *Admission) consumeVariable(number int, spec FieldSpec, body []byte, offset, prefixLen int) (int, error) {
	if offset+prefixLen > len(body) {
		return 0, truncatedFieldError(number, "length prefix exceeds remaining bytes")
	}
	length := 0
	for i := 0; i < prefixLen; i++ {
		length = length<<8 | int(body[offset+i])
	}
	offset += prefixLen
	if length > spec.MaxLength || offset+length > len(body) {
		return 0, lengthFieldError(number, "declared length exceeds field limit or remaining bytes")
	}
	return offset + length, nil
}

// parseBitmap 解析主位图，并仅在位 1 置位时解析次级位图。
// 返回按字段号升序排列的置位字段号、位图结束偏移量。
func parseBitmap(body []byte) ([]int, int, error) {
	if len(body) < primaryBytes {
		return nil, 0, truncatedError("primary bitmap")
	}
	hasSecondary := body[0]&0x80 != 0
	bitmapLen := primaryBytes
	if hasSecondary {
		bitmapLen += secondaryBytes
		if len(body) < bitmapLen {
			return nil, 0, truncatedError("secondary bitmap")
		}
	}

	bits := make([]int, 0, 16)
	for index, b := range body[:bitmapLen] {
		if b == 0 {
			continue
		}
		for bit := 7; bit >= 0; bit-- {
			if b&(1<<bit) != 0 {
				bits = append(bits, index*8+(8-bit))
			}
		}
	}
	return bits, bitmapLen, nil
}

func validMTI(mti string) bool {
	if len(mti) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if mti[i] < '0' || mti[i] > '9' {
			return false
		}
	}
	return true
}

func fieldError(number int, detail string) error {
	return fmt.Errorf("%w: field %d: %s", ErrInvalidConfig, number, detail)
}

func undefinedFieldError(number int, detail string) error {
	return fmt.Errorf("%w: field %d: %s", ErrUndefinedBit, number, detail)
}

func lengthFieldError(number int, detail string) error {
	return fmt.Errorf("%w: field %d: %s", ErrLengthOverflow, number, detail)
}

func truncatedFieldError(number int, detail string) error {
	return fmt.Errorf("%w: field %d: %s", ErrTruncated, number, detail)
}

func pinError(detail string) error {
	return fmt.Errorf("%w: %s", ErrPINForbidden, detail)
}

func mtiError(mti, detail string) error {
	return fmt.Errorf("%w: mti %q: %s", ErrInvalidMTI, mti, detail)
}

func truncatedError(what string) error {
	return fmt.Errorf("%w: while reading %s", ErrTruncated, what)
}

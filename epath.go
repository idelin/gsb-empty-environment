package netsec

import (
	"errors"
	"fmt"
)

// CIP 逻辑段（Logical Segment）编码常量，见 CIP 规范 Vol.1 Appendix C。
const (
	segmentTypeMask  byte = 0xE0 // 段类型占高 3 位
	segmentLogical   byte = 0x20 // 逻辑段类型固定为 001b
	logicalTypeMask  byte = 0x1C // 逻辑类型占 bit2..bit4
	logicalWidthMask byte = 0x03 // bit1..bit0：00b=8 位，01b=16 位，10b=32 位
	width8Bit        byte = 0x00
	width16Bit       byte = 0x01
	width32Bit       byte = 0x02
	padByte          byte = 0x00

	logicalClassID     byte = 0x00 // 000b（bit2..bit4）
	logicalInstanceID  byte = 0x04 // 001b
	logicalMemberID    byte = 0x08 // 010b
	logicalAttributeID byte = 0x10 // 011b
)

// 解析阶段返回的哨兵错误，调用方可据此记录审计日志或映射 CIP 错误码。
var (
	ErrEmptyPath          = errors.New("netsec: EPATH 为空")
	ErrPathSizeMismatch   = errors.New("netsec: EPATH 字长与实际字节数不一致")
	ErrTruncatedSegment   = errors.New("netsec: EPATH 逻辑段被截断")
	ErrInvalidPadByte     = errors.New("netsec: EPATH 填充字节非法")
	ErrUnsupportedSegment = errors.New("netsec: EPATH 含不支持的段类型")
	ErrDuplicateSegment   = errors.New("netsec: EPATH 含重复逻辑段")
	ErrSegmentOrder       = errors.New("netsec: EPATH 逻辑段顺序非法")
	ErrMissingClass       = errors.New("netsec: EPATH 缺少类段")
)

// LogicalPath 是规范化后的 CIP 请求路径：无论线上是 8 位、16 位还是 32 位
// 逻辑段编码，类/实例/属性都被统一还原成数值，策略判断只基于数值进行。
type LogicalPath struct {
	Class       uint32
	Instance    uint32
	Attribute   uint32
	HasClass    bool
	HasInstance bool
	HasAttr     bool
}

// NormalizeEPATH 按 CPF 字长（pathSizeWords，1 字 = 2 字节）解析 EPATH，
// 还原类、实例、属性并核对路径字长与段完整性。
//
// 解析规则：
//   - 严格按声明字长截取，字节不足返回 ErrPathSizeMismatch；
//   - 支持 8/16/32 位逻辑段，16/32 位段自带的 0x00 填充字节必须合法；
//   - 8 位段后若需字对齐（下一段是 16/32 位段或路径结束），0x00 填充必须合法；
//   - 类段必须存在且位于最前，实例、属性按类→实例→属性顺序出现且不可重复；
//   - 端口段、网络段、成员段等非类/实例/属性段一律拒绝。
func NormalizeEPATH(pathSizeWords uint16, data []byte) (*LogicalPath, error) {
	if pathSizeWords == 0 {
		return nil, ErrEmptyPath
	}

	pathBytes := int(pathSizeWords) * 2
	if len(data) < pathBytes {
		return nil, fmt.Errorf("%w: 声明 %d 字（%d 字节），实际 %d 字节",
			ErrPathSizeMismatch, pathSizeWords, pathBytes, len(data))
	}
	buf := data[:pathBytes]

	lp := &LogicalPath{}
	pos := 0
	for pos < len(buf) {
		// 字对齐填充字节（8 位段间填充、路径末尾补齐）只允许出现在段边界。
		if buf[pos] == padByte {
			pos++
			continue
		}

		segment := buf[pos]
		if segment&segmentTypeMask != segmentLogical {
			return nil, fmt.Errorf("%w: 段字节 0x%02X", ErrUnsupportedSegment, segment)
		}

		logicalType := segment & logicalTypeMask
		switch logicalType {
		case logicalClassID, logicalInstanceID, logicalMemberID, logicalAttributeID:
		default:
			return nil, fmt.Errorf("%w: 逻辑类型 0x%02X", ErrUnsupportedSegment, logicalType)
		}
		if logicalType == logicalMemberID {
			return nil, fmt.Errorf("%w: 成员段（0x%02X）", ErrUnsupportedSegment, segment)
		}

		value, next, err := readLogicalSegment(segment, buf, pos)
		if err != nil {
			return nil, err
		}

		if err := lp.assign(logicalType, value); err != nil {
			return nil, err
		}
		pos = next
	}

	if !lp.HasClass {
		return nil, ErrMissingClass
	}
	return lp, nil
}

// assign 将一个逻辑段值按类→实例→属性的顺序与唯一性约束写入规范化路径。
func (lp *LogicalPath) assign(logicalType byte, value uint32) error {
	switch logicalType {
	case logicalClassID:
		if lp.HasClass {
			return fmt.Errorf("%w: 类段", ErrDuplicateSegment)
		}
		lp.Class = value
		lp.HasClass = true
	case logicalInstanceID:
		if !lp.HasClass {
			return fmt.Errorf("%w: 实例段前缺少类段", ErrSegmentOrder)
		}
		if lp.HasInstance {
			return fmt.Errorf("%w: 实例段", ErrDuplicateSegment)
		}
		lp.Instance = value
		lp.HasInstance = true
	case logicalAttributeID:
		if !lp.HasClass || !lp.HasInstance {
			return fmt.Errorf("%w: 属性段前缺少类/实例段", ErrSegmentOrder)
		}
		if lp.HasAttr {
			return fmt.Errorf("%w: 属性段", ErrDuplicateSegment)
		}
		lp.Attribute = value
		lp.HasAttr = true
	}
	return nil
}

// readLogicalSegment 读取从 pos 开始的一个逻辑段，返回段值与段结束后的偏移。
func readLogicalSegment(segment byte, buf []byte, pos int) (uint32, int, error) {
	switch segment & logicalWidthMask {
	case width8Bit:
		// 8 位逻辑段布局：[段字节, 值]
		valueAt := pos + 1
		if valueAt >= len(buf) {
			return 0, 0, fmt.Errorf("%w: 8 位段缺少值字节", ErrTruncatedSegment)
		}
		return uint32(buf[valueAt]), valueAt + 1, nil
	case width16Bit, width32Bit:
		// 16/32 位逻辑段布局：[段字节, 0x00 填充, 值（2 或 4 字节小端）]
	default:
		return 0, 0, fmt.Errorf("%w: 段字节 0x%02X 宽度编码保留", ErrUnsupportedSegment, segment)
	}

	padAt := pos + 1
	if padAt >= len(buf) {
		return 0, 0, fmt.Errorf("%w: 16/32 位段缺少填充字节", ErrTruncatedSegment)
	}
	if buf[padAt] != padByte {
		return 0, 0, fmt.Errorf("%w: 段字节 0x%02X 的填充为 0x%02X",
			ErrInvalidPadByte, segment, buf[padAt])
	}

	width := 2
	if segment&logicalWidthMask == width32Bit {
		width = 4
	}
	valueAt := padAt + 1
	if valueAt+width > len(buf) {
		return 0, 0, fmt.Errorf("%w: %d 位段值不完整", ErrTruncatedSegment, width*8)
	}

	value := uint32(buf[valueAt])
	for i := 1; i < width; i++ {
		value |= uint32(buf[valueAt+i]) << (8 * i)
	}
	return value, valueAt + width, nil
}

// String 以“类/实例/属性”形式输出规范化路径，便于日志审计。
func (lp *LogicalPath) String() string {
	s := fmt.Sprintf("class=0x%X", lp.Class)
	if lp.HasInstance {
		s += fmt.Sprintf(",instance=0x%X", lp.Instance)
	}
	if lp.HasAttr {
		s += fmt.Sprintf(",attribute=0x%X", lp.Attribute)
	}
	return s
}

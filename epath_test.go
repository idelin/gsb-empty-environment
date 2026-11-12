package netsec

import (
	"errors"
	"testing"
)

// buildEPATH 按真实 CIP 编码拼接逻辑段，返回（字长, 字节路径）。
// 16/32 位段自带段内填充；8 位段后若需保持字对齐，由本函数自动补 0x00。
func buildEPATH(segments ...[]byte) (uint16, []byte) {
	var path []byte
	for _, seg := range segments {
		// 上一段是 8 位段（2 字节）且当前是 16/32 位段（长度 >2）时插入对齐填充。
		if len(seg) > 2 && len(path)%2 == 1 {
			path = append(path, 0x00)
		}
		path = append(path, seg...)
	}
	if len(path)%2 != 0 {
		path = append(path, 0x00)
	}
	return uint16(len(path) / 2), path
}

func class8(v byte) []byte { return []byte{0x20, v} }
func inst8(v byte) []byte  { return []byte{0x24, v} }
func attr8(v byte) []byte  { return []byte{0x30, v} }
func class16(v uint16) []byte {
	return []byte{0x21, 0x00, byte(v), byte(v >> 8)}
}
func inst16(v uint16) []byte {
	return []byte{0x25, 0x00, byte(v), byte(v >> 8)}
}
func attr16(v uint16) []byte {
	return []byte{0x31, 0x00, byte(v), byte(v >> 8)}
}
func class32(v uint32) []byte {
	return []byte{0x22, 0x00, byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

func TestNormalizeEPATH_8BitSegments(t *testing.T) {
	words, path := buildEPATH(class8(1), inst8(2))
	lp, err := NormalizeEPATH(words, path)
	if err != nil {
		t.Fatalf("规范化失败: %v", err)
	}
	if !lp.HasClass || lp.Class != 1 || !lp.HasInstance || lp.Instance != 2 || lp.HasAttr {
		t.Fatalf("规范化结果异常: %+v", lp)
	}
}

// 攻击场景核心：同一个 0xF5 类，16 位段编码必须规范化出与 8 位段相同的类号。
func TestNormalizeEPATH_16BitSameClassAs8Bit(t *testing.T) {
	words8, path8 := buildEPATH(class8(0xF5), inst8(1))
	lp8, err := NormalizeEPATH(words8, path8)
	if err != nil {
		t.Fatalf("8 位路径解析失败: %v", err)
	}

	words16, path16 := buildEPATH(class16(0xF5), inst16(1))
	lp16, err := NormalizeEPATH(words16, path16)
	if err != nil {
		t.Fatalf("16 位路径解析失败: %v", err)
	}

	if lp8.Class != lp16.Class || lp8.Instance != lp16.Instance {
		t.Fatalf("规范化不一致: 8位=%+v, 16位=%+v", lp8, lp16)
	}
	if lp16.Class != 0xF5 || lp16.Instance != 1 {
		t.Fatalf("16 位段还原错误: %+v", lp16)
	}
}

// 混合编码：8 位类 + 16 位实例 + 16 位属性，含字对齐填充。
func TestNormalizeEPATH_Mixed8And16Bit(t *testing.T) {
	words, path := buildEPATH(class8(0xF5), inst16(0x0102), attr16(0x03))
	lp, err := NormalizeEPATH(words, path)
	if err != nil {
		t.Fatalf("混合路径解析失败: %v", err)
	}
	if lp.Class != 0xF5 || lp.Instance != 0x0102 || lp.Attribute != 0x03 || !lp.HasAttr {
		t.Fatalf("混合路径还原错误: %+v", lp)
	}
}

func TestNormalizeEPATH_32BitSegment(t *testing.T) {
	words, path := buildEPATH(class32(0x12345678), inst8(1))
	lp, err := NormalizeEPATH(words, path)
	if err != nil {
		t.Fatalf("32 位路径解析失败: %v", err)
	}
	if lp.Class != 0x12345678 || lp.Instance != 1 {
		t.Fatalf("32 位段还原错误: %+v", lp)
	}
}

// 字长错位一：声明字长超过实际数据。
func TestNormalizeEPATH_WordSizeTooLarge(t *testing.T) {
	_, path := buildEPATH(class16(0xF5), inst16(1))
	_, err := NormalizeEPATH(uint16(len(path)/2+1), path)
	if !errors.Is(err, ErrPathSizeMismatch) {
		t.Fatalf("期望 ErrPathSizeMismatch，得到 %v", err)
	}
}

// 字长错位二：声明字长小于实际路径，尾部段被按字长截断后不完整。
func TestNormalizeEPATH_WordSizeTooSmallTruncatesSegment(t *testing.T) {
	_, path := buildEPATH(class8(1), inst16(0x0102))
	// 完整路径为 6 字节（3 字），只声明 2 字：16 位实例段被切成 [25 00]，值不完整。
	_, err := NormalizeEPATH(2, path)
	if !errors.Is(err, ErrTruncatedSegment) {
		t.Fatalf("期望 ErrTruncatedSegment，得到 %v", err)
	}
}

func TestNormalizeEPATH_TruncatedValue(t *testing.T) {
	// 完整 16 位类段 [21 00 F5 00] 占 4 字节，但只声明 1 字（2 字节），
	// 字长校验通过后，段值在缓冲切片内被截断。
	path := []byte{0x21, 0x00, 0xF5, 0x00}
	_, err := NormalizeEPATH(1, path)
	if !errors.Is(err, ErrTruncatedSegment) {
		t.Fatalf("期望 ErrTruncatedSegment，得到 %v", err)
	}
}

func TestNormalizeEPATH_InvalidPadByte(t *testing.T) {
	// 16 位类段的填充字节非 0，后续实例段保证解析进入该分支而非在末尾截断。
	path := []byte{0x21, 0xAA, 0xF5, 0x00, 0x24, 0x01, 0x00, 0x00}
	_, err := NormalizeEPATH(4, path)
	if !errors.Is(err, ErrInvalidPadByte) {
		t.Fatalf("期望 ErrInvalidPadByte，得到 %v", err)
	}
}

func TestNormalizeEPATH_PortSegmentRejected(t *testing.T) {
	// 0xA1 是端口段，不是逻辑段。
	words, path := buildEPATH([]byte{0xA1, 0x01}, class8(1))
	_, err := NormalizeEPATH(words, path)
	if !errors.Is(err, ErrUnsupportedSegment) {
		t.Fatalf("期望 ErrUnsupportedSegment，得到 %v", err)
	}
}

func TestNormalizeEPATH_MissingClass(t *testing.T) {
	words, path := buildEPATH(inst8(1))
	_, err := NormalizeEPATH(words, path)
	if !errors.Is(err, ErrSegmentOrder) {
		t.Fatalf("实例段缺少类段，期望 ErrSegmentOrder，得到 %v", err)
	}
}

func TestNormalizeEPATH_OnlyPaddingHasNoClass(t *testing.T) {
	// 路径里只有字对齐填充，遍历结束仍未出现类段。
	_, err := NormalizeEPATH(1, []byte{0x00, 0x00})
	if !errors.Is(err, ErrMissingClass) {
		t.Fatalf("期望 ErrMissingClass，得到 %v", err)
	}
}

func TestNormalizeEPATH_EmptyPath(t *testing.T) {
	if _, err := NormalizeEPATH(0, nil); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("期望 ErrEmptyPath，得到 %v", err)
	}
}

func TestLogicalPath_String(t *testing.T) {
	lp := &LogicalPath{Class: 0xF5, HasClass: true, Instance: 1, HasInstance: true}
	if got := lp.String(); got != "class=0xF5,instance=0x1" {
		t.Fatalf("String 输出异常: %q", got)
	}
}

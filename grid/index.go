package grid

import (
	"errors"
	"strconv"
)

// MaxRes 最大分辨率（每级 3 bit，共 15 级）。
const MaxRes = 15

const (
	modeCell   uint64 = 1
	modeOffset        = 59
	resOffset         = 52
	baseOffset        = 45
)

// ErrInvalidIndex 非法索引。
var ErrInvalidIndex = errors.New("grid: invalid index")

// ErrInvalidResolution 非法分辨率。
var ErrInvalidResolution = errors.New("grid: invalid resolution")

// Index 64 位分层网格索引，位布局（H3 风格）：
//
//	63        60 59     56 55     52 51          45 44                 0
//	[ reserved ][  mode  ][   res   ][  base cell  ][ d1 d2 ... d15      ]
//
// mode=1 表示六边形单元；res 0..15；base cell 0..19；
// 每级 digit 占 3 bit（0..6），未使用的低位 digit 置 7。
type Index uint64

// makeIndex 由分量组装索引。digits 长度必须 <= res，不足左侧补 0（中心方向）。
func makeIndex(res, baseCell int, digits []int) Index {
	h := modeCell << modeOffset
	h |= uint64(res) << resOffset
	h |= uint64(baseCell) << baseOffset
	for i := 0; i < MaxRes; i++ {
		d := 7
		if i < len(digits) {
			d = digits[i]
		}
		h |= uint64(d) << (3 * (MaxRes - 1 - i))
	}
	return Index(h)
}

// Mode 索引模式（1 = 六边形单元）。
func (h Index) Mode() int { return int(uint64(h) >> modeOffset) }

// Res 分辨率 0..15。
func (h Index) Res() int { return int((uint64(h) >> resOffset) & 0xf) }

// BaseCell 基础单元（二十面体面编号 0..19）。
func (h Index) BaseCell() int { return int((uint64(h) >> baseOffset) & 0x7f) }

// Digit 第 res 级（1 起）的方向数字 0..6。
func (h Index) Digit(res int) int {
	return int((uint64(h) >> (3 * (MaxRes - res))) & 7)
}

// digits 取出全部 res 级 digit（粗 -> 细）。
func (h Index) digits() []int {
	res := h.Res()
	digits := make([]int, res)
	for i := 1; i <= res; i++ {
		digits[i-1] = h.Digit(i)
	}
	return digits
}

// IsValid 校验索引结构合法性。
func (h Index) IsValid() bool {
	if h.Mode() != int(modeCell) {
		return false
	}
	res := h.Res()
	if res < 0 || res > MaxRes {
		return false
	}
	if h.BaseCell() < 0 || h.BaseCell() >= NumBaseCells {
		return false
	}
	for i := 1; i <= MaxRes; i++ {
		d := h.Digit(i)
		if i <= res {
			if d < 0 || d > 6 {
				return false
			}
		} else if d != 7 {
			return false
		}
	}
	return true
}

// String 编码为十六进制字符串（如 "88283082bffffff" 风格）。
func (h Index) String() string { return strconv.FormatUint(uint64(h), 16) }

// Parse 解析十六进制字符串索引。
func Parse(s string) (Index, error) {
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, err
	}
	h := Index(v)
	if !h.IsValid() {
		return 0, ErrInvalidIndex
	}
	return h, nil
}

// Package imgcodec 实现一个基于三类指令的图像编解码器：
// 字面段（Literal）、重复段（Repeat）与上一行引用（Ref）。
// 每类指令有固定头成本，编码器对整图求最短编码；
// 引用长度有上限且不能跨行。
package imgcodec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// InstTag 指令类型。
type InstTag byte

const (
	TagLiteral InstTag = 0 // 字面段：头部 + n 字节像素
	TagRepeat  InstTag = 1 // 重复段：头部 + 1 字节，重复 n 次
	TagRef     InstTag = 2 // 上一行引用：仅头部，从上一行同列复制 n 字节
)

// Config 成本模型与引用约束。
type Config struct {
	HeaderLiteral int // 字面段固定头成本（另加 n 字节负载成本）
	HeaderRepeat  int // 重复段固定头成本（另加 1 字节负载成本）
	HeaderRef     int // 引用段固定头成本（无负载）
	MaxRefLen     int // 单条引用指令的最大长度，必须 >= 1
}

// DefaultConfig 返回一组合理的默认成本。
func DefaultConfig() Config {
	return Config{HeaderLiteral: 2, HeaderRepeat: 2, HeaderRef: 3, MaxRefLen: 64}
}

// 线格式：tag(1) + len(uint16 LE) + payload。
// Literal 负载 n 字节；Repeat 负载 1 字节；Ref 无负载。
const instHeaderSize = 3

// maxInstLen 是单条指令可编码的最大像素数（线格式长度字段为 uint16）。
const maxInstLen = 0xFFFF

// Inst 是一条解码后的指令（仅用于检查/调试）。
type Inst struct {
	Tag     InstTag
	Len     int
	Payload []byte // Literal 为 n 字节，Repeat 为 1 字节，Ref 为 nil
}

// DecodeError 描述解码失败，并定位到出错的指令。
type DecodeError struct {
	InstIndex int    // 出错指令的序号（从 0 开始）
	Offset    int    // 出错指令在输入流中的字节偏移
	Reason    string // 失败原因
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("imgcodec: 指令 #%d (偏移 %d): %s", e.InstIndex, e.Offset, e.Reason)
}

// Encode 对 w*h 的图像求最短编码，返回指令流字节。
// 图像按行优先存储，每像素 1 字节。
func Encode(img []byte, w, h int, cfg Config) ([]byte, error) {
	if err := checkParams(img, w, h, cfg); err != nil {
		return nil, err
	}
	var out []byte
	for y := 0; y < h; y++ {
		row := img[y*w : (y+1)*w]
		var prev []byte
		if y > 0 {
			prev = img[(y-1)*w : y*w]
		}
		out = append(out, encodeRow(row, prev, cfg)...)
	}
	return out, nil
}

func checkParams(img []byte, w, h int, cfg Config) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("imgcodec: 非法尺寸 %dx%d", w, h)
	}
	if len(img) != w*h {
		return fmt.Errorf("imgcodec: 像素数 %d 与尺寸 %dx%d 不符", len(img), w, h)
	}
	if cfg.MaxRefLen < 1 {
		return errors.New("imgcodec: MaxRefLen 必须 >= 1")
	}
	if cfg.HeaderLiteral < 0 || cfg.HeaderRepeat < 0 || cfg.HeaderRef < 0 {
		return errors.New("imgcodec: 头成本不能为负")
	}
	return nil
}

// encodeRow 对单行做 DP，返回该行的最短指令流。
// 引用不能跨行，因此整图最优等于各行最优之和。
func encodeRow(row, prev []byte, cfg Config) []byte {
	n := len(row)
	const inf = int(^uint(0) >> 2)
	dp := make([]int, n+1)      // dp[i] = 编码前 i 个像素的最小成本
	tag := make([]InstTag, n+1) // 到达 i 的最后一条指令类型
	ln := make([]int, n+1)      // 该指令的长度
	for i := 1; i <= n; i++ {
		dp[i] = inf
	}
	// matchEnd[i]：row 与 prev 从 i 起向后匹配的长度（用于引用转移）。
	match := make([]int, n+1)
	if prev != nil {
		for i := n - 1; i >= 0; i-- {
			if row[i] == prev[i] {
				match[i] = match[i+1] + 1
			}
		}
	}
	consider := func(end, l, cost int, t InstTag) {
		if cost < dp[end] {
			dp[end] = cost
			tag[end] = t
			ln[end] = l
		}
	}
	for i := 0; i < n; i++ {
		if dp[i] == inf {
			continue
		}
		// 字面段：长度 1..n-i，成本 = 头 + 长度。
		maxLit := n - i
		if maxLit > maxInstLen {
			maxLit = maxInstLen
		}
		for l := 1; l <= maxLit; l++ {
			consider(i+l, l, dp[i]+cfg.HeaderLiteral+l, TagLiteral)
		}
		// 重复段：从 i 起相同字节的最长连续长度内任选。
		maxRun := 1
		for i+maxRun < n && row[i+maxRun] == row[i] {
			maxRun++
		}
		if maxRun > maxInstLen {
			maxRun = maxInstLen
		}
		for l := 1; l <= maxRun; l++ {
			consider(i+l, l, dp[i]+cfg.HeaderRepeat+1, TagRepeat)
		}
		// 上一行引用：长度 <= MaxRefLen 且逐字节相等。
		if prev != nil {
			maxRef := match[i]
			if maxRef > cfg.MaxRefLen {
				maxRef = cfg.MaxRefLen
			}
			if maxRef > maxInstLen {
				maxRef = maxInstLen
			}
			for l := 1; l <= maxRef; l++ {
				consider(i+l, l, dp[i]+cfg.HeaderRef, TagRef)
			}
		}
	}
	// 回溯生成指令流。
	var insts []Inst
	for i := n; i > 0; {
		l := ln[i]
		insts = append(insts, Inst{Tag: tag[i], Len: l})
		i -= l
	}
	var out []byte
	start := 0 // 当前指令在 row 中的起点
	for k := len(insts) - 1; k >= 0; k-- {
		inst := insts[k]
		out = append(out, byte(inst.Tag), 0, 0)
		binary.LittleEndian.PutUint16(out[len(out)-2:], uint16(inst.Len))
		switch inst.Tag {
		case TagLiteral:
			out = append(out, row[start:start+inst.Len]...)
		case TagRepeat:
			out = append(out, row[start])
		}
		start += inst.Len
	}
	return out
}

// Decode 严格解码指令流。任何非法指令（含非法引用）都会返回
// 定位到指令序号的 *DecodeError，而不会返回损坏的像素。
func Decode(data []byte, w, h int, cfg Config) ([]byte, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("imgcodec: 非法尺寸 %dx%d", w, h)
	}
	if cfg.MaxRefLen < 1 {
		return nil, errors.New("imgcodec: MaxRefLen 必须 >= 1")
	}
	total := w * h
	out := make([]byte, 0, total)
	pos := 0     // 输入流偏移
	instIdx := 0 // 当前指令序号
	fail := func(format string, args ...interface{}) error {
		return &DecodeError{InstIndex: instIdx, Offset: pos, Reason: fmt.Sprintf(format, args...)}
	}
	for len(out) < total {
		if pos+instHeaderSize > len(data) {
			return nil, fail("指令头不完整：剩余 %d 字节，需要 %d", len(data)-pos, instHeaderSize)
		}
		tag := InstTag(data[pos])
		n := int(binary.LittleEndian.Uint16(data[pos+1 : pos+3]))
		if n < 1 {
			return nil, fail("指令长度 %d 非法（必须 >= 1）", n)
		}
		if n > maxInstLen {
			return nil, fail("指令长度 %d 超出上限 %d", n, maxInstLen)
		}
		col := len(out) % w
		if col+n > w {
			return nil, fail("指令长度 %d 跨行（当前列 %d，行宽 %d）", n, col, w)
		}
		body := pos + instHeaderSize
		switch tag {
		case TagLiteral:
			if body+n > len(data) {
				return nil, fail("字面段负载不完整：需要 %d 字节，剩余 %d", n, len(data)-body)
			}
			out = append(out, data[body:body+n]...)
			pos = body + n
		case TagRepeat:
			if body+1 > len(data) {
				return nil, fail("重复段负载不完整：需要 1 字节")
			}
			b := data[body]
			for i := 0; i < n; i++ {
				out = append(out, b)
			}
			pos = body + 1
		case TagRef:
			row := len(out) / w
			if row == 0 {
				return nil, fail("首行不能使用上一行引用")
			}
			if n > cfg.MaxRefLen {
				return nil, fail("引用长度 %d 超过上限 %d", n, cfg.MaxRefLen)
			}
			src := len(out) - w
			out = append(out, out[src:src+n]...)
			pos = body
		default:
			return nil, fail("未知指令类型 %d", byte(tag))
		}
		instIdx++
	}
	if pos != len(data) {
		return nil, &DecodeError{InstIndex: instIdx, Offset: pos,
			Reason: fmt.Sprintf("图像解码完成后仍有 %d 字节多余数据", len(data)-pos)}
	}
	return out, nil
}

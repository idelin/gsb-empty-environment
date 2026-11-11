package polyglot

import (
	"bytes"
	"fmt"
	"strconv"
)

// pdfMaxHeaderScan 为 PDF 头允许出现的最大前置垃圾字节数（PDF 规范上限 1024）。
const pdfMaxHeaderScan = 1024

// pdfTailWindow 为定位 startxref/%%EOF 时扫描的尾部窗口大小。
const pdfTailWindow = 64 * 1024

var (
	pdfHeaderMarker = []byte("%PDF-")
	pdfEOFMarker    = []byte("%%EOF")
	pdfXRefMarker   = []byte("startxref")
	pdfTrailerHints = [][]byte{[]byte("trailer"), []byte("/XRef")}
)

type pdfParser struct{}

// parse 按 PDF 自身规则定位结构：
// 在文件前 1024 字节内寻找 %PDF- 头（允许前置垃圾），
// 在尾部窗口内定位最后一个 %%EOF 与其前面的 startxref，
// 再按 startxref 给出的偏移核对交叉引用表或交叉引用流。
func (pdfParser) parse(r *boundedReader) (string, error) {
	size := r.Size()
	if size < int64(len(pdfHeaderMarker)+len(pdfEOFMarker)) {
		return "", fmt.Errorf("文件过短，无法同时容纳 PDF 头与 %%EOF")
	}

	headEnd := min64(size, pdfMaxHeaderScan)
	head, err := r.bytesAt(0, int(headEnd))
	if err != nil {
		return "", err
	}
	headerOff := bytes.Index(head, pdfHeaderMarker)
	if headerOff < 0 {
		return "", fmt.Errorf("前 %d 字节内未找到 %%PDF- 头", headEnd)
	}
	// 版本形如 %PDF-1.7，头标记后至少需要 3 个版本字符。
	if headEnd-int64(headerOff) < int64(len(pdfHeaderMarker))+3 {
		return "", fmt.Errorf("偏移 %d 处 PDF 头版本号不完整", headerOff)
	}

	windowStart := int64(0)
	if size > pdfTailWindow {
		windowStart = size - pdfTailWindow
	}
	eofRel, ok := r.lastIndex(windowStart, size, pdfEOFMarker)
	if !ok {
		return "", fmt.Errorf("尾部 %d 字节内未找到 %%%%EOF", min64(size, pdfTailWindow))
	}
	eofOff := eofRel

	xrefRel, ok := r.lastIndex(windowStart, eofOff, pdfXRefMarker)
	if !ok {
		return "", fmt.Errorf("%%%%EOF 之前未找到 startxref")
	}
	xrefKeywordOff := xrefRel
	numberWindow, err := r.bytesAt(xrefKeywordOff, int(min64(size-xrefKeywordOff, 64)))
	if err != nil {
		return "", err
	}
	xrefOffset, err := parseStartXRef(numberWindow)
	if err != nil {
		return "", err
	}
	if xrefOffset < 0 || xrefOffset >= size {
		return "", fmt.Errorf("startxref 偏移 %d 超出文件范围", xrefOffset)
	}

	// 在目标偏移附近核对交叉引用表（xref）或交叉引用流（/XRef）。
	probeLen := min64(size-xrefOffset, 1024)
	probe, err := r.bytesAt(xrefOffset, int(probeLen))
	if err != nil {
		return "", err
	}
	probe = bytes.TrimLeft(probe, "\r\n\x00\t ")
	switch {
	case bytes.HasPrefix(probe, []byte("xref")):
		// 经典交叉引用表必须配合 trailer 字典。
		tail, err := r.bytesAt(windowStart, int(eofOff-windowStart))
		if err != nil {
			return "", err
		}
		if bytes.Index(tail, []byte("trailer")) < 0 {
			return "", fmt.Errorf("xref 表之后缺少 trailer 字典")
		}
	case bytes.Contains(probe, []byte("/XRef")):
		// 交叉引用流，/XRef 已在流对象字典中出现。
	default:
		return "", fmt.Errorf("startxref 偏移 %d 处既不是 xref 表也不是 /XRef 流", xrefOffset)
	}

	return fmt.Sprintf("%%PDF- 头位于偏移 %d，startxref 指向 %d，%%%%EOF 位于 %d",
		headerOff, xrefOffset, eofOff), nil
}

// parseStartXRef 从 “startxref<空白>数字<空白>%%EOF ...” 片段中解析偏移。
func parseStartXRef(window []byte) (int64, error) {
	rest := window[len(pdfXRefMarker):]
	rest = bytes.TrimLeft(rest, "\r\n\x00\t ")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, fmt.Errorf("startxref 后缺少数字偏移")
	}
	value, err := strconv.ParseInt(string(rest[:end]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("startxref 偏移非法: %w", err)
	}
	return value, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

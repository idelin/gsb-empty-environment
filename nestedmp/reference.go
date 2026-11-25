package nestedmp

import (
	"bytes"
	"crypto/sha256"
	"fmt"
)

// RefPart 是独立全量参考解析器输出的单个部件。
type RefPart struct {
	Level     int
	Type      string
	Name      string
	Boundary  string
	Header    []byte // 原始头块（含结尾空行）
	Body      []byte // 去除自身定界符后的原始正文
	Digest    [sha256.Size]byte
	Offset    int64 // 头块首字节的全局偏移
	BodyStart int64
	BodyEnd   int64
	EndOffset int64
	Children  []RefPart
}

// ReferenceParse 用与流式匹配器完全不同的一次性算法解析整条消息：
// 基于切片与 bytes 索引逐段定位定界行，不共享任何匹配器状态机代码。
// 它执行与流式解析相同的全部校验，便于差分测试交叉验证。
func ReferenceParse(input []byte, rootBoundary string) ([]RefPart, error) {
	if !validBoundary(rootBoundary) {
		return nil, fmt.Errorf("%w: root boundary %q", ErrBoundary, rootBoundary)
	}
	st := &refState{
		active: map[string]bool{rootBoundary: true},
		names:  map[string]bool{},
	}
	parts, _, err := st.refLevel(input, 0, 0, rootBoundary, true)
	if err != nil {
		return nil, err
	}
	return parts, nil
}

type refState struct {
	active map[string]bool
	names  map[string]bool
}

type refDelim struct {
	open  bool
	start int // 含前导 CRLF；流首定界行从 0 开始
	end   int // 定界行之后的首字节（排他）
}

// refFind 在 data 中寻找下一条属于 boundary 的定界行。
// 只在行首（data 起点或 CRLF 之后）匹配，且边界名必须完整一致、
// 严格区分大小写，因此父子互为前缀时不会误判。
func refFind(data []byte, boundary string, atStreamStart bool) (refDelim, bool) {
	openTag := []byte("--" + boundary + "\r\n")
	closeTag := []byte("--" + boundary + "--\r\n")
	pos := 0
	if atStreamStart {
		if d, ok := matchAt(data, 0, openTag, closeTag); ok {
			return d, true
		}
	}
	// 定界行必须位于某个 CRLF 之后，枚举所有行首位置逐一比对。
	for {
		idx := bytes.Index(data[pos:], []byte("\r\n"))
		if idx < 0 {
			return refDelim{}, false
		}
		lineStart := pos + idx + 2
		if lineStart >= len(data) {
			return refDelim{}, false
		}
		if d, ok := matchAt(data, lineStart, openTag, closeTag); ok {
			// 紧邻定界行的 CRLF 属于定界行，start 回退到该 CRLF 起点，
			// 与流式匹配器上报的 span（含前导 CRLF）保持同一语义。
			d.start = lineStart - 2
			return d, true
		}
		pos = lineStart
	}
}

func matchAt(data []byte, pos int, openTag, closeTag []byte) (refDelim, bool) {
	if bytes.HasPrefix(data[pos:], closeTag) {
		return refDelim{open: false, start: pos, end: pos + len(closeTag)}, true
	}
	if bytes.HasPrefix(data[pos:], openTag) {
		return refDelim{open: true, start: pos, end: pos + len(openTag)}, true
	}
	return refDelim{}, false
}

// refLevel 解析一层。data 是该层容器的原始正文（根层为整条输入）。
func (st *refState) refLevel(data []byte, level int, base int64, boundary string, isRoot bool) ([]RefPart, int, error) {
	first, ok := refFind(data, boundary, true)
	if !ok {
		return nil, 0, fmt.Errorf("%w: no opening delimiter for boundary %q", ErrEOF, boundary)
	}
	if first.start != 0 {
		return nil, 0, fmt.Errorf("%w: %d byte(s) before first delimiter", ErrPreamble, first.start)
	}
	if !first.open {
		return nil, 0, fmt.Errorf("%w: boundary %q closed before any part", ErrSyntax, boundary)
	}
	var parts []RefPart
	cursor := first.end
	for {
		d, ok := refFind(data[cursor:], boundary, false)
		if !ok {
			return nil, 0, fmt.Errorf("%w: unterminated boundary %q", ErrEOF, boundary)
		}
		d.start += cursor
		d.end += cursor
		headerEndAbs := d.start // 前导 CRLF 属于定界行，正文止于 d.start
		headerBlock := data[first.end:d.start]
		blank := bytes.Index(headerBlock, []byte("\r\n\r\n"))
		if blank < 0 {
			return nil, 0, fmt.Errorf("%w: header block has no blank line", ErrHeader)
		}
		h, headerLen, err := parseHeaderBlock(headerBlock[:blank+4])
		if err != nil {
			return nil, 0, err
		}
		if st.names[h.name] {
			return nil, 0, fmt.Errorf("%w: duplicate part name %q", ErrHeader, h.name)
		}
		if h.typ == TypeContainer && st.active[h.child] {
			return nil, 0, fmt.Errorf("%w: boundary %q already active on stack", ErrBoundary, h.child)
		}
		st.names[h.name] = true
		if headerLen != blank+4 {
			return nil, 0, fmt.Errorf("%w: inconsistent header length", ErrHeader)
		}
		headerStart := base + int64(first.end)
		bodyStart := headerStart + int64(headerLen)
		body := headerBlock[headerLen:]
		rp := RefPart{
			Level:     level,
			Type:      h.typ,
			Name:      h.name,
			Boundary:  boundary,
			Header:    append([]byte(nil), headerBlock[:headerLen]...),
			Body:      append([]byte(nil), body...),
			Offset:    headerStart,
			BodyStart: bodyStart,
			BodyEnd:   base + int64(headerEndAbs),
			EndOffset: base + int64(d.end),
		}
		rp.Digest = sha256.Sum256(body)
		if rp.Digest != h.digest {
			return nil, 0, fmt.Errorf("%w: part %q", ErrDigest, h.name)
		}
		if h.typ == TypeContainer {
			childBoundary := h.child
			if st.active[childBoundary] {
				return nil, 0, fmt.Errorf("%w: boundary %q already active on stack", ErrBoundary, childBoundary)
			}
			st.active[childBoundary] = true
			kids, childConsumed, err := st.refLevel(body, level+1, bodyStart, childBoundary, false)
			delete(st.active, childBoundary)
			if err != nil {
				return nil, 0, err
			}
			// 容器正文（含完整子 close 行）结束于子层消费位置，
			// 而不是外层下一定界行；BodyEnd/EndOffset 据此对齐流式解析。
			rp.BodyEnd = bodyStart + int64(childConsumed)
			rp.EndOffset = rp.BodyEnd
			rp.Children = kids
			parts = append(parts, rp)
		} else {
			parts = append(parts, rp)
		}
		if !d.open {
			if isRoot && d.end != len(data) {
				return nil, 0, fmt.Errorf("%w: %d trailing byte(s)", ErrTrailing, len(data)-d.end)
			}
			return parts, d.end, nil
		}
		cursor = d.end
		first = d
	}
}

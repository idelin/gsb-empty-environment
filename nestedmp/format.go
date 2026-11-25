package nestedmp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// 部件类型。
const (
	// TypeAttachment 是普通附件部件，其正文交给宿主事务暂存。
	TypeAttachment = "attachment"
	// TypeContainer 是嵌套容器部件，必须携带子边界，正文按内层格式解析。
	TypeContainer = "container"
)

// maxBoundaryLen 是边界名的最大长度（题内规定 1..70）。
const maxBoundaryLen = 70

// maxHeaderSize 是单个部件头块（含结尾空行）的固定上限。
const maxHeaderSize = 4096

// header 是一个部件头块解析后的结构化结果。
type header struct {
	typ      string
	name     string
	digest   [sha256.Size]byte
	child    string
	hasChild bool
}

// validBoundary 报告 name 是否为 1..70 个 ASCII 字母数字。
func validBoundary(name string) bool {
	if len(name) == 0 || len(name) > maxBoundaryLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
}

// parseHeaderBlock 解析从开放分隔行之后到空行（含 CRLF CRLF）的头块。
// 返回解析结果和头块总长度（调用方据此推进字节位置）。
// 头字段按题内要求严格处理：必需字段缺失、重复或非法均报错；未知字段忽略。
func parseHeaderBlock(block []byte) (header, int, error) {
	end := bytes.Index(block, []byte("\r\n\r\n"))
	if end < 0 {
		return header{}, 0, fmt.Errorf("%w: header block has no blank line", ErrHeader)
	}
	total := end + 4
	var h header
	seen := make(map[string]bool)
	lines := bytes.Split(block[:end], []byte("\r\n"))
	for _, line := range lines {
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			return header{}, 0, fmt.Errorf("%w: malformed header field", ErrHeader)
		}
		key := string(bytes.TrimRight(line[:colon], " \t"))
		val := string(bytes.TrimLeft(line[colon+1:], " \t"))
		if !tokenValid(key) {
			return header{}, 0, fmt.Errorf("%w: malformed header field name", ErrHeader)
		}
		// 字段名大小写不敏感，按小写形式比较；连字符等其它字符原样保留，
		// 因此 "SHA-256" 归一化为 "sha-256"。
		lkey := lowerASCII(key)
		if seen[lkey] {
			return header{}, 0, fmt.Errorf("%w: duplicate header %q", ErrHeader, key)
		}
		seen[lkey] = true
		switch lkey {
		case "type":
			if val != TypeAttachment && val != TypeContainer {
				return header{}, 0, fmt.Errorf("%w: unknown type %q", ErrHeader, val)
			}
			h.typ = val
		case "name":
			if val == "" {
				return header{}, 0, fmt.Errorf("%w: empty part name", ErrHeader)
			}
			h.name = val
		case "sha-256":
			raw, err := hex.DecodeString(val)
			if err != nil || len(raw) != sha256.Size {
				return header{}, 0, fmt.Errorf("%w: sha256 must be 64 hex digits", ErrHeader)
			}
			copy(h.digest[:], raw)
		case "boundary":
			if !validBoundary(val) {
				return header{}, 0, fmt.Errorf("%w: invalid child boundary %q", ErrBoundary, val)
			}
			h.child = val
			h.hasChild = true
		}
	}
	if h.typ == "" {
		return header{}, 0, fmt.Errorf("%w: missing Type", ErrHeader)
	}
	if h.name == "" {
		return header{}, 0, fmt.Errorf("%w: missing Name", ErrHeader)
	}
	if !seen["sha-256"] {
		return header{}, 0, fmt.Errorf("%w: missing SHA-256", ErrHeader)
	}
	if h.typ == TypeContainer && !h.hasChild {
		return header{}, 0, fmt.Errorf("%w: container must declare Boundary", ErrHeader)
	}
	if h.typ == TypeAttachment && h.hasChild {
		return header{}, 0, fmt.Errorf("%w: attachment must not declare Boundary", ErrHeader)
	}
	return h, total, nil
}

func tokenValid(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch <= ' ' || ch == ':' {
			return false
		}
	}
	return true
}

func lowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		b[i] = ch
	}
	return string(b)
}

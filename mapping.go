// Package netsec 在 MIME 叶节点的原始字节与 Content-Transfer-Encoding
// 解码后的正文之间建立逐字节的可溯源映射，供邮件预览宿主判断每个
// 可见字符是否被签名（已认证的原始区间）完整覆盖。
//
// 原始字节长度不能直接映射到解码后的文本：quoted-printable 的软换行
// 与转义、Base64 的 4→3 量子及填充都会改变字节数量与对应关系。
// 本包为每个解码字节记录它所依赖的全部原始字符，仅当所有依赖位置
// 都已认证时该字节才标记为可信；跨编码单元的半覆盖一律不可信，
// 保证界面高亮绝不会夸大签名实际覆盖的范围。
package netsec

import "fmt"

// Encoding 标识 MIME Content-Transfer-Encoding。
type Encoding int

const (
	// EncodingIdentity 对应 7bit / 8bit / binary，原始字节即正文，
	// 每个解码字节依赖同偏移量的单个原始字符。
	EncodingIdentity Encoding = iota
	// EncodingQuotedPrintable 对应 quoted-printable（RFC 2045）。
	EncodingQuotedPrintable
	// EncodingBase64 对应 base64（RFC 2045，标准字母表）。
	EncodingBase64
)

// IssueKind 描述输入流中遇到的非致命格式问题。
type IssueKind int

const (
	// IssueMalformed：出现非法转义、非法 Base64 字符或错误填充等损坏。
	IssueMalformed IssueKind = iota
	// IssueTruncated：编码单元在输入结尾被截断（半个量子或悬空的 "="）。
	IssueTruncated
)

// Issue 记录原始字节流中的一处格式问题，Raw 为相关原始区间。
// 存在问题时相关解码字节一律按不可信处理。
type Issue struct {
	Kind IssueKind
	Raw  Range
}

// ByteTrust 描述一个解码字节的溯源与可信状态。
// Decoded 为该字节在解码后正文中的偏移量（切片下标）。
type ByteTrust struct {
	Decoded int64
	// Deps 是产生该字节所依赖的全部原始字符位置，按出现顺序排列。
	// 只有这些位置全部被认证时 Trusted 才为 true。
	Deps []int64
	// Trusted 为 true 当且仅当 Deps 中的每个原始位置都已认证。
	Trusted bool
}

// Mapping 是一个 MIME 叶节点的完整映射结果。
type Mapping struct {
	// Decoded 是解码后的正文字节，供宿主直接渲染。
	Decoded []byte
	// Bytes 与 Decoded 逐字节对应，下标即解码偏移量。
	Bytes []ByteTrust
	// Issues 是解码过程中发现的格式问题（截断、损坏等）。
	Issues []Issue
}

// MapPart 解码 raw 的同时建立逐字节溯源映射。
//
// encoding 为叶节点的 Content-Transfer-Encoding；authenticated 为
// 签名实际覆盖的原始字节半开区间集合，可任意排序、可相邻或重叠，
// 越界或反向的区间会返回错误。解码本身不因内容损坏而失败：
// 损坏处记入 Mapping.Issues，相关字节标记为不可信。
func MapPart(raw []byte, encoding Encoding, authenticated []Range) (*Mapping, error) {
	cov, err := newCoverage(authenticated, int64(len(raw)))
	if err != nil {
		return nil, err
	}

	m := &Mapping{}
	s := &scanner{m: m, cov: cov}
	switch encoding {
	case EncodingIdentity:
		s.scanIdentity(raw)
	case EncodingQuotedPrintable:
		s.scanQP(raw)
	case EncodingBase64:
		s.scanBase64(raw)
	default:
		return nil, fmt.Errorf("netsec: unsupported encoding %d", encoding)
	}
	return m, nil
}

// scanner 累积解码输出与溯源信息。depPool 复用依赖切片的底层数组，
// 每个 ByteTrust.Deps 都指向独立的子切片。
type scanner struct {
	m       *Mapping
	cov     coverage
	depPool []int64
}

// emit 追加一个解码字节，并依据其全部依赖位置判定可信性。
func (s *scanner) emit(decoded byte, deps []int64) {
	trusted := true
	for _, pos := range deps {
		if !s.cov.covered(pos) {
			trusted = false
			break
		}
	}
	s.m.Decoded = append(s.m.Decoded, decoded)
	s.m.Bytes = append(s.m.Bytes, ByteTrust{
		Decoded: int64(len(s.m.Decoded)) - 1,
		Deps:    deps,
		Trusted: trusted,
	})
}

// deps 将一组原始位置切出为独立的依赖切片（共享 depPool 底层数组）。
func (s *scanner) deps(positions ...int64) []int64 {
	start := len(s.depPool)
	s.depPool = append(s.depPool, positions...)
	return s.depPool[start:]
}

func (s *scanner) issue(kind IssueKind, start, end int64) {
	s.m.Issues = append(s.m.Issues, Issue{Kind: kind, Raw: Range{Start: start, End: end}})
}

func (s *scanner) scanIdentity(raw []byte) {
	for i, b := range raw {
		pos := int64(i)
		s.emit(b, s.deps(pos))
	}
}

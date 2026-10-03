// Package segment 实现紧凑只读倒排索引段的构建与检索。
//
// 段布局（全部使用 varint 编码）：
//
//	[magic "ISG1"][termCount][dictLen][dictionary][postings]
//
// 词典：按块前缀压缩（front coding），块首词完整存储并建有块索引，
// 查找时先对块索引二分，再在块内顺序扫描。
//
// 倒排列表：文档 ID 差值（delta）+ 词频 varint 编码；
// 每 skipInterval 条 posting 记录一个跳跃指针（前一块末位 docID 与字节偏移），
// 供多词交集求值时跳过无用段。
package segment

import (
	"encoding/binary"
	"sort"
	"strings"
)

const (
	magic        = "ISG1"
	dictBlockLen = 16 // 每个词典块包含的词数
	skipInterval = 8  // 每多少条 posting 设置一个跳跃指针
)

// Builder 收集文档并逐步构建只读段。
type Builder struct {
	// term -> docID -> tf
	postings map[string]map[uint32]uint32
}

// postingRef 记录某个词的倒排列表在倒排区中的位置。
type postingRef struct{ off, length int }

func NewBuilder() *Builder {
	return &Builder{postings: make(map[string]map[uint32]uint32)}
}

// Add 将一篇文档加入索引。文本按非字母数字切词并转小写，统计词频。
func (b *Builder) Add(docID uint32, text string) {
	counts := map[string]uint32{}
	for _, tok := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		counts[tok]++
	}
	for term, tf := range counts {
		m := b.postings[term]
		if m == nil {
			m = make(map[uint32]uint32)
			b.postings[term] = m
		}
		m[docID] += tf
	}
}

// Build 序列化为只读段字节流。
func (b *Builder) Build() []byte {
	terms := make([]string, 0, len(b.postings))
	for t := range b.postings {
		terms = append(terms, t)
	}
	sort.Strings(terms)

	// 先编码倒排区，记录每个词的偏移。
	postingsBuf := []byte{}
	refs := make(map[string]postingRef, len(terms))
	for _, t := range terms {
		docs := b.postings[t]
		ids := make([]uint32, 0, len(docs))
		for id := range docs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		tfs := make([]uint32, len(ids))
		for i, id := range ids {
			tfs[i] = docs[id]
		}
		off := len(postingsBuf)
		postingsBuf = append(postingsBuf, encodePostings(ids, tfs)...)
		refs[t] = postingRef{off, len(postingsBuf) - off}
	}

	dictBuf := encodeDict(terms, refs)

	out := []byte(magic)
	out = binary.AppendUvarint(out, uint64(len(terms)))
	out = binary.AppendUvarint(out, uint64(len(dictBuf)))
	out = append(out, dictBuf...)
	out = append(out, postingsBuf...)
	return out
}

// encodePostings 编码单个词的倒排列表：
//
//	[docCount][skipCount][skip 条目...][delta/tf 数据]
//
// 第 k 个跳跃指针对应数据区第 (k+1)*skipInterval 条 posting，
// 记录其前一条 posting 的绝对 docID（解码基准）与该条的字节偏移。
func encodePostings(ids, tfs []uint32) []byte {
	data := []byte{}
	type skip struct {
		base uint32
		off  int
	}
	var skips []skip
	prev := uint32(0)
	for i, id := range ids {
		if i > 0 && i%skipInterval == 0 {
			skips = append(skips, skip{prev, len(data)})
		}
		data = binary.AppendUvarint(data, uint64(id-prev))
		data = binary.AppendUvarint(data, uint64(tfs[i]))
		prev = id
	}

	buf := binary.AppendUvarint(nil, uint64(len(ids)))
	buf = binary.AppendUvarint(buf, uint64(len(skips)))
	for _, s := range skips {
		buf = binary.AppendUvarint(buf, uint64(s.base))
		buf = binary.AppendUvarint(buf, uint64(s.off))
	}
	return append(buf, data...)
}

// encodeDict 前缀压缩词典：
//
//	[blockCount][块索引: (完整词, 块偏移)...][块体]
//
// 块体内每个词存 (与前词的公共前缀长度, 后缀长度, 后缀, postings偏移, postings长度)。
func encodeDict(terms []string, refs map[string]postingRef) []byte {
	body := []byte{}
	var heads []string
	var headOffs []int
	for i := 0; i < len(terms); i += dictBlockLen {
		heads = append(heads, terms[i])
		headOffs = append(headOffs, len(body))
		prev := ""
		for j := i; j < i+dictBlockLen && j < len(terms); j++ {
			t := terms[j]
			p := commonPrefix(prev, t)
			body = binary.AppendUvarint(body, uint64(p))
			body = binary.AppendUvarint(body, uint64(len(t)-p))
			body = append(body, t[p:]...)
			r := refs[t]
			body = binary.AppendUvarint(body, uint64(r.off))
			body = binary.AppendUvarint(body, uint64(r.length))
			prev = t
		}
	}

	buf := binary.AppendUvarint(nil, uint64(len(heads)))
	for i, h := range heads {
		buf = binary.AppendUvarint(buf, uint64(len(h)))
		buf = append(buf, h...)
		buf = binary.AppendUvarint(buf, uint64(headOffs[i]))
	}
	return append(buf, body...)
}

func commonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

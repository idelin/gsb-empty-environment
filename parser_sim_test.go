package taskpkg

import "bytes"

// simSide 配置一个参考解析器，用于在不依赖外部宿主的情况下复现网关/后端分歧。
type simSide struct {
	// lenient 为 true 时同时接受 CRLF 与裸 LF 作为部件分隔；false 时只接受 CRLF。
	lenient bool
	// policy 决定同名字段的重复项取舍。
	policy DuplicatePolicy
	// reorder 为 true 时按字段名字典序重排 Fields，用于验证排序差异不告警。
	reorder bool
	// auditName 非空时只有该名字的字段进入审核/依赖区间；为空则审核全部入口。
	auditName string
}

const simBoundary = "--BOUND"

var simCRLF = []byte("\r\n")
var simLF = []byte("\n")

// newSimParser 返回一个解析简化 multipart 体的 Parser 回调。
//
// 每个部件形如：
//
//	--BOUND\r\n
//	Content-Disposition: form-data; name="f"; filename="x"\r\n
//	\r\n
//	<content>
//	\r\n--BOUND
//
// 严格模式只承认 CRLF 分隔，宽松模式也承认裸 LF。所有区间均相对传入 body。
func newSimParser(cfg simSide) Parser {
	return func(body []byte) (*Report, error) {
		var parts []simPart
		marker := []byte(simBoundary)
		searchFrom := 0
		for {
			rel := bytes.Index(body[searchFrom:], marker)
			if rel < 0 {
				break
			}
			idx := searchFrom + rel
			lineEnd := idx + len(marker)
			// "--" 闭合标记结束 multipart 体。
			if bytes.HasPrefix(body[lineEnd:], []byte("--")) {
				break
			}
			// 分隔标记行必须以 CRLF（宽松模式下也可为裸 LF）结束。
			sepLen := 0
			if bytes.HasPrefix(body[lineEnd:], simCRLF) {
				sepLen = 2
			} else if cfg.lenient && lineEnd < len(body) && body[lineEnd] == '\n' {
				sepLen = 1
			} else {
				searchFrom = lineEnd
				continue
			}
			headerStart := lineEnd + sepLen

			termStart, termLen := headerTerminator(body, headerStart, cfg.lenient)
			if termStart < 0 {
				searchFrom = lineEnd
				continue
			}
			contentStart := termStart + termLen
			next := nextBoundary(body, contentStart, marker, cfg.lenient)
			if next < 0 {
				break
			}
			contentEnd := next
			if contentEnd >= 2 && bytes.Equal(body[contentEnd-2:contentEnd], simCRLF) {
				contentEnd -= 2
			} else if cfg.lenient && contentEnd >= 1 && body[contentEnd-1] == '\n' {
				contentEnd--
			}

			name := parseFieldName(body[headerStart:termStart])
			parts = append(parts, simPart{
				name:       name,
				headerRaw:  Interval{Start: headerStart, End: termStart},
				contentRaw: Interval{Start: contentStart, End: contentEnd},
				wholeRaw:   Interval{Start: idx, End: next},
				ord:        len(parts),
			})
			// 推进到下一边界标记起点，由下一轮消费该标记本身。
			searchFrom = next
		}

		report := &Report{}
		byName := map[string][]int{}
		for i, p := range parts {
			report.Fields = append(report.Fields, FieldOccurrence{
				Name:        p.name,
				Raw:         p.wholeRaw,
				KeptOrdinal: p.ord,
				Kept:        true,
			})
			byName[p.name] = append(byName[p.name], i)
		}
		if cfg.reorder {
			sortFieldsByName(report.Fields)
		}

		for name, idxs := range byName {
			chosen := chooseSimPart(parts, idxs, cfg.policy)
			for _, ord := range idxs {
				for fi := range report.Fields {
					if report.Fields[fi].Name == name && report.Fields[fi].KeptOrdinal == parts[ord].ord {
						report.Fields[fi].Kept = cfg.policy == PolicyAll || parts[ord].ord == chosen.ord
					}
				}
			}
			deps := []Interval{chosen.contentRaw}
			if cfg.policy == PolicyAll {
				deps = nil
				for _, ord := range idxs {
					deps = append(deps, parts[ord].contentRaw)
				}
			}
			entry := Entry{
				Name:     name,
				Policy:   cfg.policy,
				Chosen:   FieldOccurrence{Name: name, Raw: chosen.wholeRaw, KeptOrdinal: chosen.ord, Kept: true},
				DependOn: deps,
			}
			report.Entries = append(report.Entries, entry)
			if cfg.auditName == "" || cfg.auditName == name {
				report.Audited = append(report.Audited, deps...)
			}
		}
		return report, nil
	}
}

type simPart struct {
	name       string
	headerRaw  Interval
	contentRaw Interval
	wholeRaw   Interval
	ord        int
}

func headerTerminator(body []byte, from int, lenient bool) (int, int) {
	crlf := bytes.Index(body[from:], []byte("\r\n\r\n"))
	if crlf >= 0 {
		return from + crlf, 4
	}
	if lenient {
		lf := bytes.Index(body[from:], []byte("\n\n"))
		if lf >= 0 {
			return from + lf, 2
		}
	}
	return -1, 0
}

func nextBoundary(body []byte, from int, marker []byte, lenient bool) int {
	search := from
	for {
		rel := bytes.Index(body[search:], marker)
		if rel < 0 {
			return -1
		}
		i := search + rel
		if i >= 2 && bytes.Equal(body[i-2:i], simCRLF) {
			return i
		}
		if lenient && i >= 1 && body[i-1] == '\n' {
			return i
		}
		search = i + len(marker)
	}
}

func parseFieldName(header []byte) string {
	key := []byte("name=\"")
	idx := bytes.Index(header, key)
	if idx < 0 {
		return ""
	}
	rest := header[idx+len(key):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return string(rest[:end])
}

func chooseSimPart(parts []simPart, idxs []int, policy DuplicatePolicy) simPart {
	switch policy {
	case PolicyFirst:
		return parts[idxs[0]]
	case PolicyAll:
		return parts[idxs[0]]
	default:
		return parts[idxs[len(idxs)-1]]
	}
}

func sortFieldsByName(fields []FieldOccurrence) {
	for i := 1; i < len(fields); i++ {
		for j := i; j > 0 && fields[j].Name < fields[j-1].Name; j-- {
			fields[j], fields[j-1] = fields[j-1], fields[j]
		}
	}
}

func simEncodedPart(name, filename, content string) string {
	h := "Content-Disposition: form-data; name=\"" + name + "\""
	if filename != "" {
		h += "; filename=\"" + filename + "\""
	}
	return simBoundary + "\r\n" + h + "\r\n\r\n" + content + "\r\n"
}

func simBody(parts ...string) []byte {
	var buf bytes.Buffer
	for _, p := range parts {
		buf.WriteString(p)
	}
	buf.WriteString(simBoundary + "--\r\n")
	return buf.Bytes()
}

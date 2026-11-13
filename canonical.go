package netsec

import "bytes"

type bodyCanonicalization struct {
	data       []byte
	rawOffsets []int
}

type rawBodyLine struct {
	textStart int
	textEnd   int
	next      int
	hasBreak  bool
}

func canonicalizeBody(raw []byte, relaxed bool) bodyCanonicalization {
	lines := splitBodyLines(raw)

	keep := len(lines)
	for keep > 0 && relaxedBodyLineEmpty(lines[keep-1], raw, relaxed) {
		keep--
	}

	canon := bodyCanonicalization{
		rawOffsets: make([]int, 0, len(raw)),
	}
	for i := 0; i < keep; i++ {
		line := lines[i]
		text := raw[line.textStart:line.textEnd]
		if relaxed {
			canon.appendRelaxedLine(text, line.textStart)
		} else {
			for j := range text {
				canon.data = append(canon.data, text[j])
				canon.rawOffsets = append(canon.rawOffsets, line.textStart+j)
			}
		}

		breakOffset := line.textEnd
		if !line.hasBreak {
			breakOffset = len(raw)
		}
		canon.data = append(canon.data, '\r', '\n')
		canon.rawOffsets = append(canon.rawOffsets, breakOffset, breakOffset)
	}
	return canon
}

func (c *bodyCanonicalization) appendRelaxedLine(text []byte, rawStart int) {
	for i := 0; i < len(text); {
		if text[i] != ' ' && text[i] != '\t' {
			c.data = append(c.data, text[i])
			c.rawOffsets = append(c.rawOffsets, rawStart+i)
			i++
			continue
		}

		runStart := i
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		if i == len(text) {
			break
		}
		c.data = append(c.data, ' ')
		c.rawOffsets = append(c.rawOffsets, rawStart+runStart)
	}
}

func splitBodyLines(raw []byte) []rawBodyLine {
	var lines []rawBodyLine
	for pos := 0; pos < len(raw); {
		textEnd, next := lineBounds(raw, pos)
		lines = append(lines, rawBodyLine{
			textStart: pos,
			textEnd:   textEnd,
			next:      next,
			hasBreak:  next > textEnd,
		})
		pos = next
	}
	return lines
}

func relaxedBodyLineEmpty(line rawBodyLine, raw []byte, relaxed bool) bool {
	text := raw[line.textStart:line.textEnd]
	if len(text) == 0 {
		return true
	}
	if !relaxed {
		return false
	}
	for _, b := range text {
		if b != ' ' && b != '\t' {
			return false
		}
	}
	return true
}

func canonicalizeHeader(raw []byte, relaxed bool) []byte {
	if !relaxed {
		out := normalizeCRLF(raw)
		return append(out, '\r', '\n')
	}

	colon := bytes.IndexByte(raw, ':')
	if colon < 0 {
		return nil
	}
	name := bytes.ToLower(bytes.TrimSpace(raw[:colon]))
	value := relaxedHeaderValue(raw[colon+1:])

	out := make([]byte, 0, len(name)+len(value)+3)
	out = append(out, name...)
	out = append(out, ':')
	out = append(out, value...)
	out = append(out, '\r', '\n')
	return out
}

func relaxedHeaderValue(value []byte) []byte {
	out := make([]byte, 0, len(value))
	started := false
	spacePending := false

	for _, b := range value {
		switch b {
		case ' ', '\t', '\r', '\n':
			if started {
				spacePending = true
			}
		default:
			if spacePending {
				out = append(out, ' ')
				spacePending = false
			}
			out = append(out, b)
			started = true
		}
	}
	return out
}

func normalizeCRLF(in []byte) []byte {
	out := make([]byte, 0, len(in)+2)
	for i := 0; i < len(in); i++ {
		switch in[i] {
		case '\r':
			if i+1 < len(in) && in[i+1] == '\n' {
				i++
			}
			out = append(out, '\r', '\n')
		case '\n':
			out = append(out, '\r', '\n')
		default:
			out = append(out, in[i])
		}
	}
	return out
}

func stripBodySignatureValue(raw []byte) []byte {
	colon := bytes.IndexByte(raw, ':')
	if colon < 0 {
		return raw
	}

	out := make([]byte, 0, len(raw))
	out = append(out, raw[:colon+1]...)
	value := raw[colon+1:]
	segmentStart := 0
	for i := 0; i <= len(value); i++ {
		if i < len(value) && value[i] != ';' {
			continue
		}
		segment := value[segmentStart:i]
		eq := bytes.IndexByte(segment, '=')
		if eq >= 0 && bytes.EqualFold(bytes.TrimSpace(segment[:eq]), []byte("b")) {
			out = append(out, segment[:eq+1]...)
		} else {
			out = append(out, segment...)
		}
		if i < len(value) {
			out = append(out, ';')
		}
		segmentStart = i + 1
	}
	return out
}

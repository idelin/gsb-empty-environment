package pdfscan

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

// xrefEntry locates one indirect object. Type 1 entries point at a normal
// object; type 2 entries point at a slot inside a compressed object stream.
type xrefEntry struct {
	typ       int
	offset    int
	streamNum int
	streamIdx int
}

type xrefInfo struct {
	entries   map[int]xrefEntry
	trailers  []*Dict
	root      *Value
	encrypted bool
}

// parseXref builds the effective cross-reference map by walking the
// startxref/Prev chain from the newest revision backwards. Entries from
// newer revisions overwrite older ones, matching how a PDF viewer resolves
// objects after incremental updates.
func parseXref(data []byte) (*xrefInfo, error) {
	info := &xrefInfo{entries: map[int]xrefEntry{}}
	off, ok := lastStartXref(data)
	if !ok {
		return nil, errors.New("pdfscan: startxref not found")
	}

	seen := map[int]bool{}
	for i := 0; i < maxXrefRevisions; i++ {
		if off < 0 || off >= len(data) || seen[off] {
			break
		}
		seen[off] = true

		isStream, err := objectAt(data, off)
		if err != nil {
			return nil, err
		}
		var prev int
		var trailer *Dict
		if isStream {
			prev, trailer, err = parseXrefStream(data, off, info.entries)
		} else {
			prev, trailer, err = parseXrefTable(data, off, info.entries)
		}
		if err != nil {
			return nil, err
		}
		if trailer != nil {
			info.trailers = append(info.trailers, trailer)
		}
		if prev <= 0 {
			break
		}
		off = prev
	}

	// The newest trailer wins for /Root and /Encrypt.
	for _, tr := range info.trailers {
		if r := tr.Get("Root"); r != nil && r.IsRef() {
			info.root = r
		}
		if e := tr.Get("Encrypt"); e != nil && e.Kind != KindNull {
			info.encrypted = true
		}
	}
	if info.root == nil {
		return nil, errors.New("pdfscan: catalog root not found in trailers")
	}
	return info, nil
}

// objectAt reports whether the object at off is a cross-reference stream
// (N G obj << /Type /XRef ... >> stream) or a classic xref keyword table.
func objectAt(data []byte, off int) (bool, error) {
	p := newParser(data[off:])
	t, err := p.peek()
	if err != nil {
		return false, err
	}
	if t.kind == tokKeyword && t.text == "xref" {
		return false, nil
	}
	_, _, _, err = p.parseIndirect()
	if err != nil {
		return false, fmt.Errorf("pdfscan: invalid xref at offset %d: %w", off, err)
	}
	return true, nil
}

func lastStartXref(data []byte) (int, bool) {
	marker := []byte("startxref")
	idx := bytes.LastIndex(data, marker)
	if idx < 0 {
		return 0, false
	}
	pos := idx + len(marker)
	for pos < len(data) && isWhite(data[pos]) {
		pos++
	}
	end := pos
	for end < len(data) && data[end] >= '0' && data[end] <= '9' {
		end++
	}
	if end == pos {
		return 0, false
	}
	n, err := strconv.Atoi(string(data[pos:end]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseXrefTable parses a classic keyword-based xref section at off.
func parseXrefTable(data []byte, off int, entries map[int]xrefEntry) (int, *Dict, error) {
	pos := off
	if !bytes.HasPrefix(data[pos:], []byte("xref")) {
		return 0, nil, errors.New("pdfscan: expected xref keyword")
	}
	pos += 4

	for {
		pos = skipWS(data, pos)
		if pos >= len(data) {
			return 0, nil, errors.New("pdfscan: truncated xref table")
		}
		if bytes.HasPrefix(data[pos:], []byte("trailer")) {
			pos += len("trailer")
			break
		}
		// Incremental updates may emit several independent subsections,
		// each optionally re-introducing the xref keyword.
		if bytes.HasPrefix(data[pos:], []byte("xref")) {
			pos += len("xref")
			pos = skipWS(data, pos)
		}
		// Subsection header: "first count"
		lineEnd := pos
		for lineEnd < len(data) && data[lineEnd] != '\r' && data[lineEnd] != '\n' {
			lineEnd++
		}
		headerFields := bytes.Fields(data[pos:lineEnd])
		if len(headerFields) != 2 {
			return 0, nil, errors.New("pdfscan: malformed xref subsection header")
		}
		firstObj, e1 := strconv.Atoi(string(headerFields[0]))
		count, e2 := strconv.Atoi(string(headerFields[1]))
		if e1 != nil || e2 != nil {
			return 0, nil, errors.New("pdfscan: malformed xref subsection header")
		}
		pos = lineEnd
		for pos < len(data) && (data[pos] == '\r' || data[pos] == '\n') {
			pos++
		}
		objNum := firstObj
		for i := 0; i < count; i++ {
			pos = skipWS(data, pos)
			lineEnd := pos
			for lineEnd < len(data) && data[lineEnd] != '\r' && data[lineEnd] != '\n' {
				lineEnd++
			}
			fields := bytes.Fields(data[pos:lineEnd])
			if len(fields) != 3 {
				return 0, nil, fmt.Errorf("pdfscan: malformed xref entry %q", string(data[pos:lineEnd]))
			}
			entryOff, e1 := strconv.Atoi(string(fields[0]))
			_, e2 := strconv.Atoi(string(fields[1]))
			typ := fields[2][0]
			pos = lineEnd
			if e1 != nil || e2 != nil {
				return 0, nil, errors.New("pdfscan: non-numeric xref entry")
			}
			if typ == 'n' {
				// Revisions are visited newest-first; keep the first
				// (newest) definition of each object number.
				if _, exists := entries[objNum]; !exists {
					entries[objNum] = xrefEntry{typ: 1, offset: entryOff}
				}
			} else if typ == 'f' {
				entries[objNum] = xrefEntry{typ: 0}
			}
			// Free entries ('f') remove nothing: newer revisions simply
			// overwrite via the normal newest-wins merge order.
			objNum++
		}
	}

	p := newParser(data[pos:])
	tv, err := p.parseValue()
	if err != nil || tv.Kind != KindDict {
		return 0, nil, errors.New("pdfscan: malformed trailer dictionary")
	}
	prev := 0
	if pv := tv.Dict.Get("Prev"); pv != nil && pv.Kind == KindInt {
		prev = int(pv.Int)
	}
	return prev, tv.Dict, nil
}

func skipWS(data []byte, pos int) int {
	for pos < len(data) && isWhite(data[pos]) {
		pos++
	}
	return pos
}

// parseXrefStream parses a /Type /XRef stream and merges its entries.
func parseXrefStream(data []byte, off int, entries map[int]xrefEntry) (int, *Dict, error) {
	st, err := streamAtOffset(data, off)
	if err != nil {
		return 0, nil, err
	}
	body, err := decodeStream(st)
	if err != nil {
		return 0, nil, err
	}

	wv := st.Dict.Get("W")
	if wv == nil || wv.Kind != KindArray || len(wv.Array) != 3 {
		return 0, nil, errors.New("pdfscan: xref stream missing /W array")
	}
	widths := [3]int{}
	for i := 0; i < 3; i++ {
		if wv.Array[i].Kind != KindInt {
			return 0, nil, errors.New("pdfscan: xref /W entries must be integers")
		}
		widths[i] = int(wv.Array[i].Int)
	}
	rowLen := widths[0] + widths[1] + widths[2]
	if rowLen <= 0 || len(body)%rowLen != 0 {
		return 0, nil, errors.New("pdfscan: xref stream length does not match /W")
	}

	var indexPairs []int
	if iv := st.Dict.Get("Index"); iv != nil && iv.Kind == KindArray {
		for _, item := range iv.Array {
			if item.Kind != KindInt {
				return 0, nil, errors.New("pdfscan: xref /Index entries must be integers")
			}
			indexPairs = append(indexPairs, int(item.Int))
		}
	} else {
		size := 0
		if sv := st.Dict.Get("Size"); sv != nil && sv.Kind == KindInt {
			size = int(sv.Int)
		}
		indexPairs = []int{0, size}
	}

	row := 0
	for pair := 0; pair+1 < len(indexPairs); pair += 2 {
		start := indexPairs[pair]
		count := indexPairs[pair+1]
		for i := 0; i < count; i++ {
			if row >= len(body)/rowLen {
				return 0, nil, errors.New("pdfscan: xref stream has fewer rows than /Index claims")
			}
			base := row * rowLen
			typ := 1
			if widths[0] > 0 {
				typ = int(readBigInt(body[base : base+widths[0]]))
			}
			f1 := int(readBigInt(body[base+widths[0] : base+widths[0]+widths[1]]))
			f2 := int(readBigInt(body[base+widths[0]+widths[1] : base+rowLen]))
			switch typ {
			case 0:
				entries[start+i] = xrefEntry{typ: 0}
			case 1:
				if _, exists := entries[start+i]; !exists {
					entries[start+i] = xrefEntry{typ: 1, offset: f1}
				}
			case 2:
				if _, exists := entries[start+i]; !exists {
					entries[start+i] = xrefEntry{typ: 2, streamNum: f1, streamIdx: f2}
				}
			}
			row++
		}
	}

	prev := 0
	if pv := st.Dict.Get("Prev"); pv != nil && pv.Kind == KindInt {
		prev = int(pv.Int)
	}
	return prev, st.Dict, nil
}

func readBigInt(b []byte) int64 {
	var n int64
	for _, c := range b {
		n = n<<8 + int64(c)
	}
	return n
}

// streamAtOffset reparses an indirect object located at off and returns its
// stream payload.
func streamAtOffset(data []byte, off int) (*Stream, error) {
	p := newParser(data[off:])
	_, _, v, err := p.parseIndirect()
	if err != nil {
		return nil, err
	}
	if v.Kind != KindStream {
		return nil, errors.New("pdfscan: expected stream object at xref offset")
	}
	return v.Stream, nil
}

package pdfscan

import (
	"bytes"
	"errors"
	"fmt"
)

// store resolves indirect references to the latest effective object
// version, including objects hidden inside Flate-compressed object streams
// and objects replaced through incremental updates.
type store struct {
	data    []byte
	entries map[int]xrefEntry

	objects map[int]*Value // regular indirect objects, lazily parsed
	carved  map[int]*Value // fallback: last textual definition of each number
	objStms map[int]map[int]*Value
	root    *Value
}

func newStore(data []byte, info *xrefInfo) (*store, error) {
	s := &store{
		data:    data,
		entries: info.entries,
		objects: map[int]*Value{},
		carved:  map[int]*Value{},
		objStms: map[int]map[int]*Value{},
	}
	s.carved = carveObjects(data)

	root, ok := s.resolve(info.root)
	if !ok || root.Kind != KindDict {
		return nil, errors.New("pdfscan: document catalog is missing or invalid")
	}
	s.root = root
	return s, nil
}

// resolve follows an indirect reference to its effective object.
func (s *store) resolve(ref *Value) (*Value, bool) {
	if ref == nil {
		return nil, false
	}
	if ref.Kind != KindRef {
		return ref, true
	}
	num := ref.RefNum
	if entry, ok := s.entries[num]; ok && entry.typ == 2 {
		return s.resolveCompressed(num, entry.streamNum)
	}
	return s.loadRegular(num)
}

func (s *store) loadRegular(num int) (*Value, bool) {
	if v, ok := s.objects[num]; ok {
		return v, ok
	}
	entry, hasEntry := s.entries[num]
	if hasEntry && entry.typ == 0 {
		return nil, false
	}
	if hasEntry && entry.typ == 1 && entry.offset > 0 && entry.offset < len(s.data) {
		p := newParser(s.data[entry.offset:])
		gotNum, _, v, err := p.parseIndirect()
		if err == nil && gotNum == num {
			s.objects[num] = v
			return v, true
		}
	}
	// Fallback for broken/partial cross-references: use the last textual
	// definition found in the file.
	if v, ok := s.carved[num]; ok {
		s.objects[num] = v
		return v, true
	}
	return nil, false
}

func (s *store) resolveCompressed(num, streamNum int) (*Value, bool) {
	objs, err := s.loadObjStm(streamNum)
	if err != nil {
		return nil, false
	}
	v, ok := objs[num]
	return v, ok
}

func (s *store) loadObjStm(streamNum int) (map[int]*Value, error) {
	if objs, ok := s.objStms[streamNum]; ok {
		return objs, nil
	}
	v, ok := s.loadRegular(streamNum)
	if !ok || v.Kind != KindStream {
		return nil, fmt.Errorf("pdfscan: object stream %d not found", streamNum)
	}
	body, err := decodeStream(v.Stream)
	if err != nil {
		return nil, err
	}
	n := dictInt(v.Stream.Dict, "N", -1)
	first := dictInt(v.Stream.Dict, "First", -1)
	if n <= 0 || first < 0 || first > len(body) {
		return nil, fmt.Errorf("pdfscan: object stream %d has invalid N/First", streamNum)
	}
	objs, err := parseObjectStreamBody(body, n, first)
	if err != nil {
		return nil, err
	}
	if len(objs) > maxObjects {
		return nil, errors.New("pdfscan: too many compressed objects")
	}
	s.objStms[streamNum] = objs
	return objs, nil
}

// carveObjects scans the whole file for indirect object definitions and
// keeps the last definition of every object number. It is only a fallback
// path for documents whose cross-references cannot be parsed.
func carveObjects(data []byte) map[int]*Value {
	out := map[int]*Value{}
	pos := 0
	for pos < len(data) {
		idx := bytes.Index(data[pos:], []byte(" obj"))
		if idx < 0 {
			break
		}
		headerStart := pos + idx
		num, gen, objectStart, ok := objectHeaderAt(data, headerStart)
		if !ok {
			pos = headerStart + 4
			continue
		}
		p := newParser(data[objectStart:])
		if gotNum, gotGen, v, err := p.parseIndirect(); err == nil && gotNum == num && gotGen == gen {
			out[num] = v
		}
		pos = headerStart + 4
	}
	return out
}

// objectHeaderAt verifies that the " obj" marker at markerStart is the end
// of an "N G obj" header and returns the parsed object and generation
// numbers.
func objectHeaderAt(data []byte, markerStart int) (num, gen, objectStart int, ok bool) {
	// Skip the single leading whitespace that precedes "obj".
	i := markerStart
	for i > 0 && isWhite(data[i-1]) {
		i--
	}
	gEnd := i
	for i > 0 && data[i-1] >= '0' && data[i-1] <= '9' {
		i--
	}
	if i == gEnd {
		return 0, 0, 0, false
	}
	gen = atoi(data[i:gEnd])
	for i > 0 && isWhite(data[i-1]) {
		i--
	}
	nEnd := i
	for i > 0 && data[i-1] >= '0' && data[i-1] <= '9' {
		i--
	}
	if i == nEnd {
		return 0, 0, 0, false
	}
	if i > 0 && !isWhite(data[i-1]) {
		return 0, 0, 0, false
	}
	num = atoi(data[i:nEnd])
	return num, gen, i, true
}

func atoi(b []byte) int {
	n := 0
	for _, c := range b {
		n = n*10 + int(c-'0')
	}
	return n
}

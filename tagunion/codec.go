package tagunion

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// 帧格式（单对象，多对象批量见 batch.go）：
//
//	tag      uvarint
//	version  uvarint
//	length   uvarint          载荷字节数，先验证 ≤ MaxPayloadLen 再分配
//	payload  [length]byte
//	checksum uint32 小端      CRC32-IEEE(tag‖version‖length‖payload 的编码字节)
//
// 解码是纯数据解析：不执行宿主代码、不反射构造任意类型。

// Encode 把 fields 按 (tag, version) 的已注册 schema 编码。
// 编码前先验证标签与载荷一致：字段名、类型、数量必须与 schema 完全匹配，
// 用标签 A 搭标签 B 的字段会被拒绝。未知标签请用 EncodeUnknown 显式透传。
func (r *Registry) Encode(tag, version uint64, fields map[string]any) ([]byte, error) {
	s, ok := r.schema(tag, version)
	if !ok {
		if r.knownTag(tag) {
			return nil, errf(0, "tag=%d version=%d: version not registered", tag, version)
		}
		return nil, errf(0, "tag=%d: unknown tag; use EncodeUnknown to pass through raw payload", tag)
	}
	payload, err := encodeFields(s, fields)
	if err != nil {
		return nil, err
	}
	return frame(tag, version, payload), nil
}

// EncodeUnknown 对未知标签只做帧封装，不解释载荷内容（原样保留语义）。
func EncodeUnknown(tag, version uint64, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadLen {
		return nil, errf(0, "payload length %d exceeds max %d", len(payload), MaxPayloadLen)
	}
	return frame(tag, version, payload), nil
}

// Marshal 把解码得到的 Value 重新编码。未知分支逐字节重放原始帧。
func (r *Registry) Marshal(v Value) ([]byte, error) {
	if v.Kind == KindUnknown {
		if v.Frame == nil {
			return nil, errf(0, "tag=%d: unknown value without original frame", v.Tag)
		}
		return append([]byte(nil), v.Frame...), nil
	}
	return r.Encode(v.Tag, v.Version, v.Fields)
}

func frame(tag, version uint64, payload []byte) []byte {
	var hdr [binary.MaxVarintLen64 * 3]byte
	n := 0
	n += binary.PutUvarint(hdr[n:], tag)
	n += binary.PutUvarint(hdr[n:], version)
	n += binary.PutUvarint(hdr[n:], uint64(len(payload)))
	crc := crc32.ChecksumIEEE(hdr[:n])
	crc = crc32.Update(crc, crc32.IEEETable, payload)
	out := make([]byte, 0, n+len(payload)+4)
	out = append(out, hdr[:n]...)
	out = append(out, payload...)
	var c [4]byte
	binary.LittleEndian.PutUint32(c[:], crc)
	return append(out, c[:]...)
}

// Decode 从 data 起点解码一个帧，返回对象与消耗的字节数。
func (r *Registry) Decode(data []byte) (Value, int, error) {
	return r.decodeAt(data, 0, "")
}

func (r *Registry) decodeAt(data []byte, start int, path string) (Value, int, error) {
	v, n, err := r.decodeFrame(data, start)
	if err != nil {
		return Value{}, 0, withPath(path, err)
	}
	return v, n, nil
}

func (r *Registry) decodeFrame(data []byte, start int) (Value, int, error) {
	pos := start
	tag, n, err := readUvarint(data, pos, "tag")
	if err != nil {
		return Value{}, 0, err
	}
	pos += n
	version, n, err := readUvarint(data, pos, "version")
	if err != nil {
		return Value{}, 0, err
	}
	pos += n
	lengthOff := pos
	length, n, err := readUvarint(data, pos, "length")
	if err != nil {
		return Value{}, 0, err
	}
	pos += n
	// 先验证长度，再分配/切片。
	if length > MaxPayloadLen {
		return Value{}, 0, errf(lengthOff, "payload length %d exceeds max %d", length, MaxPayloadLen)
	}
	if uint64(len(data)-pos) < length+4 {
		return Value{}, 0, errf(pos, "truncated: need %d payload+checksum bytes, have %d", length+4, len(data)-pos)
	}
	payloadOff := pos
	payload := data[pos : pos+int(length)]
	pos += int(length)
	wantCRC := binary.LittleEndian.Uint32(data[pos : pos+4])
	gotCRC := crc32.ChecksumIEEE(data[start:pos])
	if wantCRC != gotCRC {
		return Value{}, 0, errf(pos, "checksum mismatch: stored %#08x, computed %#08x", wantCRC, gotCRC)
	}
	end := pos + 4

	s, ok := r.schema(tag, version)
	if !ok {
		// 未知标签或未知版本：原样保留（含版本与校验），绝不按巧合字段套用旧 schema。
		raw := append([]byte(nil), payload...)
		fr := append([]byte(nil), data[start:end]...)
		return Value{Kind: KindUnknown, Tag: tag, Version: version, Raw: raw, Frame: fr}, end - start, nil
	}
	fields, err := decodeFields(s, payload, payloadOff)
	if err != nil {
		return Value{}, 0, err
	}
	latest, migrated, err := r.migrateToLatest(tag, version, fields)
	if err != nil {
		return Value{}, 0, errf(start, "%v", err)
	}
	return Value{Kind: KindKnown, Tag: tag, Version: latest, Fields: migrated}, end - start, nil
}

func readUvarint(data []byte, off int, what string) (uint64, int, error) {
	if off >= len(data) {
		return 0, 0, errf(off, "truncated: reading %s", what)
	}
	v, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return 0, 0, errf(off, "invalid varint for %s", what)
	}
	return v, n, nil
}

// encodeFields 严格校验：字段集必须与 schema 完全一致（不多不少、类型相符）。
func encodeFields(s *Schema, fields map[string]any) ([]byte, error) {
	if len(fields) != len(s.Fields) {
		return nil, errf(0, "tag=%d: schema has %d fields, got %d", s.Tag, len(s.Fields), len(fields))
	}
	var out []byte
	for _, fd := range s.Fields {
		val, ok := fields[fd.Name]
		if !ok {
			return nil, errf(0, "tag=%d: missing field %q", s.Tag, fd.Name)
		}
		b, err := encodeScalar(fd, val)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func encodeScalar(fd FieldDef, val any) ([]byte, error) {
	switch fd.Type {
	case FUint:
		v, ok := val.(uint64)
		if !ok {
			return nil, errf(0, "field %q: want uint64, got %T", fd.Name, val)
		}
		var b [binary.MaxVarintLen64]byte
		return append([]byte(nil), b[:binary.PutUvarint(b[:], v)]...), nil
	case FInt:
		v, ok := val.(int64)
		if !ok {
			return nil, errf(0, "field %q: want int64, got %T", fd.Name, val)
		}
		var b [binary.MaxVarintLen64]byte
		return append([]byte(nil), b[:binary.PutVarint(b[:], v)]...), nil
	case FBool:
		v, ok := val.(bool)
		if !ok {
			return nil, errf(0, "field %q: want bool, got %T", fd.Name, val)
		}
		if v {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case FString:
		v, ok := val.(string)
		if !ok {
			return nil, errf(0, "field %q: want string, got %T", fd.Name, val)
		}
		if len(v) > MaxStringLen {
			return nil, errf(0, "field %q: string length %d exceeds max %d", fd.Name, len(v), MaxStringLen)
		}
		var b [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(b[:], uint64(len(v)))
		return append(append([]byte(nil), b[:n]...), v...), nil
	case FBytes:
		v, ok := val.([]byte)
		if !ok {
			return nil, errf(0, "field %q: want []byte, got %T", fd.Name, val)
		}
		if len(v) > MaxPayloadLen {
			return nil, errf(0, "field %q: bytes length %d exceeds max %d", fd.Name, len(v), MaxPayloadLen)
		}
		var b [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(b[:], uint64(len(v)))
		return append(append([]byte(nil), b[:n]...), v...), nil
	}
	return nil, errf(0, "field %q: unsupported type %v", fd.Name, fd.Type)
}

// decodeFields 严格按 schema 解码载荷；解码后必须恰好耗尽载荷，
// 多余字节视为错误，避免把巧合字段当作合法数据。
func decodeFields(s *Schema, payload []byte, baseOff int) (map[string]any, error) {
	fields := make(map[string]any, len(s.Fields))
	pos := 0
	for _, fd := range s.Fields {
		val, n, err := decodeScalar(fd, payload, pos, baseOff)
		if err != nil {
			return nil, err
		}
		fields[fd.Name] = val
		pos += n
	}
	if pos != len(payload) {
		return nil, errf(baseOff+pos, "tag=%d: %d trailing bytes after schema fields", s.Tag, len(payload)-pos)
	}
	return fields, nil
}

func decodeScalar(fd FieldDef, buf []byte, pos, baseOff int) (any, int, error) {
	fieldErr := func(off int, format string, args ...any) error {
		e := errf(off, format, args...)
		e.Path = fmt.Sprintf("field(%s)", fd.Name)
		return e
	}
	switch fd.Type {
	case FUint:
		v, n := binary.Uvarint(buf[pos:])
		if n <= 0 {
			return nil, 0, fieldErr(baseOff+pos, "invalid uvarint")
		}
		return v, n, nil
	case FInt:
		v, n := binary.Varint(buf[pos:])
		if n <= 0 {
			return nil, 0, fieldErr(baseOff+pos, "invalid varint")
		}
		return v, n, nil
	case FBool:
		if pos >= len(buf) {
			return nil, 0, fieldErr(baseOff+pos, "truncated bool")
		}
		switch buf[pos] {
		case 0:
			return false, 1, nil
		case 1:
			return true, 1, nil
		}
		return nil, 0, fieldErr(baseOff+pos, "invalid bool value %d", buf[pos])
	case FString, FBytes:
		v, n := binary.Uvarint(buf[pos:])
		if n <= 0 {
			return nil, 0, fieldErr(baseOff+pos, "invalid length prefix")
		}
		limit := uint64(MaxPayloadLen)
		if fd.Type == FString {
			limit = MaxStringLen
		}
		if v > limit {
			return nil, 0, fieldErr(baseOff+pos, "length %d exceeds max %d", v, limit)
		}
		p := pos + n
		if uint64(len(buf)-p) < v {
			return nil, 0, fieldErr(baseOff+p, "truncated: need %d bytes, have %d", v, len(buf)-p)
		}
		if fd.Type == FString {
			return string(buf[p : p+int(v)]), n + int(v), nil
		}
		return append([]byte(nil), buf[p:p+int(v)]...), n + int(v), nil
	}
	return nil, 0, fieldErr(baseOff+pos, "unsupported type %v", fd.Type)
}

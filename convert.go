package bridge

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// Trace records what a single conversion did, for difference reports.
type Trace struct {
	ViaExtension []uint32 // fields carried inside the extension blob
	Restored     []uint32 // fields restored from an extension blob
	Defaulted    []uint32 // fields injected by migration rules
}

func (t *Trace) sortAll() {
	sort.Slice(t.ViaExtension, func(i, j int) bool { return t.ViaExtension[i] < t.ViaExtension[j] })
	sort.Slice(t.Restored, func(i, j int) bool { return t.Restored[i] < t.Restored[j] })
	sort.Slice(t.Defaulted, func(i, j int) bool { return t.Defaulted[i] < t.Defaulted[j] })
}

func parseExtension(data []byte) ([]RawField, error) {
	var out []RawField
	for len(data) > 0 {
		id, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, fmt.Errorf("%w: corrupt extension blob (field id)", ErrBadData)
		}
		data = data[n:]
		if len(data) < 1 {
			return nil, fmt.Errorf("%w: corrupt extension blob (type)", ErrBadData)
		}
		t := FieldType(data[0])
		data = data[1:]
		l, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, fmt.Errorf("%w: corrupt extension blob (length)", ErrBadData)
		}
		data = data[n:]
		if l > uint64(len(data)) {
			return nil, fmt.Errorf("%w: corrupt extension blob (truncated)", ErrBadData)
		}
		out = append(out, RawField{ID: uint32(id), Type: t, Data: append([]byte(nil), data[:l]...)})
		data = data[l:]
	}
	return out, nil
}

// Convert transforms a message under a compiled plan. Fields the target
// schema cannot represent are packed into the extension blob; extension
// blobs from earlier hops are unpacked and restored when the target knows
// the fields again. The result is built fully before being returned, so a
// failure never yields a half-converted message.
func Convert(p *Plan, m *Message) (*Message, *Trace, error) {
	out := NewMessage()
	tr := &Trace{}
	var pack []RawField

	ids := make([]uint32, 0, len(m.Values))
	for id := range m.Values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		v := m.Values[id]
		if f, ok := p.To.Fields[id]; ok {
			if f.Type != v.Type {
				return nil, nil, fmt.Errorf("%w: field %d type %s, plan expects %s", ErrBadData, id, v.Type, f.Type)
			}
			out.Values[id] = v
		} else {
			pack = append(pack, RawField{ID: id, Type: v.Type, Data: valuePayload(v)})
		}
	}

	var carry []RawField
	for _, u := range m.Unknown {
		if u.ID != ExtFieldID {
			carry = append(carry, u)
			continue
		}
		if u.Type != TyBytes {
			return nil, nil, fmt.Errorf("%w: extension blob must be bytes", ErrBadData)
		}
		fields, err := parseExtension(u.Data)
		if err != nil {
			return nil, nil, err
		}
		for _, rf := range fields {
			if f, ok := p.To.Fields[rf.ID]; ok {
				if f.Type != rf.Type {
					return nil, nil, fmt.Errorf("%w: restored field %d type %s, schema says %s", ErrBadData, rf.ID, rf.Type, f.Type)
				}
				if _, exists := out.Values[rf.ID]; !exists {
					v, err := decodeValue(rf.Type, rf.Data)
					if err != nil {
						return nil, nil, err
					}
					out.Values[rf.ID] = v
					tr.Restored = append(tr.Restored, rf.ID)
				}
			} else {
				carry = append(carry, rf)
			}
		}
	}

	for id, def := range p.Defaults {
		if _, ok := out.Values[id]; !ok {
			out.Values[id] = def
			tr.Defaulted = append(tr.Defaulted, id)
		}
	}
	for _, f := range p.To.Fields {
		if f.Required {
			if _, ok := out.Values[f.ID]; !ok {
				return nil, nil, fmt.Errorf("bridge: required field %d (%s) unrepresentable", f.ID, f.Name)
			}
		}
	}

	pack = append(pack, carry...)
	if len(pack) > 0 {
		var blob []byte
		for _, rf := range pack {
			blob = appendRawField(blob, rf)
		}
		out.Unknown = append(out.Unknown, RawField{ID: ExtFieldID, Type: TyBytes, Data: blob})
		for _, rf := range pack {
			tr.ViaExtension = append(tr.ViaExtension, rf.ID)
		}
	}
	tr.sortAll()
	return out, tr, nil
}

// ConvertFrame decodes, converts and re-encodes one frame payload
// atomically: any error discards the whole frame.
func ConvertFrame(p *Plan, framePayload []byte) ([]byte, *Trace, error) {
	m, err := DecodeMessage(p.From, framePayload)
	if err != nil {
		return nil, nil, err
	}
	out, tr, err := Convert(p, m)
	if err != nil {
		return nil, nil, err
	}
	return out.Encode(), tr, nil
}

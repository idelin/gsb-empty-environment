package wirecodec

import "errors"

// errFieldWireType is returned when a known field arrives with the wrong wire type.
var errFieldWireType = errors.New("wirecodec: field encoded with unexpected wire type")

// ---------------------------------------------------------------------------
// Schema (hand-written, mirrors a .proto definition):
//
//   message Geo {
//       double lat = 1;            // fixed64
//       double lng = 2;            // fixed64
//   }
//   message Address {
//       string city = 1;           // length-delimited
//       uint32 zip  = 2;           // varint
//       Geo    geo  = 3;           // nested message
//   }
//   message Person {
//       string          name    = 1; // length-delimited
//       uint64          id      = 2; // varint
//       sint64          balance = 3; // zigzag varint
//       repeated sint32 scores  = 4; // packed (wire 2), unpacked (wire 0) also accepted
//       Address         address = 5; // nested message (level 2 nesting via Geo)
//       repeated string tags    = 6; // length-delimited
//       bytes           avatar  = 7; // zero-copy payload
//       fixed32         flags   = 8; // fixed32
//       double          ratio   = 9; // fixed64
//   }
// ---------------------------------------------------------------------------

// Geo is a level-3 nested message using fixed64 fields.
type Geo struct {
	Lat float64
	Lng float64
}

// Marshal encodes g to the protobuf wire format.
func (g *Geo) Marshal() []byte {
	w := NewWriter()
	w.DoubleField(1, g.Lat)
	w.DoubleField(2, g.Lng)
	return w.Bytes()
}

// Unmarshal decodes g from data.
func (g *Geo) Unmarshal(data []byte) error {
	r := NewReader(data)
	for !r.Done() {
		field, wt, err := r.Tag()
		if err != nil {
			return err
		}
		switch field {
		case 1:
			if wt != WireFixed64 {
				return errFieldWireType
			}
			if g.Lat, err = r.Double(); err != nil {
				return err
			}
		case 2:
			if wt != WireFixed64 {
				return errFieldWireType
			}
			if g.Lng, err = r.Double(); err != nil {
				return err
			}
		default:
			if err := r.Skip(wt); err != nil {
				return err
			}
		}
	}
	return nil
}

// Address is a level-2 nested message.
type Address struct {
	City string
	Zip  uint32
	Geo  *Geo
}

// Marshal encodes a to the protobuf wire format.
func (a *Address) Marshal() []byte {
	w := NewWriter()
	if a.City != "" {
		w.StringField(1, a.City)
	}
	if a.Zip != 0 {
		w.Uint64Field(2, uint64(a.Zip))
	}
	if a.Geo != nil {
		w.MessageField(3, a.Geo.Marshal())
	}
	return w.Bytes()
}

// Unmarshal decodes a from data.
func (a *Address) Unmarshal(data []byte) error {
	r := NewReader(data)
	for !r.Done() {
		field, wt, err := r.Tag()
		if err != nil {
			return err
		}
		switch field {
		case 1:
			if wt != WireBytes {
				return errFieldWireType
			}
			if a.City, err = r.String(); err != nil {
				return err
			}
		case 2:
			if wt != WireVarint {
				return errFieldWireType
			}
			v, err := r.Varint()
			if err != nil {
				return err
			}
			a.Zip = uint32(v)
		case 3:
			if wt != WireBytes {
				return errFieldWireType
			}
			raw, err := r.Bytes() // zero-copy sub-slice of the parent stream
			if err != nil {
				return err
			}
			geo := new(Geo)
			if err := geo.Unmarshal(raw); err != nil {
				return err
			}
			a.Geo = geo
		default:
			if err := r.Skip(wt); err != nil {
				return err
			}
		}
	}
	return nil
}

// Person is the top-level message exercising every supported wire type.
type Person struct {
	Name    string
	ID      uint64
	Balance int64
	Scores  []int32
	Address *Address
	Tags    []string
	Avatar  []byte // decoded zero-copy: aliases the input buffer
	Flags   uint32
	Ratio   float64
}

// Marshal encodes p to the protobuf wire format. Scores are emitted as a
// packed repeated field.
func (p *Person) Marshal() []byte {
	w := NewWriter()
	if p.Name != "" {
		w.StringField(1, p.Name)
	}
	if p.ID != 0 {
		w.Uint64Field(2, p.ID)
	}
	if p.Balance != 0 {
		w.Sint64Field(3, p.Balance)
	}
	if len(p.Scores) > 0 {
		w.Tag(4, WireBytes)
		var packed []byte
		for _, s := range p.Scores {
			packed = AppendVarint(packed, EncodeZigZag(int64(s)))
		}
		w.WriteBytes(packed)
	}
	if p.Address != nil {
		w.MessageField(5, p.Address.Marshal())
	}
	for _, t := range p.Tags {
		w.StringField(6, t)
	}
	if len(p.Avatar) > 0 {
		w.BytesField(7, p.Avatar)
	}
	if p.Flags != 0 {
		w.Fixed32Field(8, p.Flags)
	}
	if p.Ratio != 0 {
		w.DoubleField(9, p.Ratio)
	}
	return w.Bytes()
}

// Unmarshal decodes p from data. Unknown fields are skipped (forward
// compatibility); packed and unpacked encodings of Scores are both accepted
// (backward compatibility). The Avatar field aliases data without copying.
func (p *Person) Unmarshal(data []byte) error {
	r := NewReader(data)
	for !r.Done() {
		field, wt, err := r.Tag()
		if err != nil {
			return err
		}
		switch field {
		case 1:
			if wt != WireBytes {
				return errFieldWireType
			}
			if p.Name, err = r.String(); err != nil {
				return err
			}
		case 2:
			if wt != WireVarint {
				return errFieldWireType
			}
			if p.ID, err = r.Varint(); err != nil {
				return err
			}
		case 3:
			if wt != WireVarint {
				return errFieldWireType
			}
			if p.Balance, err = r.ZigZag(); err != nil {
				return err
			}
		case 4:
			switch wt {
			case WireBytes: // packed
				raw, err := r.Bytes()
				if err != nil {
					return err
				}
				pr := NewReader(raw)
				for !pr.Done() {
					v, err := pr.ZigZag()
					if err != nil {
						return err
					}
					p.Scores = append(p.Scores, int32(v))
				}
			case WireVarint: // unpacked element
				v, err := r.ZigZag()
				if err != nil {
					return err
				}
				p.Scores = append(p.Scores, int32(v))
			default:
				return errFieldWireType
			}
		case 5:
			if wt != WireBytes {
				return errFieldWireType
			}
			raw, err := r.Bytes() // zero-copy sub-slice of the parent stream
			if err != nil {
				return err
			}
			addr := new(Address)
			if err := addr.Unmarshal(raw); err != nil {
				return err
			}
			p.Address = addr
		case 6:
			if wt != WireBytes {
				return errFieldWireType
			}
			s, err := r.String()
			if err != nil {
				return err
			}
			p.Tags = append(p.Tags, s)
		case 7:
			if wt != WireBytes {
				return errFieldWireType
			}
			if p.Avatar, err = r.Bytes(); err != nil { // zero-copy
				return err
			}
		case 8:
			if wt != WireFixed32 {
				return errFieldWireType
			}
			if p.Flags, err = r.Fixed32(); err != nil {
				return err
			}
		case 9:
			if wt != WireFixed64 {
				return errFieldWireType
			}
			if p.Ratio, err = r.Double(); err != nil {
				return err
			}
		default:
			if err := r.Skip(wt); err != nil {
				return err
			}
		}
	}
	return nil
}

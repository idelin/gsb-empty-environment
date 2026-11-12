package pdfscan

// Value kinds produced by the structural PDF parser. The scanner never
// renders or interprets page content; these values describe the raw PDF
// object graph only.
const (
	KindNull = iota
	KindBool
	KindInt
	KindReal
	KindName
	KindString
	KindArray
	KindDict
	KindStream
	KindRef
)

// Value is a parsed PDF object. Only the fields relevant to Kind are set.
//
// For KindRef the reference is stored verbatim; resolution to the latest
// effective version of the object is performed by the scanner so that
// objects hidden in object streams or superseded by incremental updates
// are handled correctly.
type Value struct {
	Kind int

	Bool   bool
	Int    int64
	Real   float64
	Str    []byte
	Array  []*Value
	Dict   *Dict
	Stream *Stream

	RefNum int
	RefGen int

	// ObjNum is the indirect object number when this value is the root
	// value of an indirect object (including compressed objects). It is 0
	// for values that are embedded directly inside another object.
	ObjNum int
}

// Dict preserves insertion order so that traversal is deterministic.
type Dict struct {
	Keys []string
	Map  map[string]*Value
}

// Get looks up a dictionary entry by (already normalized) name.
func (d *Dict) Get(name string) *Value {
	if d == nil || d.Map == nil {
		return nil
	}
	return d.Map[name]
}

// Stream is a stream dictionary together with its raw (still encoded)
// content bytes. Decoded bytes are produced lazily by the scanner.
type Stream struct {
	Dict   *Dict
	Raw    []byte
	ObjNum int
	Offset int
}

// IsRef reports whether v is an indirect reference.
func (v *Value) IsRef() bool { return v != nil && v.Kind == KindRef }

// AsName returns the normalized name when v is a name object.
func (v *Value) AsName() (string, bool) {
	if v != nil && v.Kind == KindName {
		return string(v.Str), true
	}
	return "", false
}

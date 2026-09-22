package bitlayout

// opKind identifies the instructions of the flat decode program.
type opKind int

const (
	opValue    opKind = iota // read bits, validate, store into current scope
	opAlign                  // verify zero padding up to a bit multiple
	opArray                  // begin an array; element ops are [elemStart, elemEnd)
	opUnion                  // jump to the case selected by a previous tag field
	opJump                   // end of a union case: close scope, jump to jumpTgt
	opChecksum               // read bits and verify against the covered range
	opEnd                    // record complete
)

type op struct {
	kind   opKind
	name   string
	bits   int
	signed bool
	enum   []uint64

	multiple int // opAlign

	lengthFrom string // opArray
	elemStart  int
	elemEnd    int
	elemMax    uint64

	tagFrom string            // opUnion
	cases   map[uint64][2]int // tag -> [start, end) op indices
	jumpTgt int               // opJump

	from, to string // opChecksum
}

func emitProgram(fields []cfield) []op {
	var ops []op
	emitFields(fields, &ops)
	return append(ops, op{kind: opEnd})
}

func emitFields(fields []cfield, ops *[]op) {
	for i := range fields {
		f := &fields[i]
		switch f.typ {
		case tUint, tInt:
			*ops = append(*ops, op{
				kind: opValue, name: f.name, bits: f.bits,
				signed: f.signed, enum: f.enum,
			})
		case tAlign:
			*ops = append(*ops, op{kind: opAlign, name: f.name, multiple: f.multiple})
		case tArray:
			idx := len(*ops)
			*ops = append(*ops, op{
				kind: opArray, name: f.name,
				lengthFrom: f.lengthFrom, elemMax: maxBitsOf(f.elem),
			})
			(*ops)[idx].elemStart = len(*ops)
			emitFields(f.elem, ops)
			(*ops)[idx].elemEnd = len(*ops)
		case tUnion:
			idx := len(*ops)
			*ops = append(*ops, op{
				kind: opUnion, name: f.name, tagFrom: f.tagFrom,
				cases: map[uint64][2]int{},
			})
			var jumps []int
			for _, tag := range f.caseOrder {
				start := len(*ops)
				emitFields(f.cases[tag], ops)
				jumps = append(jumps, len(*ops))
				*ops = append(*ops, op{kind: opJump, name: f.name})
				(*ops)[idx].cases[tag] = [2]int{start, len(*ops)}
			}
			end := len(*ops)
			for _, j := range jumps {
				(*ops)[j].jumpTgt = end
			}
		case tChecksum:
			*ops = append(*ops, op{
				kind: opChecksum, name: f.name, bits: f.bits,
				from: f.from, to: f.to,
			})
		}
	}
}

func maxBitsOf(fields []cfield) uint64 {
	total := uint64(0)
	for i := range fields {
		total += fields[i].maxBits // overflow already rejected at compile time
	}
	return total
}

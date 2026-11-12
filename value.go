package netsec

// source identifies one bit of one secret input.
type source struct {
	Input string
	Bit   int
}

// trace is a reversed path.  The base dependency of a secret input has a nil
// trace; every operation prepends a step.
type trace struct {
	prev   *trace
	step   Step
	length int
}

func prependTrace(previous *trace, step Step) *trace {
	length := 1
	if previous != nil {
		length = previous.length + 1
	}
	return &trace{prev: previous, step: step, length: length}
}

func flattenTrace(tr *trace) []Step {
	if tr == nil {
		return nil
	}
	steps := make([]Step, 0, tr.length)
	for current := tr; current != nil; current = current.prev {
		steps = append(steps, current.step)
	}
	for left, right := 0, len(steps)-1; left < right; left, right = left+1, right-1 {
		steps[left], steps[right] = steps[right], steps[left]
	}
	return steps
}

type bitDep struct {
	tr     *trace
	public bool
}

type bitInfo struct {
	deps  map[source]bitDep
	known int8 // -1 unknown, otherwise 0 or 1
}

type value struct {
	width int
	id    int
	bits  []bitInfo
}

func newValue(width, id int) value {
	if width < 0 {
		width = 0
	}
	v := value{width: width, id: id, bits: make([]bitInfo, width)}
	for i := range v.bits {
		v.bits[i].known = -1
	}
	return v
}

func (b *bitInfo) merge(src source, dep bitDep) {
	if b.deps == nil {
		b.deps = make(map[source]bitDep)
	}
	existing, ok := b.deps[src]
	if !ok {
		b.deps[src] = dep
		return
	}
	// A raw path always dominates a declassified path from the same source.
	if existing.public && !dep.public {
		b.deps[src] = dep
		return
	}
	if !existing.public && dep.public {
		return
	}
	if traceLength(dep.tr) < traceLength(existing.tr) {
		b.deps[src] = dep
	}
}

func traceLength(tr *trace) int {
	if tr == nil {
		return 0
	}
	return tr.length
}

func (v *value) mergeBit(outBit int, src source, dep bitDep) {
	v.bits[outBit].merge(src, dep)
}

func (v *value) mergeFromBit(outBit int, from bitInfo, step Step, declassify bool) {
	for src, dep := range from.deps {
		v.mergeBit(outBit, src, bitDep{
			tr:     prependTrace(dep.tr, step),
			public: dep.public || declassify,
		})
	}
}

func (v *value) setKnown(bit int, known int8) {
	if bit >= 0 && bit < len(v.bits) {
		v.bits[bit].known = known
	}
}

func (v value) knownBit(bit int) (int8, bool) {
	if bit < 0 || bit >= len(v.bits) || v.bits[bit].known < 0 {
		return -1, false
	}
	return v.bits[bit].known, true
}

func (v value) knownValue() (Mask, bool) {
	var out Mask
	for bit := 0; bit < v.width; bit++ {
		known, ok := v.knownBit(bit)
		if !ok {
			return 0, false
		}
		if known != 0 {
			out |= Bit(bit)
		}
	}
	return out, true
}

func setKnownValue(v *value, mask Mask) {
	for bit := 0; bit < v.width; bit++ {
		if mask.Has(bit) {
			v.setKnown(bit, 1)
		} else {
			v.setKnown(bit, 0)
		}
	}
}

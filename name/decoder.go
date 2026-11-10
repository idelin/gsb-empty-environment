package name

import "errors"

const (
	ptrMask    byte = 0xC0
	labelMask  byte = 0x3F
	maxNameLen int  = 255
)

var errInvalid = errors.New("invalid name")

const (
	stUnseen = iota
	stActive
	stValid
	stError
)

// entry is the memoized result of decoding a name that starts at one
// particular packet offset. Each resolved offset is visited at most once,
// so many starting offsets sharing the same suffix chain do not re-walk
// the chain.
type entry struct {
	state   int
	labels  [][]byte
	wireLen int
	nextAt  int
}

// Decoder parses DNS wire-format names out of a single packet.
type Decoder struct {
	packet []byte
	memo   []entry
	reads  int
}

// New returns a Decoder for the given packet bytes.
func New(packet []byte) *Decoder {
	return &Decoder{
		packet: packet,
		memo:   make([]entry, len(packet)),
	}
}

// Reads reports how many packet bytes have been fetched.
func (d *Decoder) Reads() int { return d.reads }

func (d *Decoder) byteAt(offset int) (byte, error) {
	if offset < 0 || offset >= len(d.packet) {
		return 0, errInvalid
	}
	d.reads++
	return d.packet[offset], nil
}

// Resolve decodes the name beginning at start. It returns the label
// sequence (root label omitted), the total wire length of the expanded
// name including every length octet and the root, and the offset of the
// byte immediately following the encoded name at start's own location.
func (d *Decoder) Resolve(start int) (labels [][]byte, wireLen, nextAt int, err error) {
	if start < 0 || start >= len(d.packet) {
		return nil, 0, 0, errInvalid
	}
	result := d.resolve(start, nil)
	if result.state != stValid {
		return nil, 0, 0, errInvalid
	}
	return result.labels, result.wireLen, result.nextAt, nil
}

func (d *Decoder) fail(offset int) *entry {
	e := &d.memo[offset]
	e.state = stError
	return e
}

func (d *Decoder) resolve(offset int, active map[int]bool) *entry {
	e := &d.memo[offset]
	switch e.state {
	case stValid, stError:
		return e
	case stActive:
		return e
	}
	e.state = stActive
	if active == nil {
		active = map[int]bool{}
	}
	active[offset] = true
	defer delete(active, offset)

	first, err := d.byteAt(offset)
	if err != nil {
		return d.fail(offset)
	}

	if first&ptrMask == ptrMask {
		// Two-byte compression pointer.
		second, err := d.byteAt(offset + 1)
		if err != nil {
			return d.fail(offset)
		}
		target := int(first&labelMask)<<8 | int(second)
		if target < 0 || target >= len(d.packet) {
			return d.fail(offset)
		}

		if active[target] || d.memo[target].state == stActive {
			// Pointer loop; every active entry on the cycle is invalid.
			d.fail(target)
			return d.fail(offset)
		}
		targetEntry := d.resolve(target, active)
		if targetEntry.state != stValid {
			return d.fail(offset)
		}
		e.labels = targetEntry.labels
		e.wireLen = targetEntry.wireLen
		e.nextAt = offset + 2
		e.state = stValid
		return e
	}

	if first&ptrMask != 0 {
		// Reserved 01/10 prefix.
		return d.fail(offset)
	}

	length := int(first & labelMask)
	if length == 0 {
		// Root label terminates the name.
		e.labels = [][]byte{}
		e.wireLen = 1
		e.nextAt = offset + 1
		e.state = stValid
		return e
	}

	start := offset + 1
	end := start + length
	if end > len(d.packet) {
		return d.fail(offset)
	}
	label := append([]byte(nil), d.packet[start:end]...)
	d.reads += length

	if end >= len(d.packet) {
		return d.fail(offset)
	}
	if active[end] || d.memo[end].state == stActive {
		d.fail(end)
		return d.fail(offset)
	}
	suffix := d.resolve(end, active)
	if suffix.state != stValid {
		return d.fail(offset)
	}

	wireLen := 1 + length + suffix.wireLen
	if wireLen > maxNameLen {
		return d.fail(offset)
	}

	labels := make([][]byte, 0, 1+len(suffix.labels))
	labels = append(labels, label)
	labels = append(labels, suffix.labels...)

	e.labels = labels
	e.wireLen = wireLen
	e.nextAt = suffix.nextAt
	e.state = stValid
	return e
}

package netsec

const (
	replayWindowBits  = 1024
	replayWindowWords = replayWindowBits / 64
)

type replayDecision uint8

const (
	replayAccepted replayDecision = iota
	replayDuplicate
	replayTooOld
)

// replayWindow is a highest-sequence bitmap. Bit zero represents the highest
// accepted sequence; bit N represents highest-N.
type replayWindow struct {
	bits        [replayWindowWords]uint64
	highest     uint64
	initialized bool
}

func (w *replayWindow) accept(sequence uint64) replayDecision {
	if !w.initialized {
		w.initialized = true
		w.highest = sequence
		w.set(0)
		return replayAccepted
	}

	if sequence > w.highest {
		w.shift(sequence - w.highest)
		w.highest = sequence
		w.set(0)
		return replayAccepted
	}

	offset := w.highest - sequence
	if offset >= replayWindowBits {
		return replayTooOld
	}
	if w.get(offset) {
		return replayDuplicate
	}
	w.set(offset)
	return replayAccepted
}

func (w *replayWindow) get(offset uint64) bool {
	word := offset / 64
	bit := offset % 64
	return w.bits[word]&(uint64(1)<<bit) != 0
}

func (w *replayWindow) set(offset uint64) {
	word := offset / 64
	bit := offset % 64
	w.bits[word] |= uint64(1) << bit
}

// shift moves existing offsets toward older entries after the highest seen
// sequence advances.
func (w *replayWindow) shift(distance uint64) {
	if distance >= replayWindowBits {
		w.bits = [replayWindowWords]uint64{}
		return
	}

	wordShift := int(distance / 64)
	bitShift := uint(distance % 64)

	if wordShift > 0 {
		for i := replayWindowWords - 1; i >= wordShift; i-- {
			w.bits[i] = w.bits[i-wordShift]
		}
		for i := 0; i < wordShift; i++ {
			w.bits[i] = 0
		}
	}

	if bitShift == 0 {
		return
	}
	for i := replayWindowWords - 1; i >= 0; i-- {
		value := w.bits[i] << bitShift
		if i > 0 {
			value |= w.bits[i-1] >> (64 - bitShift)
		}
		w.bits[i] = value
	}
}

func (w *replayWindow) reset() {
	*w = replayWindow{}
}

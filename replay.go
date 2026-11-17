package netsec

const (
	replayWindowBits  = 1024
	replayWindowWords = replayWindowBits / 64
)

// replayWindow stores a sliding window as a bitmap. Bit zero represents max,
// and bit n represents max-n.
type replayWindow struct {
	max    uint64
	words  [replayWindowWords]uint64
	seeded bool
}

func (w *replayWindow) accept(sequence uint64) error {
	if !w.seeded {
		w.seeded = true
		w.max = sequence
		w.words[0] = 1
		return nil
	}

	if sequence > w.max {
		w.advance(sequence - w.max)
		w.max = sequence
		w.words[0] |= 1
		return nil
	}

	distance := w.max - sequence
	if distance >= replayWindowBits {
		return ErrTooOld
	}
	if w.bit(distance) {
		return ErrDuplicate
	}
	w.words[distance/64] |= uint64(1) << (distance % 64)
	return nil
}

func (w *replayWindow) advance(distance uint64) {
	if distance >= replayWindowBits {
		w.words = [replayWindowWords]uint64{}
		return
	}

	wordShift := int(distance / 64)
	bitShift := uint(distance % 64)
	for dst := replayWindowWords - 1; dst >= 0; dst-- {
		src := dst - wordShift
		if src < 0 {
			w.words[dst] = 0
			continue
		}

		value := w.words[src] << bitShift
		if bitShift != 0 && src-1 >= 0 {
			value |= w.words[src-1] >> (64 - bitShift)
		}
		w.words[dst] = value
	}
}

func (w *replayWindow) bit(distance uint64) bool {
	return w.words[distance/64]&(uint64(1)<<(distance%64)) != 0
}

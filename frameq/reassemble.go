package frameq

import "fmt"

// Reassembler collects fragments and returns messages once complete.
// Fragments for one message must arrive in order (the Scheduler emits
// them in order); fragments of different messages may interleave.
type Reassembler struct {
	parts map[uint32][]byte
}

func NewReassembler() *Reassembler {
	return &Reassembler{parts: make(map[uint32][]byte)}
}

// Add consumes one fragment. It returns the reassembled message when the
// final fragment completes it, or nil otherwise.
func (r *Reassembler) Add(fr Fragment) ([]byte, error) {
	buf := r.parts[fr.MsgID]
	if len(buf) != int(fr.Offset) {
		return nil, fmt.Errorf("frameq: fragment for msg %d at offset %d, expected %d",
			fr.MsgID, fr.Offset, len(buf))
	}
	buf = append(buf, fr.Data...)
	if !fr.Last {
		r.parts[fr.MsgID] = buf
		return nil, nil
	}
	if len(buf) != int(fr.Total) {
		return nil, fmt.Errorf("frameq: msg %d completed at %d bytes, expected %d",
			fr.MsgID, len(buf), fr.Total)
	}
	delete(r.parts, fr.MsgID)
	return buf, nil
}

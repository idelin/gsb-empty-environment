package framepack

import "fmt"

// Reassembler stitches fragments back into whole messages. Fragments of a
// given message ID must arrive in send order (the Scheduler guarantees
// this); different message IDs may interleave freely.
type Reassembler struct {
	maxMsg int
	parts  map[uint32]*assembly
}

type assembly struct {
	total int
	buf   []byte
}

// NewReassembler creates a reassembler that rejects any message announcing
// more than maxMsg total bytes. The quota is checked before the assembly
// buffer is allocated.
func NewReassembler(maxMsg int) *Reassembler {
	return &Reassembler{maxMsg: maxMsg, parts: make(map[uint32]*assembly)}
}

// Push consumes one frame. When it completes a message, Push returns the
// full payload with done=true.
func (r *Reassembler) Push(f Frame) (payload []byte, done bool, err error) {
	if int(f.TotalLen) > r.maxMsg {
		return nil, false, fmt.Errorf("%w: message length %d exceeds max message %d", ErrBadData, f.TotalLen, r.maxMsg)
	}
	a, ok := r.parts[f.MsgID]
	if !ok {
		if !f.Start() {
			return nil, false, fmt.Errorf("%w: fragment for unknown message %d", ErrBadData, f.MsgID)
		}
		// Quota already verified above; only now allocate.
		a = &assembly{total: int(f.TotalLen), buf: make([]byte, 0, f.TotalLen)}
		r.parts[f.MsgID] = a
	}
	if int(f.TotalLen) != a.total {
		return nil, false, fmt.Errorf("%w: message %d changed length", ErrBadData, f.MsgID)
	}
	a.buf = append(a.buf, f.Payload...)
	if len(a.buf) > a.total {
		return nil, false, fmt.Errorf("%w: message %d overruns announced length", ErrBadData, f.MsgID)
	}
	if !f.End() {
		return nil, false, nil
	}
	if len(a.buf) != a.total {
		return nil, false, fmt.Errorf("%w: message %d ended short of announced length", ErrBadData, f.MsgID)
	}
	delete(r.parts, f.MsgID)
	return a.buf, true, nil
}

// Drop discards any partial assembly for id, e.g. after the sender
// canceled the message.
func (r *Reassembler) Drop(id uint32) { delete(r.parts, id) }

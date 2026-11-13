package netsec

const (
	// MaxDropout is the largest forward sequence-number gap that is accepted
	// without treating the packet as a possible stream restart.
	MaxDropout = 3000

	// MaxMisorder bounds the range of old packets which are treated as
	// duplicate or reordered packets instead of a very large sequence jump.
	MaxMisorder = 100

	// MinSequential is the number of sequential packets prescribed by
	// RFC 3550 for validating a source.
	MinSequential = 2

	rtpSeqMod = uint32(1 << 16)
)

// SequenceUpdate describes the result of feeding one RTP packet to a
// SequenceState.
type SequenceUpdate struct {
	Accepted       bool
	Resynchronized bool
	LargeJump      bool
}

// SequenceState implements the sequence-number validation algorithm from
// RFC 3550 appendix A.1. It is not safe for concurrent use.
type SequenceState struct {
	maxSeq    uint16
	cycles    uint32
	baseSeq   uint16
	badSeq    uint32
	probation uint8
}

// NewSequenceState starts a sequence epoch at seq. The first packet of a
// receive epoch establishes the baseline and is accepted by the caller.
func NewSequenceState(seq uint16) *SequenceState {
	s := &SequenceState{}
	s.init(seq)
	s.probation = 0
	return s
}

func (s *SequenceState) init(seq uint16) {
	s.baseSeq = seq
	s.maxSeq = seq
	s.cycles = 0
	s.badSeq = rtpSeqMod + 1
	s.probation = MinSequential
}

// MaxSequence returns the highest sequence number in the current epoch.
func (s *SequenceState) MaxSequence() uint16 {
	return s.maxSeq
}

// ExtendedSequence returns the rollover-adjusted highest sequence number.
func (s *SequenceState) ExtendedSequence() uint64 {
	return uint64(s.cycles) + uint64(s.maxSeq)
}

// Update applies RFC 3550 appendix A.1's update_seq to seq.
//
// A packet outside MAX_DROPOUT starts a probationary bad_seq. It is rejected,
// and only the immediately following sequence number causes a resynchronization.
// The resynchronizing packet is itself accepted so a newly synchronized stream
// can continue without a second, unrelated probation window.
func (s *SequenceState) Update(seq uint16) SequenceUpdate {
	if s.probation > 0 {
		if seq == s.maxSeq+1 {
			s.probation--
			s.maxSeq = seq
			if s.probation == 0 {
				s.init(seq)
				s.maxSeq = seq - 1
				s.probation = 0
				return SequenceUpdate{Accepted: true}
			}
		} else {
			s.probation = MinSequential - 1
			s.maxSeq = seq
		}
		return SequenceUpdate{}
	}

	delta := seq - s.maxSeq
	if delta < MaxDropout {
		if seq < s.maxSeq {
			s.cycles += rtpSeqMod
		}
		s.maxSeq = seq
		return SequenceUpdate{Accepted: true}
	}

	if uint32(delta) <= rtpSeqMod-MaxMisorder {
		if uint32(seq) == s.badSeq {
			s.init(seq)
			s.probation = 0
			return SequenceUpdate{Accepted: true, Resynchronized: true}
		}
		s.badSeq = uint32(seq) + 1
		return SequenceUpdate{LargeJump: true}
	}

	// A very old packet is a duplicate or reordering candidate. Keep the
	// current sequence epoch unchanged.
	return SequenceUpdate{Accepted: true}
}

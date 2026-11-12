package netsec

import "bytes"

// Event type tags from the TCG PC Client event log specification.
const (
	evNoAction uint32 = 0x00000003
)

// Header values of the TCG_PCR_EVENT2 / Spec ID structures.
const (
	specIDSignature = "Spec ID Event03"
	maxPCRIndex     = 31
)

// EventDigest is one algorithm-specific digest carried by an agile event.
type EventDigest struct {
	Algorithm AlgorithmID
	Digest    []byte
}

// LogEvent is a replayed TCG event log entry.
type LogEvent struct {
	PCRIndex  int
	EventType uint32
	Digests   []EventDigest
	EventData []byte
}

// ReplayedLog holds the result of replaying a TCG event log.
type ReplayedLog struct {
	// Events are the non-Spec-ID log entries in log order.
	Events []LogEvent
	// PCRs maps each advertised hash algorithm to its 32 PCR values.
	PCRs map[AlgorithmID][][]byte
	// Algorithms are the banks declared by the Spec ID event, in order.
	Algorithms []AlgorithmID
}

// ReplayEventLog parses the TCG PC Client binary event log and replays every
// PCR extend. The Spec ID event only establishes the digest layout of later
// entries; it is never extended. Missing digests, unknown algorithms and
// truncated entries cause the whole log to be rejected.
func ReplayEventLog(log []byte) (*ReplayedLog, error) {
	r := newByteReader(log)

	specAlgorithms, err := parseSpecID(r)
	if err != nil {
		return nil, err
	}

	pcrs := make(map[AlgorithmID][][]byte, len(specAlgorithms))
	for _, alg := range specAlgorithms {
		zero := make([][]byte, maxPCRIndex+1)
		spec, err := hashSpecFor(alg)
		if err != nil {
			return nil, err
		}
		for i := range zero {
			zero[i] = make([]byte, spec.size)
		}
		pcrs[alg] = zero
	}

	var events []LogEvent
	for r.remaining() > 0 {
		event, err := parseAgileEvent(r, specAlgorithms)
		if err != nil {
			return nil, err
		}
		if event.PCRIndex < 0 || event.PCRIndex > maxPCRIndex {
			return nil, ErrMalformedEventLog
		}
		if event.EventType != evNoAction {
			for _, d := range event.Digests {
				spec, err := hashSpecFor(d.Algorithm)
				if err != nil {
					return nil, err
				}
				current := pcrs[d.Algorithm][event.PCRIndex]
				hasher := spec.crypto.New()
				hasher.Write(current)
				hasher.Write(d.Digest)
				pcrs[d.Algorithm][event.PCRIndex] = hasher.Sum(nil)
			}
		}
		events = append(events, event)
	}

	return &ReplayedLog{
		Events:     events,
		PCRs:       pcrs,
		Algorithms: specAlgorithms,
	}, nil
}

// parseSpecID parses the leading legacy TCG_PCR_EVENT holding the Spec ID
// Event03 structure and returns the advertised digest algorithms in order.
func parseSpecID(r *byteReader) ([]AlgorithmID, error) {
	pcrIndex, err := r.readU32()
	if err != nil {
		return nil, ErrTruncatedEvent
	}
	eventType, err := r.readU32()
	if err != nil {
		return nil, ErrTruncatedEvent
	}
	digest, err := r.read(20)
	if err != nil {
		return nil, ErrTruncatedEvent
	}
	eventDataSize, err := r.readU32()
	if err != nil {
		return nil, ErrTruncatedEvent
	}
	eventData, err := r.read(int(eventDataSize))
	if err != nil {
		return nil, ErrTruncatedEvent
	}

	if pcrIndex != 0 || eventType != evNoAction {
		return nil, ErrMalformedEventLog
	}
	for _, b := range digest {
		if b != 0 {
			return nil, ErrMalformedEventLog
		}
	}
	return parseSpecIDEventData(eventData)
}

func parseSpecIDEventData(data []byte) ([]AlgorithmID, error) {
	if len(data) < 32 {
		return nil, ErrMalformedEventLog
	}
	// The signature is a 15-character NUL-padded 16-byte field.
	if string(bytes.TrimRight(data[0:16], "\x00")) != specIDSignature {
		return nil, ErrMalformedEventLog
	}
	// specVersionMinor, specVersionMajor, specErrata, uintnSize occupy bytes 20..23.
	uintnSize := data[23]
	if uintnSize != 2 {
		return nil, ErrMalformedEventLog
	}
	numberOfAlgorithms := int(data[24])<<8 | int(data[25])
	if numberOfAlgorithms == 0 {
		return nil, ErrMalformedEventLog
	}
	vendorInfoSize := int(data[26])<<8 | int(data[27])

	pos := 28
	type algSize struct {
		alg  AlgorithmID
		size uint16
	}
	entries := make([]algSize, 0, numberOfAlgorithms)
	seen := map[AlgorithmID]bool{}
	for i := 0; i < numberOfAlgorithms; i++ {
		if pos+4 > len(data) {
			return nil, ErrTruncatedEvent
		}
		alg := AlgorithmID(uint16(data[pos])<<8 | uint16(data[pos+1]))
		size := uint16(data[pos+2])<<8 | uint16(data[pos+3])
		pos += 4
		spec, err := hashSpecFor(alg)
		if err != nil {
			return nil, err
		}
		if int(size) != spec.size || seen[alg] {
			return nil, ErrMalformedEventLog
		}
		seen[alg] = true
		entries = append(entries, algSize{alg: alg, size: size})
	}

	if pos+vendorInfoSize != len(data) {
		return nil, ErrMalformedEventLog
	}
	algorithms := make([]AlgorithmID, len(entries))
	for i, entry := range entries {
		algorithms[i] = entry.alg
	}
	return algorithms, nil
}

// parseAgileEvent parses one TCG_PCR_EVENT2. Every algorithm declared by the
// Spec ID event must be present, and all digest lengths must match exactly.
func parseAgileEvent(r *byteReader, algorithms []AlgorithmID) (LogEvent, error) {
	pcrIndex, err := r.readU32()
	if err != nil {
		return LogEvent{}, ErrTruncatedEvent
	}
	if pcrIndex > maxPCRIndex {
		return LogEvent{}, ErrMalformedEventLog
	}
	eventType, err := r.readU32()
	if err != nil {
		return LogEvent{}, ErrTruncatedEvent
	}
	digestCount, err := r.readU32()
	if err != nil {
		return LogEvent{}, ErrTruncatedEvent
	}
	if digestCount == 0 || digestCount > uint32(len(algorithms)) {
		return LogEvent{}, ErrMalformedEventLog
	}

	event := LogEvent{
		PCRIndex:  int(pcrIndex),
		EventType: eventType,
		Digests:   make([]EventDigest, 0, digestCount),
	}
	present := map[AlgorithmID]bool{}
	for i := uint32(0); i < digestCount; i++ {
		algID, err := r.readU16()
		if err != nil {
			return LogEvent{}, ErrTruncatedEvent
		}
		spec, err := hashSpecFor(AlgorithmID(algID))
		if err != nil {
			return LogEvent{}, err
		}
		digest, err := r.read(spec.size)
		if err != nil {
			return LogEvent{}, ErrTruncatedEvent
		}
		if present[AlgorithmID(algID)] {
			return LogEvent{}, ErrMalformedEventLog
		}
		present[AlgorithmID(algID)] = true
		event.Digests = append(event.Digests, EventDigest{
			Algorithm: AlgorithmID(algID),
			Digest:    append([]byte{}, digest...),
		})
	}
	for _, alg := range algorithms {
		if !present[alg] {
			return LogEvent{}, ErrMissingDigest
		}
	}

	eventData, err := r.readSized16()
	if err != nil {
		return LogEvent{}, ErrTruncatedEvent
	}
	event.EventData = append([]byte{}, eventData...)
	return event, nil
}

// equalSelection reports whether two PCR selections encode the same banks and
// the same PCR indices, independent of ordering within each bank.
func equalSelection(a, b []PCRBank) bool {
	if len(a) != len(b) {
		return false
	}
	bMap := make(map[AlgorithmID][]int, len(b))
	for _, bank := range b {
		bMap[bank.Hash] = bank.PCRs
	}
	for _, bank := range a {
		other, ok := bMap[bank.Hash]
		if !ok || !sameIntSet(bank.PCRs, other) {
			return false
		}
	}
	return true
}

func sameIntSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int]bool, len(a))
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
}

func bytesEqual(a, b []byte) bool {
	return bytes.Equal(a, b)
}

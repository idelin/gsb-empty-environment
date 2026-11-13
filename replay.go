package netsec

// Replay runs the naive deterministic replayer on one complete input.
//
// input may be a []byte, string, or []int (one value per controlled byte).
// It must contain exactly as many bytes as len(Problem.Allowed). The chosen
// bytes occupy the beginning of the input slot; the rest of the slot is taken
// from the initial stack. Replay does not check that each byte is in its
// allowed set; Solve performs that check by enumerating the sets. A copy
// whose source prefix is shorter than the requested length traps
// immediately (TrapSourceShort). A destination outside [0,StackSize) traps
// immediately (TrapStackBounds). A trap stops all later writes and no return
// epilogue is reached, even if an earlier write already changed the saved
// return address.
func Replay(p Problem, input any) (Result, error) {
	m, err := normalize(p)
	if err != nil {
		return Result{Status: StatusInvalid, Err: err}, err
	}
	values, err := inputBytes(input)
	if err != nil {
		return Result{Status: StatusInvalid, Err: err}, err
	}
	if len(values) != len(m.allowed) {
		err = wrap(ErrInputLength, "got %d bytes, want %d", len(values), len(m.allowed))
		return Result{Status: StatusInvalid, Err: err}, err
	}
	return replayModel(m, values), nil
}

// ReplayInput is an alias for Replay.
func ReplayInput(p Problem, input any) (Result, error) { return Replay(p, input) }

// NaiveReplay is an alias for Replay.
func NaiveReplay(p Problem, input any) (Result, error) { return Replay(p, input) }

func inputBytes(input any) ([]byte, error) {
	switch v := input.(type) {
	case nil:
		return nil, nil
	case []byte:
		return append([]byte(nil), v...), nil
	case string:
		return []byte(v), nil
	case []int:
		out := make([]byte, len(v))
		for i, x := range v {
			if x < 0 || x > 255 {
				return nil, wrap(ErrInvalidInput, "value %d at %d", x, i)
			}
			out[i] = byte(x)
		}
		return out, nil
	case [MaxInputBytes]byte:
		return append([]byte(nil), v[:]...), nil
	default:
		return nil, wrap(ErrInvalidInput, "unsupported input type %T", input)
	}
}

func replayModel(m model, input []byte) Result {
	r := Result{
		Input:         append([]byte(nil), input...),
		InitialStack:  m.stack,
		InitialRet:    m.initialRet,
		CanaryEnabled: m.canary != nil,
		CanaryIntact:  true,
		CanaryOK:      true,
		TrapWrite:     -1,
		Candidates:    1,
		Exhausted:     true,
	}
	r.InputBytes = r.Input
	if m.canary != nil {
		r.CanaryInitial = m.canary.initial
	}

	stack := m.stack
	for i, b := range input {
		stack[m.input.Start+i] = b
	}
	r.InputString = append([]byte(nil), stack[m.input.Start:m.input.Start+m.input.Len]...)

	for i, w := range m.writes {
		offsetIndex, lengthIndex := w.inputIndices()
		offsetX := int(input[offsetIndex])
		lengthX := int(input[lengthIndex])

		// Source availability is checked first: the length form must name a
		// prefix that actually exists in the input string.
		length, lengthOK := w.lengthForm().evalChecked(lengthX)
		if !lengthOK || length < 0 || length > m.input.Len {
			r.Trapped = true
			r.TrapReason = TrapSourceShort
			r.TrapWrite = i
			break
		}
		offset, offsetOK := w.offsetForm().evalChecked(offsetX)
		if !offsetOK || offset < 0 || offset > StackSize || (length > 0 && offset+length > StackSize) {
			r.Trapped = true
			r.TrapReason = TrapStackBounds
			r.TrapWrite = i
			break
		}

		// Explicit temporary-copy (memmove) semantics: the source is read
		// before any byte of the destination is changed. The source always
		// starts at the beginning of the input string.
		source := Span{Start: m.input.Start, Len: length}
		var temp [StackSize]byte
		tmp := temp[:length]
		copy(tmp, stack[source.Start:source.Start+length])
		copy(stack[offset:offset+length], tmp)

		r.Writes = append(r.Writes, WriteSpan{
			Write:  i,
			Start:  offset,
			Offset: offset,
			Len:    length,
			Length: length,
			End:    offset + length,
			Source: source,
			Target: Span{Start: offset, Len: length},
		})
	}

	r.FinalStack = stack
	r.Stack = stack
	r.FinalRet = littleEndian(stack[m.returnOffset : m.returnOffset+ReturnSlotSize])
	r.ReturnAddr = r.FinalRet
	r.Changed = r.FinalRet != m.initialRet
	_, r.Allowed = m.targets[r.FinalRet]
	if m.canary != nil {
		copy(r.CanaryFinal[:], stack[m.canary.offset:m.canary.offset+CanarySize])
		r.CanaryIntact = r.CanaryFinal == m.canary.initial
		r.CanaryOK = r.CanaryIntact
	}

	if r.Trapped {
		r.Status = StatusTrap
		r.Returned = false
		r.Transferred = false
		r.WriteSpans = r.Writes
		r.Intervals = writeIntervals(r.Writes)
		return r
	}

	r.Returned = true
	r.Transferred = r.CanaryIntact && r.Allowed && r.Changed
	switch {
	case r.Transferred:
		r.Status = StatusExploit
	case !r.CanaryIntact:
		r.Status = StatusCanaryOnly
	default:
		r.Status = StatusNoExploit
	}
	r.WriteSpans = r.Writes
	r.Intervals = writeIntervals(r.Writes)
	return r
}

func writeIntervals(writes []WriteSpan) []Span {
	out := make([]Span, len(writes))
	for i, w := range writes {
		out[i] = Span{Start: w.Start, Len: w.Len}
	}
	return out
}

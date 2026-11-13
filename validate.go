package netsec

// model is the normalized, immutable description used by Replay and Solve.
type model struct {
	stack        [StackSize]byte // initial stack with ret and canary installed
	input        Span
	returnOffset int
	initialRet   uint32
	canary       *canaryModel
	allowed      [][]byte
	targets      map[uint32]struct{}
	writes       []Write
}

type canaryModel struct {
	offset  int
	initial [CanarySize]byte
}

// Validate normalizes a Problem and reports caller errors. It does not replay
// any input.
func Validate(p Problem) error {
	_, err := normalize(p)
	return err
}

func normalize(p Problem) (model, error) {
	var m model

	stack, err := chooseStack(p)
	if err != nil {
		return m, err
	}

	allowed := chooseAllowed(p)
	if len(allowed) > MaxInputBytes {
		return m, wrap(ErrTooManyInputBytes, "got %d, maximum %d", len(allowed), MaxInputBytes)
	}

	input, err := chooseInput(p, len(allowed))
	if err != nil {
		return m, err
	}
	if !input.Valid(StackSize) {
		return m, wrap(ErrInvalidInputSlot, "span [%d,%d) outside stack", input.Start, input.End())
	}
	if input.Len < len(allowed) {
		return m, wrap(ErrInvalidInputSlot, "slot length %d cannot hold %d controlled bytes", input.Len, len(allowed))
	}
	m.input = input

	retOffset, err := chooseReturnOffset(p)
	if err != nil {
		return m, err
	}
	retSpan := Span{Start: retOffset, Len: ReturnSlotSize}
	if !retSpan.Valid(StackSize) {
		return m, wrap(ErrInvalidReturnSlot, "span [%d,%d) outside stack", retSpan.Start, retSpan.End())
	}
	m.returnOffset = retOffset

	providedRet, err := chooseInitialRet(p)
	if err != nil {
		return m, err
	}
	stackRet := littleEndian(stack[retOffset : retOffset+ReturnSlotSize])
	switch {
	case providedRet != 0:
		m.initialRet = providedRet
	case stackRet != 0:
		// Be useful when a platform supplies a complete initial stack but
		// leaves the separate InitialRet field at its zero value.
		m.initialRet = stackRet
	default:
		m.initialRet = 0
	}
	putLittleEndian(stack[retOffset:retOffset+ReturnSlotSize], m.initialRet)

	cn, err := chooseCanary(p, stack)
	if err != nil {
		return m, err
	}
	if cn != nil {
		canarySpan := Span{Start: cn.offset, Len: CanarySize}
		if !canarySpan.Valid(StackSize) {
			return m, wrap(ErrInvalidCanary, "span [%d,%d) outside stack", canarySpan.Start, canarySpan.End())
		}
		if retSpan.Overlaps(canarySpan) {
			return m, wrap(ErrSlotsOverlap, "return [%d,%d) and canary [%d,%d)", retSpan.Start, retSpan.End(), canarySpan.Start, canarySpan.End())
		}
		copy(stack[cn.offset:cn.offset+CanarySize], cn.initial[:])
	}
	m.canary = cn

	if len(p.Writes) > MaxWrites {
		return m, wrap(ErrTooManyWrites, "got %d, maximum %d", len(p.Writes), MaxWrites)
	}
	for i, w := range p.Writes {
		offsetIndex, lengthIndex := w.inputIndices()
		if offsetIndex < 0 || offsetIndex >= len(allowed) {
			return m, wrap(ErrInvalidWriteIndex, "write %d offset index %d", i, offsetIndex)
		}
		if lengthIndex < 0 || lengthIndex >= len(allowed) {
			return m, wrap(ErrInvalidWriteIndex, "write %d length index %d", i, lengthIndex)
		}
	}
	m.writes = p.Writes
	m.allowed = allowed
	m.targets = chooseTargets(p)
	m.stack = stack
	return m, nil
}

func chooseStack(p Problem) ([StackSize]byte, error) {
	var zero [StackSize]byte
	var chosen [StackSize]byte
	for _, candidate := range [][StackSize]byte{p.InitialStack, p.Stack, p.StackImage} {
		if candidate == zero {
			continue
		}
		if chosen == zero {
			chosen = candidate
			continue
		}
		if chosen != candidate {
			return zero, wrap(ErrInconsistentValue, "InitialStack/Stack/StackImage differ")
		}
	}
	return chosen, nil
}

func chooseAllowed(p Problem) [][]byte {
	for _, candidate := range [][][]byte{
		p.Allowed,
		p.Values,
		p.InputValues,
		p.Alphabet,
		p.AllowedInputs,
	} {
		if len(candidate) != 0 {
			return candidate
		}
	}
	return nil
}

func chooseTargets(p Problem) map[uint32]struct{} {
	values := p.AllowedTargets
	if len(values) == 0 {
		values = p.Targets
	}
	// An empty target list intentionally means no allowed target.
	if len(values) == 0 {
		return map[uint32]struct{}{}
	}
	set := make(map[uint32]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

func chooseInput(p Problem, controlled int) (Span, error) {
	spans := make([]Span, 0, 4)
	for _, s := range []Span{p.InputSlot, p.Input, p.InputRange} {
		s := s.normalized()
		if s.Len != 0 || s.Start != 0 || s.Offset != 0 || s.Length != 0 {
			spans = append(spans, s)
		}
	}
	if p.InputLen != 0 || p.InputStart != 0 {
		spans = append(spans, Span{Start: p.InputStart, Len: p.InputLen})
	}
	if len(spans) == 0 {
		return Span{Start: 0, Len: controlled}, nil
	}
	chosen := spans[0]
	for _, s := range spans[1:] {
		if s.Len != 0 && chosen.Len == 0 {
			chosen = s
			continue
		}
		if s.Len != 0 && s.Start != chosen.Start {
			return Span{}, wrap(ErrInconsistentSpan, "input spans start at %d and %d", chosen.Start, s.Start)
		}
	}
	if chosen.Len == 0 {
		chosen.Len = controlled
	}
	return chosen.normalized(), nil
}

func chooseReturnOffset(p Problem) (int, error) {
	offsets := make([]int, 0, 4)
	if p.ReturnSlot.Start != 0 || p.ReturnSlot.Len != 0 || p.ReturnSlot.Offset != 0 || p.ReturnSlot.Length != 0 {
		s := p.ReturnSlot.normalized()
		if s.Len != 0 && s.Len != ReturnSlotSize {
			return 0, wrap(ErrInvalidReturnSlot, "length %d, want %d", s.Len, ReturnSlotSize)
		}
		offsets = append(offsets, s.Start)
	}
	if p.RetSlot.Start != 0 || p.RetSlot.Len != 0 || p.RetSlot.Offset != 0 || p.RetSlot.Length != 0 {
		s := p.RetSlot.normalized()
		if s.Len != 0 && s.Len != ReturnSlotSize {
			return 0, wrap(ErrInvalidReturnSlot, "length %d, want %d", s.Len, ReturnSlotSize)
		}
		offsets = append(offsets, s.Start)
	}
	if p.ReturnOffset != 0 {
		offsets = append(offsets, p.ReturnOffset)
	}
	if p.RetOffset != 0 {
		offsets = append(offsets, p.RetOffset)
	}
	if len(offsets) == 0 {
		return 0, nil
	}
	for _, off := range offsets[1:] {
		if off != offsets[0] {
			return 0, wrap(ErrInconsistentSpan, "return offsets %d and %d", offsets[0], off)
		}
	}
	return offsets[0], nil
}

func chooseInitialRet(p Problem) (uint32, error) {
	values := []uint32{p.InitialRet, p.InitialReturn, p.RetValue}
	var chosen uint32
	for _, v := range values {
		if v == 0 {
			continue
		}
		if chosen == 0 {
			chosen = v
			continue
		}
		if chosen != v {
			return 0, wrap(ErrInconsistentValue, "InitialRet/InitialReturn/RetValue differ")
		}
	}
	return chosen, nil
}

func chooseCanary(p Problem, stack [StackSize]byte) (*canaryModel, error) {
	c := p.Canary
	if c == nil && (p.CanaryEnabled || p.CanaryOffset != 0 || p.CanaryValue != ([CanarySize]byte{}) || p.CanaryInitial != ([CanarySize]byte{})) {
		c = &Canary{Offset: p.CanaryOffset, Value: p.CanaryValue, Initial: p.CanaryInitial}
	}
	if c == nil {
		return nil, nil
	}

	offset := c.Offset
	if offset == 0 && c.At != 0 {
		offset = c.At
	}
	value := c.Value
	if value == ([CanarySize]byte{}) && c.Initial != ([CanarySize]byte{}) {
		value = c.Initial
	}
	if value == ([CanarySize]byte{}) {
		// Derive from the initial stack when the separate value was omitted.
		if span := (Span{Start: offset, Len: CanarySize}); span.Valid(StackSize) {
			copy(value[:], stack[offset:offset+CanarySize])
		}
	}
	return &canaryModel{offset: offset, initial: value}, nil
}

func littleEndian(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func putLittleEndian(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

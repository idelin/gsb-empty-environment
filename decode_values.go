package sintcol

func decodeIndependent(header blockHeader, payload []byte) ([]int64, chainState, error) {
	state := chainState{}
	return decodeWithState(header, payload, header.base, header.init, false, state)
}

func decodeChained(header blockHeader, payload []byte, previous chainState) ([]int64, chainState, error) {
	if !previous.goodValue {
		return nil, previous, ErrBrokenChain
	}
	base := previous.value
	initDiff := previous.diff
	hasInit := previous.goodDiff
	return decodeWithState(header, payload, base, initDiff, hasInit, previous)
}

func decodeWithState(header blockHeader, payload []byte, base, initDiff int64, hasInit bool, previous chainState) ([]int64, chainState, error) {
	if header.count == 0 || header.count > uint64(capLimit()) {
		return nil, previous, ErrCorrupt
	}
	values := make([]int64, 0, int(header.count))
	pos := 0
	current := base
	diff := initDiff
	hasDiff := hasInit

	switch header.enc {
	case EncZigZag:
		for i := uint64(0); i < header.count; i++ {
			encoded, n, err := strictUvarint(payload[pos:])
			if err != nil {
				return nil, previous, ErrCorrupt
			}
			pos += n
			values = append(values, zigzagDecode(encoded))
		}
	case EncDelta:
		for i := uint64(0); i < header.count; i++ {
			encoded, n, err := strictUvarint(payload[pos:])
			if err != nil {
				return nil, previous, ErrCorrupt
			}
			pos += n
			step := zigzagDecode(encoded)
			next, ok := addChecked(current, step)
			if !ok {
				return nil, previous, ErrCorrupt
			}
			values = append(values, next)
			diff = step
			hasDiff = true
			current = next
		}
	case EncDeltaDelta:
		for i := uint64(0); i < header.count; i++ {
			encoded, n, err := strictUvarint(payload[pos:])
			if err != nil {
				return nil, previous, ErrCorrupt
			}
			pos += n
			change := zigzagDecode(encoded)
			if i == 0 {
				if change != 0 {
					return nil, previous, ErrCorrupt
				}
				values = append(values, current)
				continue
			}
			if !hasDiff {
				return nil, previous, ErrBrokenChain
			}
			nextDiff, ok := addChecked(diff, change)
			if !ok {
				return nil, previous, ErrCorrupt
			}
			next, ok := addChecked(current, nextDiff)
			if !ok {
				return nil, previous, ErrCorrupt
			}
			values = append(values, next)
			diff = nextDiff
			current = next
			hasDiff = true
		}
	default:
		return nil, previous, ErrCorrupt
	}
	if pos != len(payload) {
		return nil, previous, ErrCorrupt
	}

	state := chainState{
		value:     current,
		diff:      diff,
		hasValue:  true,
		hasDiff:   hasDiff,
		goodValue: true,
		goodDiff:  header.enc != EncDeltaDelta || hasDiff,
	}
	if header.enc == EncZigZag {
		state.value = values[len(values)-1]
		state.goodDiff = false
	}
	return values, state, nil
}

func capLimit() int64 {
	return int64(^uint(0) >> 1)
}

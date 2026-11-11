package shadow

// And computes bitwise a AND b and clears the internal carry bit.
func (machine *Machine) And(a, b Value) (Value, error) {
	if err := sameWidth(a, b); err != nil {
		return Value{}, err
	}
	bits := make([]bitTag, len(a.bits))
	for index := range a.bits {
		resultValue := a.bits[index].value & b.bits[index].value
		if resultValue == 1 {
			var source SourceSet
			switch {
			case !a.bits[index].constant:
				source = cloneSources(a.bits[index].sources)
			case !b.bits[index].constant:
				source = cloneSources(b.bits[index].sources)
			default:
				source = unionSources(a.bits[index].sources, b.bits[index].sources)
			}
			bits[index] = machine.tag(resultValue, source)
			continue
		}

		var source SourceSet
		switch {
		case a.bits[index].value == 0 && !a.bits[index].constant && !b.bits[index].constant:
			source = cloneSources(a.bits[index].sources)
		case b.bits[index].value == 0 && !b.bits[index].constant && !a.bits[index].constant:
			source = cloneSources(b.bits[index].sources)
		case a.bits[index].value == 0 && !a.bits[index].constant:
			source = cloneSources(a.bits[index].sources)
		case b.bits[index].value == 0 && !b.bits[index].constant:
			source = cloneSources(b.bits[index].sources)
		}
		bits[index] = machine.tag(0, emptyOrNil(source))
	}
	machine.carry = machine.Const(Width1, 0)
	return Value{width: a.width, bits: bits}, nil
}

// Or computes bitwise a OR b and clears the internal carry bit.
func (machine *Machine) Or(a, b Value) (Value, error) {
	if err := sameWidth(a, b); err != nil {
		return Value{}, err
	}
	bits := make([]bitTag, len(a.bits))
	for index := range a.bits {
		resultValue := a.bits[index].value | b.bits[index].value
		var source SourceSet
		if resultValue == 0 {
			switch {
			case !a.bits[index].constant:
				source = cloneSources(a.bits[index].sources)
			case !b.bits[index].constant:
				source = cloneSources(b.bits[index].sources)
			default:
				source = unionSources(a.bits[index].sources, b.bits[index].sources)
			}
		} else {
			switch {
			case a.bits[index].value == 1 && !a.bits[index].constant:
				source = cloneSources(a.bits[index].sources)
			case b.bits[index].value == 1 && !b.bits[index].constant:
				source = cloneSources(b.bits[index].sources)
			default:
				source = nil
			}
		}
		bits[index] = machine.tag(resultValue, emptyOrNil(source))
	}
	machine.carry = machine.Const(Width1, 0)
	return Value{width: a.width, bits: bits}, nil
}

// Xor computes bitwise a XOR b. Equal lineage at every bit, including the
// common xor-reg,reg clearing idiom, produces unsourced zero bits.
func (machine *Machine) Xor(a, b Value) (Value, error) {
	if err := sameWidth(a, b); err != nil {
		return Value{}, err
	}
	bits := make([]bitTag, len(a.bits))
	equalLineage := true
	for index := range a.bits {
		if a.bits[index].id != b.bits[index].id {
			equalLineage = false
			break
		}
	}
	for index := range a.bits {
		if equalLineage {
			bits[index] = machine.tag(0, nil)
			continue
		}
		bits[index] = machine.tagged(a.bits[index].value^b.bits[index].value, a.bits[index], b.bits[index])
	}
	machine.carry = machine.Const(Width1, 0)
	return Value{width: a.width, bits: bits}, nil
}

// Not computes bitwise NOT while preserving each bit's source set.
func (machine *Machine) Not(value Value) Value {
	if err := value.validate(); err != nil {
		panic(err)
	}
	bits := make([]bitTag, len(value.bits))
	for index, bit := range value.bits {
		bits[index] = machine.tag(bit.value^1, bit.sources)
	}
	return Value{width: value.width, bits: bits}
}

// Add computes a + b and returns the sum and its carry-out bit.
func (machine *Machine) Add(a, b Value) (Value, Value, error) {
	if err := sameWidth(a, b); err != nil {
		return Value{}, Value{}, err
	}
	sum, carry := machine.add(a, b, bitTag{}, false)
	machine.carry = Value{width: Width1, bits: []bitTag{carry}}
	return sum, Value{width: Width1, bits: []bitTag{carry}}, nil
}

// Adc computes a + b + carry and returns the sum and its carry-out bit.
func (machine *Machine) Adc(a, b Value) (Value, Value, error) {
	if err := sameWidth(a, b); err != nil {
		return Value{}, Value{}, err
	}
	sum, carry := machine.add(a, b, machine.carry.bits[0], true)
	machine.carry = Value{width: Width1, bits: []bitTag{carry}}
	return sum, Value{width: Width1, bits: []bitTag{carry}}, nil
}

// Sub computes a - b and returns the difference and its borrow bit.
func (machine *Machine) Sub(a, b Value) (Value, Value, error) {
	if err := sameWidth(a, b); err != nil {
		return Value{}, Value{}, err
	}
	difference, borrow := machine.sub(a, b, bitTag{value: 1}, false)
	machine.carry = Value{width: Width1, bits: []bitTag{borrow}}
	return difference, Value{width: Width1, bits: []bitTag{borrow}}, nil
}

// Sbb computes a - b - carry and returns the difference and its borrow bit.
func (machine *Machine) Sbb(a, b Value) (Value, Value, error) {
	if err := sameWidth(a, b); err != nil {
		return Value{}, Value{}, err
	}
	borrowIn := machine.notTag(machine.carry.bits[0])
	difference, borrow := machine.sub(a, b, borrowIn, true)
	machine.carry = Value{width: Width1, bits: []bitTag{borrow}}
	return difference, Value{width: Width1, bits: []bitTag{borrow}}, nil
}

// Cmp computes a - b, updates carry with the resulting borrow, and discards
// the arithmetic result.
func (machine *Machine) Cmp(a, b Value) (Value, error) {
	_, borrow, err := machine.Sub(a, b)
	return borrow, err
}

func (machine *Machine) add(a, b Value, carryIn bitTag, hasCarryIn bool) (Value, bitTag) {
	bits := make([]bitTag, len(a.bits))
	carry := carryIn
	hasCarry := hasCarryIn
	for index := range a.bits {
		left := a.bits[index]
		right := b.bits[index]
		resultValue := left.value ^ right.value ^ carry.value
		operands := make([]bitTag, 0, 3)
		operands = append(operands, left, right)
		if hasCarry {
			operands = append(operands, carry)
		}
		bits[index] = machine.tagged(resultValue, operands...)

		nextValue := uint8(0)
		if int(left.value)+int(right.value)+int(carry.value) >= 2 {
			nextValue = 1
		}
		if hasCarry {
			carry = machine.tag(nextValue, unionSources(left.sources, right.sources, carry.sources))
		} else {
			carry = machine.tag(nextValue, unionSources(left.sources, right.sources))
		}
		hasCarry = true
	}
	return Value{width: a.width, bits: bits}, carry
}

func (machine *Machine) sub(a, b Value, carryIn bitTag, hasCarryIn bool) (Value, bitTag) {
	invertedB := make([]bitTag, len(b.bits))
	for index, bit := range b.bits {
		invertedB[index] = machine.notTag(bit)
	}
	sum, carryOut := machine.add(a, Value{width: b.width, bits: invertedB}, carryIn, hasCarryIn)
	borrow := machine.notTag(carryOut)
	return sum, borrow
}

func (machine *Machine) notTag(tag bitTag) bitTag {
	return machine.tag(tag.value^1, tag.sources)
}

// ShiftLeft shifts value left by count and masks count to the operand width.
func (machine *Machine) ShiftLeft(value, count Value) Value {
	if err := value.validate(); err != nil {
		panic(err)
	}
	return machine.shift(value, count.Uint64()&shiftMask(value.width), true, false, count)
}

// ShiftRight logically shifts value right by count.
func (machine *Machine) ShiftRight(value, count Value) Value {
	if err := value.validate(); err != nil {
		panic(err)
	}
	return machine.shift(value, count.Uint64()&shiftMask(value.width), false, false, count)
}

// ShiftArithmeticRight arithmetically shifts value right by count.
func (machine *Machine) ShiftArithmeticRight(value, count Value) Value {
	if err := value.validate(); err != nil {
		panic(err)
	}
	return machine.shift(value, count.Uint64()&shiftMask(value.width), false, true, count)
}

// ShiftLeftImmediate shifts value left by an immediate count.
func (machine *Machine) ShiftLeftImmediate(value Value, count uint64) Value {
	if err := value.validate(); err != nil {
		panic(err)
	}
	return machine.shift(value, count&shiftMask(value.width), true, false, Value{})
}

// ShiftRightImmediate logically shifts value right by an immediate count.
func (machine *Machine) ShiftRightImmediate(value Value, count uint64) Value {
	if err := value.validate(); err != nil {
		panic(err)
	}
	return machine.shift(value, count&shiftMask(value.width), false, false, Value{})
}

// ShiftArithmeticRightImmediate arithmetically shifts value right by an immediate count.
func (machine *Machine) ShiftArithmeticRightImmediate(value Value, count uint64) Value {
	if err := value.validate(); err != nil {
		panic(err)
	}
	return machine.shift(value, count&shiftMask(value.width), false, true, Value{})
}

func (machine *Machine) shift(value Value, count uint64, left bool, arithmetic bool, countValue Value) Value {
	if count == 0 {
		bits := make([]bitTag, len(value.bits))
		for index, bit := range value.bits {
			if len(countValue.bits) > 0 {
				bits[index] = machine.tag(bit.value, unionSources(bit.sources, countValue.bitSourcesAll()))
			} else {
				bits[index] = bit
			}
		}
		return Value{width: value.width, bits: bits}
	}

	bits := make([]bitTag, len(value.bits))
	for index := range bits {
		var source bitTag
		hasSource := false
		if left {
			sourceIndex := index - int(count)
			if index >= int(count) && sourceIndex >= 0 {
				source = value.bits[sourceIndex]
				hasSource = true
			}
		} else {
			sourceIndex := index + int(count)
			if sourceIndex >= 0 && sourceIndex < len(value.bits) {
				source = value.bits[sourceIndex]
				hasSource = true
			} else if arithmetic {
				source = value.bits[len(value.bits)-1]
				hasSource = true
			}
		}

		if !hasSource {
			source = machine.tag(0, nil)
		}
		if len(countValue.bits) > 0 {
			bits[index] = machine.tag(source.value, unionSources(source.sources, countValue.bitSourcesAll()))
		} else {
			bits[index] = source
		}
	}
	return Value{width: value.width, bits: bits}
}

func (value Value) bitSourcesAll() SourceSet {
	sets := make([]SourceSet, 0, len(value.bits))
	for _, bit := range value.bits {
		sets = append(sets, bit.sources)
	}
	return unionSources(sets...)
}

func shiftMask(width Width) uint64 {
	switch width {
	case Width8:
		return 7
	case Width16:
		return 15
	default:
		return 31
	}
}

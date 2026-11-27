package writeproof

func decomposeInterval(start, end uint64, bits int) []BitCube {
	cubes := make([]BitCube, 0, 2*bits)
	for start < end {
		lowZero := trailingZero64(start)
		span := bits - lowZero
		if span < 0 {
			span = 0
		}
		size := uint64(1) << uint(span)
		for start+size > end || size == 0 || start&(size-1) != 0 {
			span--
			size >>= 1
		}
		cubes = append(cubes, BitCube{Base: start, FreeMask: size - 1})
		start += size
	}
	return cubes
}

func trailingZero64(v uint64) int {
	if v == 0 {
		return 64
	}
	n := 0
	for shift := 32; shift > 0; shift >>= 1 {
		if v&((uint64(1)<<uint(shift))-1) == 0 {
			n += shift
			v >>= uint(shift)
		}
	}
	return n
}

func decomposeMaskedDomain(start, end, mask, match uint64, logicalBits int, budget *Budget) (DomainDecomposition, error) {
	cubes := make([]DomainCube, 0, 2*logicalBits*logicalBits)
	for _, intervalCube := range decomposeInterval(start, end, logicalBits) {
		if err := chargeProof(budget); err != nil {
			return DomainDecomposition{}, err
		}
		for _, cube := range splitIntervalCube(intervalCube, mask, match, logicalBits) {
			cubes = append(cubes, cube)
		}
	}
	return DomainDecomposition{
		Start: start,
		End:   end,
		Mask:  mask,
		Match: match,
		Cubes: cubes,
	}, nil
}

func splitIntervalCube(cube BitCube, mask, match uint64, logicalBits int) []DomainCube {
	maskInCube := cube.FreeMask & mask
	fixedMaskInCube := (^cube.FreeMask) & addressMask(logicalBits) & mask
	if (cube.Base^match)&fixedMaskInCube != 0 {
		return nil
	}
	type state struct {
		fixedBase uint64
		freeMask uint64
	}
	pending := []state{{fixedBase: cube.Base &^ cube.FreeMask, freeMask: cube.FreeMask}}
	output := make([]DomainCube, 0, 2*logicalBits)
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		splitBit := highestMaskedFreeBit(current.freeMask, maskInCube, logicalBits)
		if splitBit < 0 {
			fixedMask := (^current.freeMask) & addressMask(logicalBits)
			output = append(output, DomainCube{
				Base:       current.fixedBase,
				FreeMask:   current.freeMask,
				FixedMask:  fixedMask,
				FixedValue: current.fixedBase & fixedMask,
			})
			continue
		}
		bitMask := uint64(1) << uint(splitBit)
		nextFreeMask := current.freeMask &^ bitMask
		selectedBase := current.fixedBase
		if match&bitMask != 0 {
			selectedBase |= bitMask
		}
		pending = append(pending, state{fixedBase: selectedBase, freeMask: nextFreeMask})
	}
	return output
}

func highestMaskedFreeBit(freeMask, mask uint64, logicalBits int) int {
	for bit := logicalBits - 1; bit >= 0; bit-- {
		bitMask := uint64(1) << uint(bit)
		if freeMask&mask&bitMask != 0 {
			return bit
		}
	}
	return -1
}

func decomposeProtectionDomain(p Protection, logicalBits int, budget *Budget) (DomainDecomposition, error) {
	return decomposeMaskedDomain(p.Start, p.End, 0, 0, logicalBits, budget)
}

func chargeProof(budget *Budget) error {
	if budget == nil || budget.Unlimited {
		return nil
	}
	if budget.UsedProofs >= budget.MaxProofs {
		return budgetError{}
	}
	budget.UsedProofs++
	return nil
}

func variablesFromDomain(cube DomainCube, logicalBits int) ([]uint64, uint64) {
	freeBits := make([]int, 0, logicalBits)
	for bit := 0; bit < logicalBits; bit++ {
		if cube.FreeMask&(uint64(1)<<uint(bit)) != 0 {
			freeBits = append(freeBits, bit)
		}
	}
	columns := make([]uint64, len(freeBits))
	for i, freeBit := range freeBits {
		columns[i] = uint64(1) << uint(freeBit)
	}
	return columns, cube.Base
}

func solutionToAddress(values []bool, offset int, freeMask, base uint64, logicalBits int) uint64 {
	address := base
	index := 0
	for bit := 0; bit < logicalBits; bit++ {
		if freeMask&(uint64(1)<<uint(bit)) == 0 {
			continue
		}
		if values[offset+index] {
			address |= uint64(1) << uint(bit)
		}
		index++
	}
	return address
}

package writeproof

type symbolicImage struct {
	origin      uint64
	variables   int
	columns     []uint64
	coefficient []uint64
	cube        DomainCube
	transform   AffineTransform
	logicalBits int
}

func buildSymbolicImage(cube DomainCube, transform AffineTransform, logicalBits, physicalBits int) symbolicImage {
	columns, base := variablesFromDomain(cube, logicalBits)
	origin := transform.Apply(base)
	coefficient := make([]uint64, physicalBits)
	for variable, column := range columns {
		for outputBit := 0; outputBit < physicalBits; outputBit++ {
			if transform.Rows[outputBit]&column != 0 {
				coefficient[outputBit] |= uint64(1) << uint(variable)
			}
		}
	}
	return symbolicImage{
		origin:      origin,
		variables:   len(columns),
		columns:     columns,
		coefficient: coefficient,
		cube:        cube,
		transform:   transform,
		logicalBits: logicalBits,
	}
}

func (s symbolicImage) highOrigin(lowBits int) uint64 {
	return s.origin >> uint(lowBits)
}

func (s symbolicImage) highCoefficients(lowBits int) []uint64 {
	return s.coefficient[lowBits:]
}

func (s symbolicImage) domainValue(values []bool, offset int, logicalBits int) uint64 {
	return solutionToAddress(values, offset, s.cube.FreeMask, s.cube.Base, logicalBits)
}

func (s symbolicImage) value(values []bool, offset int) uint64 {
	if s.variables == 0 {
		return s.origin
	}
	var output uint64
	for bit, coefficient := range s.coefficient {
		bitValue := int((s.origin>>uint(bit))&1) ^ parity(coefficient&bitsToWord(values, offset, s.variables))
		output |= uint64(bitValue) << uint(bit)
	}
	return output
}

func bitsToWord(values []bool, offset, count int) uint64 {
	var word uint64
	for i := 0; i < count; i++ {
		if values[offset+i] {
			word |= uint64(1) << uint(i)
		}
	}
	return word
}

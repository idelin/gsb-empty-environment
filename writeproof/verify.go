package writeproof

func VerifySolution(solution Solution) bool {
	if len(solution.Values) != solution.System.Variables {
		return false
	}
	for _, equation := range solution.System.Equations {
		if len(equation.Terms) != (solution.System.Variables+63)/64 {
			return false
		}
		value := false
		for bit := 0; bit < solution.System.Variables; bit++ {
			if hasBit(equation.Terms, bit) && solution.Values[bit] {
				value = !value
			}
		}
		if value != equation.Constant {
			return false
		}
	}
	return true
}

func VerifyUnsatCertificate(certificate UnsatCertificate) bool {
	g := newGaussian(certificate.System.Variables, nil)
	for _, equation := range certificate.System.Equations {
		err := g.add(linearEquation{terms: cloneWords(equation.Terms), constant: equation.Constant})
		if err != nil {
			if _, ok := err.(budgetError); ok {
				return false
			}
			return equationIsZero(linearEquation{terms: cloneWords(equation.Terms), constant: equation.Constant})
		}
	}
	err := g.add(linearEquation{terms: cloneWords(certificate.Contradiction.Terms), constant: certificate.Contradiction.Constant})
	if err == nil {
		return false
	}
	if len(certificate.PivotColumns) == 0 && len(g.pivot) != 0 {
		return false
	}
	return true
}

func VerifyIntervalDecomposition(decomposition IntervalDecomposition, bits int) bool {
	current := decomposition.Start
	limit := uint64(1) << uint(bits)
	for _, cube := range decomposition.Cubes {
		if cube.Base != current || cube.FreeMask == 0 || cube.Base&cube.FreeMask != 0 {
			return false
		}
		size := cube.FreeMask + 1
		if size == 0 || current+size > limit || current+size < current {
			return false
		}
		current += size
	}
	return current == decomposition.End
}

func VerifyDomainDecomposition(decomposition DomainDecomposition, logicalBits int) bool {
	for _, cube := range decomposition.Cubes {
		if cube.FixedMask|cube.FreeMask != addressMask(logicalBits) {
			return false
		}
		if cube.FixedMask&cube.FreeMask != 0 {
			return false
		}
		if cube.Base&cube.FixedMask != cube.FixedValue {
			return false
		}
		if cube.Base&cube.FreeMask != 0 {
			return false
		}
		if cube.FixedValue&cube.FreeMask != 0 {
			return false
		}
		if (cube.FixedValue^decomposition.Match)&decomposition.Mask != 0 {
			return false
		}
		if !cubeInsideInterval(cube, decomposition.Start, decomposition.End) {
			return false
		}
	}
	return true
}

func cubeInsideInterval(cube DomainCube, start, end uint64) bool {
	low := cube.Base
	high := low | cube.FreeMask
	return low >= start && high < end
}

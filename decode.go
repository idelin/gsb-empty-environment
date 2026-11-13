package netsec

func decodePolynomial(field primeField, shares []Share, k, t int) ([]int64, bool) {
	n := len(shares)
	variables := k + 2*t
	system := make([][]int64, n)

	for i, share := range shares {
		x := int64(share.X)
		y := int64(share.Y)
		maxPower := k + t - 1
		powers := make([]int64, maxPower+1)
		powers[0] = 1
		for j := 1; j <= maxPower; j++ {
			powers[j] = field.mul(powers[j-1], x)
		}

		row := make([]int64, variables+1)
		for j := 0; j < k+t; j++ {
			row[j] = powers[j]
		}
		for j := 0; j < t; j++ {
			row[k+t+j] = field.sub(0, field.mul(y, powers[j]))
		}
		row[variables] = field.mul(y, powers[t])
		system[i] = row
	}

	solution, ok := solveAffine(system, n, variables, field)
	if !ok {
		return nil, false
	}

	numerator := make([]int64, k+t)
	copy(numerator, solution[:k+t])
	denominator := make([]int64, t+1)
	copy(denominator, solution[k+t:])
	denominator[t] = 1

	quotient, remainder := dividePolynomials(numerator, denominator, field)
	if len(trimPolynomial(remainder)) != 0 {
		return nil, false
	}
	quotient = trimPolynomial(quotient)
	if len(quotient) > k {
		return nil, false
	}

	polynomial := make([]int64, k)
	copy(polynomial, quotient)
	return polynomial, true
}

func solveAffine(system [][]int64, rows, variables int, field primeField) ([]int64, bool) {
	pivots := make([]int, 0, variables)
	row := 0

	for column := 0; column < variables && row < rows; column++ {
		pivot := -1
		for candidate := row; candidate < rows; candidate++ {
			if system[candidate][column] != 0 {
				pivot = candidate
				break
			}
		}
		if pivot == -1 {
			continue
		}
		system[row], system[pivot] = system[pivot], system[row]

		scale := field.inv(system[row][column])
		for j := 0; j <= variables; j++ {
			system[row][j] = field.mul(system[row][j], scale)
		}

		for other := 0; other < rows; other++ {
			if other == row || system[other][column] == 0 {
				continue
			}
			factor := system[other][column]
			for j := 0; j <= variables; j++ {
				system[other][j] = field.sub(system[other][j], field.mul(factor, system[row][j]))
			}
		}

		pivots = append(pivots, column)
		row++
	}

	for candidate := row; candidate < rows; candidate++ {
		if system[candidate][variables] != 0 {
			return nil, false
		}
	}

	solution := make([]int64, variables)
	for i, column := range pivots {
		solution[column] = field.norm(system[i][variables])
	}
	return solution, true
}

func dividePolynomials(numerator, denominator []int64, field primeField) ([]int64, []int64) {
	numerator = trimPolynomial(numerator)
	denominator = trimPolynomial(denominator)
	if len(denominator) == 0 {
		panic("netsec: division by zero polynomial")
	}
	if len(numerator) < len(denominator) {
		return nil, numerator
	}

	remainder := append([]int64(nil), numerator...)
	quotient := make([]int64, len(numerator)-len(denominator)+1)
	leadingInverse := field.inv(denominator[len(denominator)-1])

	for degree := len(remainder) - 1; degree >= len(denominator)-1; degree-- {
		coefficient := field.mul(remainder[degree], leadingInverse)
		if coefficient == 0 {
			continue
		}
		shift := degree - (len(denominator) - 1)
		quotient[shift] = coefficient
		for j, divisorCoefficient := range denominator {
			index := shift + j
			remainder[index] = field.sub(remainder[index], field.mul(coefficient, divisorCoefficient))
		}
	}

	return trimPolynomial(quotient), trimPolynomial(remainder)
}

func trimPolynomial(coefficients []int64) []int64 {
	last := len(coefficients) - 1
	for last >= 0 && coefficients[last] == 0 {
		last--
	}
	return coefficients[:last+1]
}

func evalPolynomial(coefficients []int64, x, p int64) int64 {
	field := primeField{p: p}
	result := int64(0)
	for i := len(coefficients) - 1; i >= 0; i-- {
		result = field.add(field.mul(result, x), coefficients[i])
	}
	return result
}

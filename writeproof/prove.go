package writeproof

import "fmt"

type hitResult struct {
	address    uint64
	fullAddress uint64
	values     []bool
	rows       []linearEquation
	cubeIndex  int
}

type jointHit struct {
	address    uint64
	values     []bool
	rows       []linearEquation
	writeIndex int
	otherIndex int
}

func buildUnsatCertificate(variables int, rows []linearEquation, budget *Budget) (UnsatCertificate, error) {
	g := newGaussian(variables, budget)
	for _, row := range rows {
		if err := g.add(row); err != nil {
			if _, ok := err.(budgetError); ok {
				return UnsatCertificate{}, err
			}
			return UnsatCertificate{
				System:        publicSystem(variables, rows),
				PivotColumns: append([]int(nil), g.pivot...),
				Contradiction: publicEquation(row),
			}, nil
		}
	}
	return UnsatCertificate{}, fmt.Errorf("writeproof: expected unsatisfiable system")
}

func buildSolution(variables int, rows []linearEquation, values []bool) Solution {
	return Solution{
		System: publicSystem(variables, rows),
		Values: append([]bool(nil), values...),
	}
}

func intersectImagePhysical(image symbolicImage, cube BitCube, physicalBits int, budget *Budget) (*hitResult, *UnsatCertificate, error) {
	if image.variables == 0 {
		hit, certificate := constantPhysicalResult(image.origin, cube, physicalBits)
		return hit, certificate, nil
	}
	fixedMask := (^cube.FreeMask) & addressMask(physicalBits)
	fixedValue := cube.Base
	rows := make([]linearEquation, 0, physicalBits)
	for bit := 0; bit < physicalBits; bit++ {
		mask := uint64(1) << uint(bit)
		if fixedMask&mask != 0 {
			rows = append(rows, equalityBitEquation(
					image.variables,
					image.coefficient[bit],
					0,
					int((image.origin>>uint(bit))&1),
				int((fixedValue>>uint(bit))&1),
			))
		}
	}
	address, values, finalRows, err := minimizeAffine(
		image.variables,
		rows,
		image.coefficient,
		0,
		image.origin,
		physicalBits,
		budget,
	)
	if err == nil {
		finalRows, err = minimizeLogicalAddressVariables(image.variables, finalRows, image, 0, image.logicalBits, budget)
		if err != nil {
			return nil, nil, err
		}
			values, _, err = solveSystem(image.variables, finalRows, budget)
		if err != nil {
			return nil, nil, err
		}
		address = image.value(values, 0)
		return &hitResult{address: address, fullAddress: address, values: values, rows: finalRows}, nil, nil
	}
	if _, ok := err.(budgetError); ok {
		return nil, nil, err
	}
	certificate, err := buildUnsatCertificate(image.variables, rows, budget)
	if err != nil {
		return nil, nil, err
	}
	return nil, &certificate, nil
}

func constantPhysicalResult(address uint64, cube BitCube, bits int) (*hitResult, *UnsatCertificate) {
	fixedMask := (^cube.FreeMask) & addressMask(bits)
	if address&fixedMask != (cube.Base&^cube.FreeMask)&fixedMask {
		return nil, &UnsatCertificate{
			System:        LinearSystem{Variables: 0},
			Contradiction: LinearEquation{Terms: []uint64{}, Constant: true},
		}
	}
	return &hitResult{address: address, fullAddress: address, values: []bool{}, rows: []linearEquation{}}, nil
}

func intersectHighImagePhysical(image symbolicImage, lowBits, highBits int, cube BitCube, budget *Budget) (*hitResult, *UnsatCertificate, error) {
	if highBits == 0 {
		values, _, err := solveSystem(image.variables, nil, budget)
		if err != nil {
			return nil, nil, err
		}
		return &hitResult{address: 0, fullAddress: image.origin, values: values, rows: []linearEquation{}}, nil, nil
	}
	physicalBits := lowBits + highBits
	if image.variables == 0 {
		highOrigin := image.origin >> uint(lowBits)
		hit, certificate := constantPhysicalResult(highOrigin, cube, highBits)
		if hit != nil {
			hit.fullAddress = image.origin
		}
		return hit, certificate, nil
	}
	fixedMask := (^cube.FreeMask) & addressMask(highBits)
	fixedValue := cube.Base
	rows := make([]linearEquation, 0, highBits)
	for bit := 0; bit < highBits; bit++ {
		mask := uint64(1) << uint(bit)
		if fixedMask&mask != 0 {
			outputBit := lowBits + bit
				rows = append(rows, equalityBitEquation(
					image.variables,
					image.coefficient[outputBit],
					0,
					int((image.origin>>uint(outputBit))&1),
				int((fixedValue>>uint(bit))&1),
			))
	}
	}
	highAddress, _, finalRows, err := minimizeAffine(
		image.variables,
		rows,
		image.highCoefficients(lowBits),
		0,
		image.highOrigin(lowBits),
		highBits,
		budget,
	)
	if err == nil {
		lowAddress, lowValues, lowRows, lowErr := minimizeAffine(
			image.variables,
			finalRows,
			image.coefficient,
			0,
			image.origin,
			physicalBits,
			budget,
		)
		if lowErr != nil {
			return nil, nil, lowErr
		}
		if lowAddress>>uint(lowBits) != highAddress {
			return nil, nil, fmt.Errorf("writeproof: internal erase block minimization error: high=%d full=%d", highAddress, lowAddress)
		}
		lowRows, lowErr = minimizeLogicalAddressVariables(image.variables, lowRows, image, 0, image.logicalBits, budget)
		if lowErr != nil {
			return nil, nil, lowErr
		}
		lowValues, _, lowErr = solveSystem(image.variables, lowRows, budget)
		if lowErr != nil {
			return nil, nil, lowErr
		}
		lowAddress = image.value(lowValues, 0)
		return &hitResult{address: highAddress, fullAddress: lowAddress, values: lowValues, rows: lowRows}, nil, nil
	}
	if _, ok := err.(budgetError); ok {
		return nil, nil, err
	}
	certificate, err := buildUnsatCertificate(image.variables, rows, budget)
	if err != nil {
		return nil, nil, err
	}
	return nil, &certificate, nil
}

func intersectJointImages(write, protected symbolicImage, physicalBits int, budget *Budget) (*jointHit, *UnsatCertificate, error) {
	variables := write.variables + protected.variables
	rows := make([]linearEquation, 0, physicalBits)
	for bit := 0; bit < physicalBits; bit++ {
		writeRow := outputBitEquation(
			variables,
			write.coefficient[bit],
			0,
			int((write.origin>>uint(bit))&1),
			0,
		)
		protectedRow := outputBitEquation(
			variables,
			protected.coefficient[bit],
			write.variables,
			int((protected.origin>>uint(bit))&1),
			0,
		)
		xorWords(writeRow.terms, protectedRow.terms)
		writeRow.constant = writeRow.constant != protectedRow.constant
		rows = append(rows, writeRow)
	}
	address, values, finalRows, err := minimizeJointOutput(variables, rows, write, protected, physicalBits, budget)
	if err == nil {
		return &jointHit{address: address, values: values, rows: finalRows}, nil, nil
	}
	if _, ok := err.(budgetError); ok {
		return nil, nil, err
	}
	certificate, err := buildUnsatCertificate(variables, rows, budget)
	if err != nil {
		return nil, nil, err
	}
	return nil, &certificate, nil
}

func intersectHighJointImages(write, protected symbolicImage, lowBits, physicalBits int, budget *Budget) (*jointHit, *UnsatCertificate, error) {
	variables := write.variables + protected.variables
	highBits := physicalBits - lowBits
	if highBits == 0 {
		rows := make([]linearEquation, 0, physicalBits)
		for bit := 0; bit < physicalBits; bit++ {
			writeRow := outputBitEquation(variables, write.coefficient[bit], 0, int((write.origin>>uint(bit))&1), 0)
			protectedRow := outputBitEquation(variables, protected.coefficient[bit], write.variables, int((protected.origin>>uint(bit))&1), 0)
			xorWords(writeRow.terms, protectedRow.terms)
			writeRow.constant = writeRow.constant != protectedRow.constant
			rows = append(rows, writeRow)
		}
		_, values, finalRows, err := minimizeJointOutput(variables, rows, write, protected, physicalBits, budget)
		if err == nil {
			return &jointHit{address: 0, values: values, rows: finalRows}, nil, nil
		}
		if _, ok := err.(budgetError); ok {
			return nil, nil, err
		}
		certificate, err := buildUnsatCertificate(variables, rows, budget)
		if err != nil {
			return nil, nil, err
		}
		return nil, &certificate, nil
	}
	rows := make([]linearEquation, 0, highBits)
	for bit := lowBits; bit < physicalBits; bit++ {
		writeRow := outputBitEquation(
			variables,
			write.coefficient[bit],
			0,
			int((write.origin>>uint(bit))&1),
			0,
		)
		protectedRow := outputBitEquation(
			variables,
			protected.coefficient[bit],
			write.variables,
			int((protected.origin>>uint(bit))&1),
			0,
		)
		xorWords(writeRow.terms, protectedRow.terms)
		writeRow.constant = writeRow.constant != protectedRow.constant
		rows = append(rows, writeRow)
	}
	highAddress, values, finalRows, err := minimizeHighJointOutput(variables, rows, write, protected, lowBits, physicalBits, budget)
	if err == nil {
		return &jointHit{address: highAddress, values: values, rows: finalRows}, nil, nil
	}
	if _, ok := err.(budgetError); ok {
		return nil, nil, err
	}
	certificate, err := buildUnsatCertificate(variables, rows, budget)
	if err != nil {
		return nil, nil, err
	}
	return nil, &certificate, nil
}

func minimizeJointOutput(variables int, rows []linearEquation, write, protected symbolicImage, physicalBits int, budget *Budget) (uint64, []bool, []linearEquation, error) {
	address, values, finalRows, err := minimizeAffine(variables, rows, write.coefficient, 0, write.origin, physicalBits, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	finalRows, err = minimizeLogicalAddressVariables(variables, finalRows, write, 0, write.logicalBits, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	finalRows, err = minimizeLogicalAddressVariables(variables, finalRows, protected, write.variables, protected.logicalBits, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	values, _, err = solveSystem(variables, finalRows, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	address = write.value(values, 0)
	return address, values, finalRows, nil
}

func minimizeHighJointOutput(variables int, rows []linearEquation, write, protected symbolicImage, lowBits, physicalBits int, budget *Budget) (uint64, []bool, []linearEquation, error) {
	highBits := physicalBits - lowBits
	address, values, finalRows, err := minimizeAffine(variables, rows, write.highCoefficients(lowBits), 0, write.highOrigin(lowBits), highBits, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	finalRows, err = minimizeLogicalAddressVariables(variables, finalRows, protected, write.variables, protected.logicalBits, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	finalRows, err = minimizeLogicalAddressVariables(variables, finalRows, write, 0, write.logicalBits, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	values, _, err = solveSystem(variables, finalRows, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	address = write.value(values, 0) >> uint(lowBits)
	return address, values, finalRows, nil
}

func minimizeLogicalAddressVariables(variables int, rows []linearEquation, image symbolicImage, offset, logicalBits int, budget *Budget) ([]linearEquation, error) {
	finalRows := append([]linearEquation{}, rows...)
	var err error
	for logicalBit := logicalBits - 1; logicalBit >= 0; logicalBit-- {
		if image.cube.FreeMask&(uint64(1)<<uint(logicalBit)) == 0 {
			continue
		}
		variableIndex := offset + variableIndexForBit(image.cube.FreeMask, logicalBit)
		finalRows, err = constrainDomainBit(variables, finalRows, variableIndex, budget)
		if err != nil {
			return nil, err
		}
	}
	return finalRows, nil
}

func minimizeJointVariables(variables int, rows []linearEquation, write, protected symbolicImage, budget *Budget) ([]bool, []linearEquation, error) {
	finalRows := append([]linearEquation{}, rows...)
	var err error
	for logicalBit := MaxLogicalBits - 1; logicalBit >= 0; logicalBit-- {
		if write.cube.FreeMask&(uint64(1)<<uint(logicalBit)) == 0 {
			continue
		}
		variableIndex := variableIndexForBit(write.cube.FreeMask, logicalBit)
		finalRows, err = constrainDomainBit(variables, finalRows, variableIndex, budget)
		if err != nil {
			return nil, nil, err
		}
	}
	for logicalBit := MaxLogicalBits - 1; logicalBit >= 0; logicalBit-- {
		if protected.cube.FreeMask&(uint64(1)<<uint(logicalBit)) == 0 {
			continue
		}
		variableIndex := write.variables + variableIndexForBit(protected.cube.FreeMask, logicalBit)
		finalRows, err = constrainDomainBit(variables, finalRows, variableIndex, budget)
		if err != nil {
			return nil, nil, err
		}
	}
	values, _, err := solveSystem(variables, finalRows, budget)
	return values, finalRows, err
}

func variableIndexForBit(freeMask uint64, logicalBit int) int {
	index := 0
	for bit := 0; bit < logicalBit; bit++ {
		if freeMask&(uint64(1)<<uint(bit)) != 0 {
			index++
		}
	}
	return index
}

func constrainDomainBit(variables int, rows []linearEquation, variableIndex int, budget *Budget) ([]linearEquation, error) {
	row := bitEquation(variables, variableIndex, false)
	candidate := append(append([]linearEquation{}, rows...), row)
	if _, _, err := solveSystem(variables, candidate, budget); err == nil {
		return candidate, nil
	} else if _, ok := err.(budgetError); ok {
		return nil, err
	}
	row.constant = true
	return append(rows, row), nil
}

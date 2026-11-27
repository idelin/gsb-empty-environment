package writeproof

func Prove(req Request) (*Report, error) {
	physicalBits := req.PhysicalBits
	if physicalBits == 0 {
		physicalBits = MaxLogicalBits
	}
	logicalBits := req.logicalBits
	if logicalBits == 0 {
		logicalBits = MaxLogicalBits
	}
	if err := validateRequest(req, logicalBits, physicalBits); err != nil {
		return nil, err
	}
	budget := defaultBudget(req.Budget)
	report := &Report{
		Status: StatusSafe,
		Protections: make([]ProtectionProof, 0, len(req.Protections)),
		Actions: make([]ActionProof, 0, len(req.Writes)),
		Budget: budget,
	}
	protectionImages := make([][]symbolicImage, len(req.Protections))
	for index, protection := range req.Protections {
		proof := ProtectionProof{Index: index}
		switch protection.Kind {
		case ProtectionLogical:
			domain, err := decomposeProtectionDomain(protection, logicalBits, &budget)
			if err != nil {
				return unknownReport(report, budget, err), nil
			}
			proof.Logical = &domain
			images := make([]symbolicImage, 0, len(domain.Cubes))
			for _, cube := range domain.Cubes {
				images = append(images, buildSymbolicImage(cube, protection.Transform, logicalBits, physicalBits))
			}
			protectionImages[index] = images
		case ProtectionPhysical:
			decomposition := IntervalDecomposition{
				Start: protection.Start,
				End:   protection.End,
				Cubes: decomposeInterval(protection.Start, protection.End, physicalBits),
			}
			proof.Physical = &decomposition
		}
		report.Protections = append(report.Protections, proof)
	}

	blockBits := trailingZero64(req.EraseBlockSize)
	for actionIndex, write := range req.Writes {
		domain, err := decomposeMaskedDomain(write.Start, write.End, write.Mask, write.Match, logicalBits, &budget)
		if err != nil {
			return unknownReport(report, budget, err), nil
		}
		action := ActionProof{Index: actionIndex, Domain: domain}
		if len(domain.Cubes) == 0 {
			report.Actions = append(report.Actions, action)
			continue
		}
		images := make([]symbolicImage, 0, len(domain.Cubes))
		for _, cube := range domain.Cubes {
			images = append(images, buildSymbolicImage(cube, write.Transform, logicalBits, physicalBits))
		}

		conflict, proofs, err := checkErase(actionIndex, images, req.Protections, protectionImages, logicalBits, blockBits, physicalBits, &budget)
		if err != nil {
			return unknownReport(report, budget, err), nil
		}
		action.EraseProofs = proofs
		if conflict != nil {
			report.Status = StatusConflict
			report.Conflict = conflict
			report.Actions = append(report.Actions, action)
			report.Budget = budget
			return report, nil
		}

		conflict, proofs, err = checkWrite(actionIndex, images, req.Protections, protectionImages, logicalBits, physicalBits, &budget)
		if err != nil {
			return unknownReport(report, budget, err), nil
		}
		action.WriteProofs = proofs
		if conflict != nil {
			report.Status = StatusConflict
			report.Conflict = conflict
			report.Actions = append(report.Actions, action)
			report.Budget = budget
			return report, nil
		}
		report.Actions = append(report.Actions, action)
	}
	report.Budget = budget
	return report, nil
}

func unknownReport(report *Report, budget Budget, err error) *Report {
	reason := "budget exhausted"
	if _, ok := err.(budgetError); !ok {
		reason = err.Error()
	}
	report.Status = StatusUnknown
	report.Reason = reason
	report.Budget = budget
	return report
}

func checkErase(actionIndex int, images []symbolicImage, protections []Protection, protectedImages [][]symbolicImage, logicalBits, blockBits, physicalBits int, budget *Budget) (*Conflict, []DisjointProof, error) {
	proofs := make([]DisjointProof, 0)
	var best *Conflict
	for imageIndex, image := range images {
		for protectionIndex, protection := range protections {
			switch protection.Kind {
			case ProtectionPhysical:
				blockStart := protection.Start >> uint(blockBits)
				blockEnd := (protection.End - 1) >> uint(blockBits) + 1
				for cubeIndex, cube := range decomposeInterval(blockStart, blockEnd, physicalBits-blockBits) {
					hit, certificate, err := intersectHighImagePhysical(image, blockBits, physicalBits-blockBits, cube, budget)
					if err != nil {
						return nil, nil, err
					}
					if hit != nil {
						blockAddress := hit.address << uint(blockBits)
						touchAddress := hit.fullAddress
						conflict := buildErasePhysicalConflict(actionIndex, imageIndex, protectionIndex, cubeIndex, blockAddress, touchAddress, image, hit, logicalBits, physicalBits)
						best = chooseConflict(best, conflict)
					} else {
						proofs = append(proofs, DisjointProof{
							Phase: PhaseErase,
							ProtectionKind: ProtectionPhysical,
							WriteCubeIndex: imageIndex,
							ProtectionIndex: protectionIndex,
							ProtectionCubeIndex: cubeIndex,
							Certificate: *certificate,
						})
					}
				}
			case ProtectionLogical:
				for otherIndex, protected := range protectedImages[protectionIndex] {
					hit, certificate, err := intersectHighJointImages(image, protected, blockBits, physicalBits, budget)
					if err != nil {
						return nil, nil, err
					}
					if hit != nil {
						blockAddress := hit.address << uint(blockBits)
						touchAddress := image.value(hit.values, 0)
						conflict := buildEraseLogicalConflict(actionIndex, imageIndex, protectionIndex, otherIndex, blockAddress, touchAddress, image, protected, hit, logicalBits)
						best = chooseConflict(best, conflict)
					} else {
						proofs = append(proofs, DisjointProof{
							Phase: PhaseErase,
							ProtectionKind: ProtectionLogical,
							WriteCubeIndex: imageIndex,
							ProtectionIndex: protectionIndex,
							ProtectionCubeIndex: otherIndex,
							Certificate: *certificate,
						})
					}
				}
			}
		}
	}
	return best, proofs, nil
}

func checkWrite(actionIndex int, images []symbolicImage, protections []Protection, protectedImages [][]symbolicImage, logicalBits, physicalBits int, budget *Budget) (*Conflict, []DisjointProof, error) {
	proofs := make([]DisjointProof, 0)
	var best *Conflict
	for imageIndex, image := range images {
		for protectionIndex, protection := range protections {
			switch protection.Kind {
			case ProtectionPhysical:
				for cubeIndex, cube := range decomposeInterval(protection.Start, protection.End, physicalBits) {
					hit, certificate, err := intersectImagePhysical(image, cube, physicalBits, budget)
					if err != nil {
						return nil, nil, err
					}
					if hit != nil {
						conflict := buildWritePhysicalConflict(actionIndex, imageIndex, protectionIndex, cubeIndex, image, hit, logicalBits)
						best = chooseConflict(best, conflict)
					} else {
						proofs = append(proofs, DisjointProof{
							Phase: PhaseWrite,
							ProtectionKind: ProtectionPhysical,
							WriteCubeIndex: imageIndex,
							ProtectionIndex: protectionIndex,
							ProtectionCubeIndex: cubeIndex,
							Certificate: *certificate,
						})
					}
				}
			case ProtectionLogical:
				for otherIndex, protected := range protectedImages[protectionIndex] {
					hit, certificate, err := intersectJointImages(image, protected, physicalBits, budget)
					if err != nil {
						return nil, nil, err
					}
					if hit != nil {
						conflict := buildWriteLogicalConflict(actionIndex, imageIndex, protectionIndex, otherIndex, image, protected, hit, logicalBits)
						best = chooseConflict(best, conflict)
					} else {
						proofs = append(proofs, DisjointProof{
							Phase: PhaseWrite,
							ProtectionKind: ProtectionLogical,
							WriteCubeIndex: imageIndex,
							ProtectionIndex: protectionIndex,
							ProtectionCubeIndex: otherIndex,
							Certificate: *certificate,
						})
					}
				}
			}
		}
	}
	return best, proofs, nil
}

func chooseConflict(current, candidate *Conflict) *Conflict {
	if current == nil {
		return candidate
	}
	if candidate.PhysicalAddress != current.PhysicalAddress {
		if candidate.PhysicalAddress < current.PhysicalAddress {
			return candidate
		}
		return current
	}
	if candidate.WritePreimage != nil && current.WritePreimage != nil &&
		candidate.WritePreimage.LogicalAddress != current.WritePreimage.LogicalAddress {
		if candidate.WritePreimage.LogicalAddress < current.WritePreimage.LogicalAddress {
			return candidate
		}
		return current
	}
	if candidate.ProtectedPreimage != nil && current.ProtectedPreimage != nil &&
		candidate.ProtectedPreimage.LogicalAddress < current.ProtectedPreimage.LogicalAddress {
		return candidate
	}
	return current
}

package writeproof

func buildWritePhysicalConflict(actionIndex, imageIndex, protectionIndex, cubeIndex int, image symbolicImage, hit *hitResult, logicalBits int) *Conflict {
	logicalAddress := image.domainValue(hit.values, 0, logicalBits)
	return &Conflict{
		ActionIndex: actionIndex,
		Phase: PhaseWrite,
		ProtectionIndex: protectionIndex,
		WriteCubeIndex: imageIndex,
		ProtectionCubeIndex: cubeIndex,
		PhysicalAddress: hit.address,
		WritePreimage: &Preimage{
			CubeIndex: imageIndex,
			LogicalAddress: logicalAddress,
			PhysicalAddress: hit.address,
			Solution: buildSolution(image.variables, hit.rows, hit.values),
		},
	}
}

func buildErasePhysicalConflict(actionIndex, imageIndex, protectionIndex, cubeIndex int, blockAddress, touchAddress uint64, image symbolicImage, hit *hitResult, logicalBits, physicalBits int) *Conflict {
	logicalAddress := image.domainValue(hit.values, 0, logicalBits)
	return &Conflict{
		ActionIndex: actionIndex,
		Phase: PhaseErase,
		ProtectionIndex: protectionIndex,
		WriteCubeIndex: imageIndex,
		ProtectionCubeIndex: cubeIndex,
		PhysicalAddress: blockAddress,
		WritePreimage: &Preimage{
			CubeIndex: imageIndex,
			LogicalAddress: logicalAddress,
			PhysicalAddress: touchAddress,
			Solution: buildSolution(image.variables, hit.rows, hit.values),
		},
	}
}

func buildWriteLogicalConflict(actionIndex, writeIndex, protectionIndex, otherIndex int, write, protected symbolicImage, hit *jointHit, logicalBits int) *Conflict {
	writeAddress := write.domainValue(hit.values, 0, logicalBits)
	protectedAddress := protected.domainValue(hit.values, write.variables, logicalBits)
	return &Conflict{
		ActionIndex: actionIndex,
		Phase: PhaseWrite,
		ProtectionIndex: protectionIndex,
		WriteCubeIndex: writeIndex,
		ProtectionCubeIndex: otherIndex,
		PhysicalAddress: hit.address,
		WritePreimage: &Preimage{
			CubeIndex: writeIndex,
			LogicalAddress: writeAddress,
			PhysicalAddress: hit.address,
			Solution: buildSolution(len(hit.values), hit.rows, hit.values),
		},
		ProtectedPreimage: &Preimage{
			CubeIndex: otherIndex,
			LogicalAddress: protectedAddress,
			PhysicalAddress: hit.address,
			Solution: buildSolution(len(hit.values), hit.rows, hit.values),
		},
	}
}

func buildEraseLogicalConflict(actionIndex, writeIndex, protectionIndex, otherIndex int, blockAddress, touchAddress uint64, write, protected symbolicImage, hit *jointHit, logicalBits int) *Conflict {
	writeAddress := write.domainValue(hit.values, 0, logicalBits)
	protectedAddress := protected.domainValue(hit.values, write.variables, logicalBits)
	protectedTouch := protected.value(hit.values, write.variables)
	return &Conflict{
		ActionIndex: actionIndex,
		Phase: PhaseErase,
		ProtectionIndex: protectionIndex,
		WriteCubeIndex: writeIndex,
		ProtectionCubeIndex: otherIndex,
		PhysicalAddress: blockAddress,
		WritePreimage: &Preimage{
			CubeIndex: writeIndex,
			LogicalAddress: writeAddress,
			PhysicalAddress: touchAddress,
			Solution: buildSolution(len(hit.values), hit.rows, hit.values),
		},
		ProtectedPreimage: &Preimage{
			CubeIndex: otherIndex,
			LogicalAddress: protectedAddress,
			PhysicalAddress: protectedTouch,
			Solution: buildSolution(len(hit.values), hit.rows, hit.values),
		},
	}
}

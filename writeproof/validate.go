package writeproof

func validateRequest(req Request, logicalBits, physicalBits int) error {
	if physicalBits < 1 || physicalBits > MaxPhysicalBits {
		return ErrInvalidWidth
	}
	if logicalBits < 1 || logicalBits > MaxLogicalBits {
		return ErrInvalidWidth
	}
	logicalMask := addressMask(logicalBits)
	physicalMask := addressMask(physicalBits)
	if req.EraseBlockSize == 0 || req.EraseBlockSize&(req.EraseBlockSize-1) != 0 {
		return ErrInvalidBlockSize
	}
	if req.EraseBlockSize > uint64(1)<<uint(physicalBits) {
		return ErrInvalidBlockSize
	}
	for index, write := range req.Writes {
		if write.Start > write.End || write.End > logicalMask {
			return ErrInvalidRange
		}
		if write.Mask > logicalMask || write.Match > logicalMask || write.Match&^write.Mask != 0 {
			return ErrInvalidMask
		}
		if err := validateTransform(write.Transform, logicalBits, physicalBits); err != nil {
			_ = index
			return err
		}
	}
	for _, protection := range req.Protections {
		if protection.Kind != ProtectionLogical && protection.Kind != ProtectionPhysical {
			return ErrUnknownProtection
		}
		mask := physicalMask
		if protection.Kind == ProtectionLogical {
			mask = logicalMask
		}
		if protection.Start > protection.End || protection.End > mask {
			return ErrInvalidRange
		}
		if err := validateTransform(protection.Transform, logicalBits, physicalBits); err != nil {
			return err
		}
	}
	return nil
}

func validateTransform(transform AffineTransform, logicalBits, physicalBits int) error {
	for row := physicalBits; row < MaxLogicalBits; row++ {
		if transform.Rows[row] != 0 {
			return ErrInvalidTransform
		}
	}
	for row := 0; row < physicalBits; row++ {
		if transform.Rows[row] > addressMask(logicalBits) {
			return ErrInvalidTransform
		}
	}
	if transform.Bias > addressMask(physicalBits) {
		return ErrInvalidTransform
	}
	return nil
}

func addressMask(bits int) uint64 {
	if bits >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<uint(bits) - 1
}

func defaultBudget(budget Budget) Budget {
	if budget.Unlimited || budget.MaxSystemOps != 0 || budget.MaxProofs != 0 {
		return budget
	}
	budget.MaxSystemOps = 200000
	budget.MaxProofs = 20000
	return budget
}

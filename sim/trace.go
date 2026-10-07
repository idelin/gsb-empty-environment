package sim

import "fmt"

// Trace renders any (final) product as an exact linear combination of its
// original sources, e.g. "m1/0 (qty 7) = 5*oreA + 2*oreB".
func (s *Simulator) Trace(entityID string) (string, error) {
	e, ok := s.Entities[entityID]
	if !ok {
		return "", fmt.Errorf("trace: unknown entity %q", entityID)
	}
	return fmt.Sprintf("%s (qty %d) = %s", e.ID, e.Quantity, e.Sources.String()), nil
}

// VerifyTrace checks the traceability invariant for an entity: the
// coefficients of its source linear combination must sum exactly to its
// integer quantity.
func (s *Simulator) VerifyTrace(entityID string) error {
	e, ok := s.Entities[entityID]
	if !ok {
		return fmt.Errorf("verify trace: unknown entity %q", entityID)
	}
	return e.checkInvariant()
}

// TotalLoss returns the total quantity explicitly recorded as lost.
func (s *Simulator) TotalLoss() int64 {
	var total int64
	for _, l := range s.Losses {
		total += l.Amount
	}
	return total
}

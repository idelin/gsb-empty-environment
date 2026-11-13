package netsec

import "fmt"

// ExposureError reports that one of the module's recursive groups
// canonicalizes to the host's private exposed type, so instantiating the
// module would let it ref.cast the exposed reference and read private
// fields.
type ExposureError struct {
	// GroupIndex is the index of the offending group in the module's
	// rec group list.
	GroupIndex int
}

func (e *ExposureError) Error() string {
	return fmt.Sprintf("netsec: module rec group %d is equivalent to the host private type; instantiation rejected", e.GroupIndex)
}

// CheckModule guards module instantiation. host is the recursive group of
// the private type the host exposes to the module; module lists the
// module's recursive groups in declaration order. It returns an
// *ExposureError naming the first module group equivalent to the host
// group, an ordinary error if either side is malformed, and nil if the
// module declares no equivalent group.
func CheckModule(host RecGroup, module []RecGroup) error {
	if err := host.Validate(); err != nil {
		return fmt.Errorf("netsec: host group: %w", err)
	}
	for i, g := range module {
		if err := g.Validate(); err != nil {
			return fmt.Errorf("netsec: module group %d: %w", i, err)
		}
		if Equivalent(host, g) {
			return &ExposureError{GroupIndex: i}
		}
	}
	return nil
}

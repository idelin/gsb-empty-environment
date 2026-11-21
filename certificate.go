package recovery

import (
	"fmt"
	"strings"
)

// Mismatch is one failed certificate/observation comparison.
type Mismatch struct {
	LogicalVolume  int
	PhysicalVolume PhysicalVolumeID
	Field          string
	Expected       string
	Actual         string
}

// VerificationError reports all mismatches found.  No part of the mount group
// is considered valid when this error is returned.
type VerificationError struct {
	Mismatches []Mismatch
}

func (e *VerificationError) Error() string {
	if len(e.Mismatches) == 0 {
		return ErrVerificationFailed.Error()
	}
	var b strings.Builder
	b.WriteString(ErrVerificationFailed.Error())
	for _, m := range e.Mismatches {
		fmt.Fprintf(&b, "; volume %d physical volume %q %s: expected %q, observed %q",
			m.LogicalVolume, m.PhysicalVolume, m.Field, m.Expected, m.Actual)
	}
	return b.String()
}

func (e *VerificationError) Unwrap() error { return ErrVerificationFailed }

// Verify re-reads all selected physical volumes, generations and snapshot
// digests from observations.  It returns nil only when every choice matches.
// Observations for unselected physical volumes are ignored.
func (c *Certificate) Verify(observations map[PhysicalVolumeID]Observation) error {
	if c == nil {
		return &VerificationError{}
	}
	var mismatches []Mismatch
	add := func(ch Choice, field, want, got string) {
		mismatches = append(mismatches, Mismatch{
			LogicalVolume:  ch.LogicalVolume,
			PhysicalVolume: ch.PhysicalVolume,
			Field:          field,
			Expected:       want,
			Actual:         got,
		})
	}
	for _, ch := range c.Choices {
		obs, ok := observations[ch.PhysicalVolume]
		if !ok {
			add(ch, "physical volume", string(ch.PhysicalVolume), "<missing>")
			continue
		}
		if obs.PhysicalVolume != "" && obs.PhysicalVolume != ch.PhysicalVolume {
			add(ch, "physical volume", string(ch.PhysicalVolume), string(obs.PhysicalVolume))
		}
		if obs.VolumeGeneration != ch.VolumeGeneration {
			add(ch, "generation",
				fmt.Sprintf("%d", ch.VolumeGeneration), fmt.Sprintf("%d", obs.VolumeGeneration))
		}
		if obs.Node != ch.Node {
			add(ch, "node", string(ch.Node), string(obs.Node))
		}
		if obs.SnapshotGeneration != ch.SnapshotGeneration {
			add(ch, "snapshot generation",
				fmt.Sprintf("%d", ch.SnapshotGeneration), fmt.Sprintf("%d", obs.SnapshotGeneration))
		}
		if obs.SnapshotDigest != ch.SnapshotDigest {
			add(ch, "snapshot digest", ch.SnapshotDigest, obs.SnapshotDigest)
		}
	}
	if len(mismatches) > 0 {
		return &VerificationError{Mismatches: mismatches}
	}
	return nil
}

// Confirm performs Verify and, only if the whole group matches, returns a deep
// copy of the choices.  A host must not publish any part of the group when
// Confirm returns an error.
func (c *Certificate) Confirm(observations map[PhysicalVolumeID]Observation) ([]Choice, error) {
	if err := c.Verify(observations); err != nil {
		return nil, err
	}
	out := make([]Choice, len(c.Choices))
	copy(out, c.Choices)
	for i := range out {
		if out[i].Blocks != nil {
			out[i].Blocks = append([]BlockID(nil), out[i].Blocks...)
		}
	}
	return out, nil
}

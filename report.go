package tracer

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// BatchShare describes how much of one source batch is present in a
// container: as a fraction of the contents, as a volume-equivalent, and
// as attributed solute masses.
type BatchShare struct {
	Batch    string
	Fraction *big.Rat            // fraction of the container's contents
	Volume   *big.Rat            // volume-equivalent of the batch
	Masses   map[string]*big.Rat // solute -> mass attributed to this batch
}

// ContainerReport is a snapshot of one container: volume, solute masses
// and per-source-batch shares.
type ContainerReport struct {
	ID     string
	Volume *big.Rat
	Masses map[string]*big.Rat // solute -> mass
	Shares []BatchShare        // sorted by batch label
}

// Report returns the report for one container.
func (s *System) Report(id string) (ContainerReport, error) {
	c, ok := s.containers[id]
	if !ok {
		return ContainerReport{}, fmt.Errorf("container %q does not exist", id)
	}
	rep := ContainerReport{
		ID:     id,
		Volume: new(big.Rat).Set(c.volume),
		Masses: map[string]*big.Rat{},
	}
	for k, v := range c.masses {
		rep.Masses[k] = new(big.Rat).Set(v)
	}
	labels := make([]string, 0, len(c.batchVol))
	for b := range c.batchVol {
		labels = append(labels, b)
	}
	sort.Strings(labels)
	for _, b := range labels {
		v := c.batchVol[b]
		share := BatchShare{
			Batch:  b,
			Volume: new(big.Rat).Set(v),
			Masses: map[string]*big.Rat{},
		}
		if c.volume.Sign() != 0 {
			share.Fraction = new(big.Rat).Quo(v, c.volume)
			for name, m := range c.masses {
				share.Masses[name] = new(big.Rat).Mul(share.Fraction, m)
			}
		} else {
			share.Fraction = new(big.Rat)
		}
		rep.Shares = append(rep.Shares, share)
	}
	return rep, nil
}

// ReportAll returns reports for every container, sorted by container id.
func (s *System) ReportAll() []ContainerReport {
	ids := make([]string, 0, len(s.containers))
	for id := range s.containers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	reps := make([]ContainerReport, 0, len(ids))
	for _, id := range ids {
		rep, _ := s.Report(id)
		reps = append(reps, rep)
	}
	return reps
}

func (r ContainerReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "container %s: volume=%s\n", r.ID, r.Volume)
	for _, name := range sortedKeys(r.Masses) {
		fmt.Fprintf(&b, "  mass[%s]=%s\n", name, r.Masses[name])
	}
	for _, sh := range r.Shares {
		fmt.Fprintf(&b, "  batch %s: fraction=%s volume=%s\n", sh.Batch, sh.Fraction, sh.Volume)
		for _, name := range sortedKeys(sh.Masses) {
			fmt.Fprintf(&b, "    mass[%s]=%s\n", name, sh.Masses[name])
		}
	}
	return b.String()
}

func sortedKeys(m map[string]*big.Rat) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

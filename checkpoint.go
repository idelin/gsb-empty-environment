package gcrt

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
)

// checkpoint is the on-disk format. It persists the identity (ID, modulus,
// residue) of every original constraint of every committed version together
// with the incremental solving state (the cached merged solution), so a
// reload can be audited by a full generalized merge.
type checkpoint struct {
	NextID   int64          `json:"nextID"`
	Versions []versionState `json:"versions"`
}

type versionState struct {
	Order       []int64           `json:"order"`
	Constraints []constraintState `json:"constraints"`
	Solution    solutionState     `json:"solution"`
}

type constraintState struct {
	ID  int64  `json:"id"`
	Mod string `json:"mod"`
	Res string `json:"res"`
}

type solutionState struct {
	OK       bool     `json:"ok"`
	Res      string   `json:"res,omitempty"`
	Period   string   `json:"period,omitempty"`
	Conflict [2]int64 `json:"conflict,omitempty"`
}

// SaveCheckpoint writes every committed version (original constraints plus
// cached incremental solution) to path as JSON.
func (s *Store) SaveCheckpoint(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := checkpoint{NextID: s.nextID}
	for _, sys := range s.versions {
		vs := versionState{Order: append([]int64(nil), sys.order...)}
		for _, id := range sys.order {
			c := sys.cons[id]
			vs.Constraints = append(vs.Constraints, constraintState{
				ID:  c.ID,
				Mod: c.Mod.String(),
				Res: c.Res.String(),
			})
		}
		vs.Solution = solutionState{OK: sys.sol.OK, Conflict: sys.sol.Conflict}
		if sys.sol.OK {
			vs.Solution.Res = sys.sol.Res.String()
			vs.Solution.Period = sys.sol.Period.String()
		}
		cp.Versions = append(cp.Versions, vs)
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadCheckpoint restores a store saved by SaveCheckpoint. Every version is
// audited: the persisted incremental solution must equal a full generalized
// merge of the persisted original constraints, otherwise the checkpoint is
// rejected as corrupt.
func LoadCheckpoint(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("gcrt: invalid checkpoint: %w", err)
	}
	st := &Store{nextID: cp.NextID}
	for v, vs := range cp.Versions {
		sys := NewSystem()
		seen := make(map[int64]bool, len(vs.Constraints))
		byID := make(map[int64]Constraint, len(vs.Constraints))
		for _, cs := range vs.Constraints {
			c, err := parseConstraint(cs)
			if err != nil {
				return nil, fmt.Errorf("gcrt: version %d: %w", v, err)
			}
			if seen[c.ID] {
				return nil, fmt.Errorf("gcrt: version %d: duplicate constraint ID %d", v, c.ID)
			}
			seen[c.ID] = true
			byID[c.ID] = c
		}
		for _, id := range vs.Order {
			c, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("gcrt: version %d: unknown constraint ID %d in order", v, id)
			}
			delete(byID, id)
			if err := sys.Add(c); err != nil {
				return nil, fmt.Errorf("gcrt: version %d: %w", v, err)
			}
		}
		if len(byID) != 0 {
			return nil, fmt.Errorf("gcrt: version %d: constraints missing from order", v)
		}
		want, err := parseSolution(vs.Solution)
		if err != nil {
			return nil, fmt.Errorf("gcrt: version %d: %w", v, err)
		}
		if got := sys.Solution(); !sameSolution(got, want) {
			return nil, fmt.Errorf("gcrt: version %d: checkpoint state %v does not match full merge %v", v, want, got)
		}
		st.versions = append(st.versions, sys)
	}
	if len(st.versions) == 0 {
		return nil, fmt.Errorf("gcrt: checkpoint contains no versions")
	}
	st.cur = st.versions[len(st.versions)-1].Clone()
	return st, nil
}

func parseConstraint(cs constraintState) (Constraint, error) {
	mod, ok := new(big.Int).SetString(cs.Mod, 10)
	if !ok {
		return Constraint{}, fmt.Errorf("constraint %d: bad modulus %q", cs.ID, cs.Mod)
	}
	res, ok := new(big.Int).SetString(cs.Res, 10)
	if !ok {
		return Constraint{}, fmt.Errorf("constraint %d: bad residue %q", cs.ID, cs.Res)
	}
	c := Constraint{ID: cs.ID, Mod: mod, Res: res}
	return c, c.validate()
}

func parseSolution(ss solutionState) (Solution, error) {
	if !ss.OK {
		return Solution{OK: false, Conflict: ss.Conflict}, nil
	}
	res, ok := new(big.Int).SetString(ss.Res, 10)
	if !ok {
		return Solution{}, fmt.Errorf("bad solution residue %q", ss.Res)
	}
	period, ok := new(big.Int).SetString(ss.Period, 10)
	if !ok || period.Sign() <= 0 {
		return Solution{}, fmt.Errorf("bad solution period %q", ss.Period)
	}
	return Solution{OK: true, Res: res, Period: period}, nil
}

func sameSolution(a, b Solution) bool {
	if a.OK != b.OK {
		return false
	}
	if !a.OK {
		return a.Conflict == b.Conflict
	}
	return a.Res.Cmp(b.Res) == 0 && a.Period.Cmp(b.Period) == 0
}

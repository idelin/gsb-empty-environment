package gcrt

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
)

// Checkpoint persistence. Every version is stored with its original
// constraint identities (ID, modulus, residue as submitted) plus the
// incremental solver state (the merged solution). Load re-derives each
// version's solution with a full generalized merge of the original
// constraints and refuses checkpoints whose incremental state disagrees.

type constraintJSON struct {
	ID  uint64 `json:"id"`
	Mod string `json:"mod"`
	Res string `json:"res"`
}

type solutionJSON struct {
	OK        bool   `json:"ok"`
	Res       string `json:"res,omitempty"`
	Period    string `json:"period,omitempty"`
	ConflictA uint64 `json:"conflictA,omitempty"`
	ConflictB uint64 `json:"conflictB,omitempty"`
}

type versionJSON struct {
	Constraints []constraintJSON `json:"constraints"`
	Solution    solutionJSON     `json:"solution"`
}

type checkpointJSON struct {
	NextID   uint64        `json:"nextID"`
	Versions []versionJSON `json:"versions"`
}

// Save writes a checkpoint of every committed version to w.
func (s *Store) Save(w io.Writer) error {
	cp := checkpointJSON{NextID: s.nextID}
	for _, snap := range s.versions {
		vj := versionJSON{Solution: marshalSolution(snap.sol)}
		for _, id := range sortedIDs(snap.cons) {
			c := snap.cons[id]
			vj.Constraints = append(vj.Constraints, constraintJSON{
				ID:  c.ID,
				Mod: c.Mod.String(),
				Res: c.Res.String(),
			})
		}
		cp.Versions = append(cp.Versions, vj)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(cp)
}

// Load restores a checkpoint. Each version's persisted incremental state is
// verified against a full re-merge of its original constraints; a mismatch
// means the checkpoint is corrupt and an error is returned.
func Load(r io.Reader) (*Store, error) {
	var cp checkpointJSON
	if err := json.NewDecoder(r).Decode(&cp); err != nil {
		return nil, fmt.Errorf("gcrt: decoding checkpoint: %v", err)
	}
	if len(cp.Versions) == 0 {
		return nil, fmt.Errorf("gcrt: checkpoint has no versions")
	}
	s := &Store{nextID: cp.NextID}
	for i, vj := range cp.Versions {
		cons := make(map[uint64]Constraint, len(vj.Constraints))
		for _, cj := range vj.Constraints {
			mod, ok := new(big.Int).SetString(cj.Mod, 10)
			if !ok || mod.Sign() <= 0 {
				return nil, fmt.Errorf("gcrt: version %d: bad modulus %q", i, cj.Mod)
			}
			res, ok := new(big.Int).SetString(cj.Res, 10)
			if !ok {
				return nil, fmt.Errorf("gcrt: version %d: bad residue %q", i, cj.Res)
			}
			if _, dup := cons[cj.ID]; dup {
				return nil, fmt.Errorf("gcrt: version %d: duplicate constraint ID %d", i, cj.ID)
			}
			cons[cj.ID] = Constraint{ID: cj.ID, Res: res, Mod: mod}
		}
		sol := solve(cons) // full generalized re-merge of the originals
		persisted, err := unmarshalSolution(vj.Solution)
		if err != nil {
			return nil, fmt.Errorf("gcrt: version %d: %v", i, err)
		}
		if !sameSolution(sol, persisted) {
			return nil, fmt.Errorf("gcrt: version %d: persisted state fails full-merge verification", i)
		}
		s.versions = append(s.versions, &snapshot{cons: cons, sol: sol})
	}
	return s, nil
}

func marshalSolution(s Solution) solutionJSON {
	sj := solutionJSON{OK: s.OK, ConflictA: s.Conflict[0], ConflictB: s.Conflict[1]}
	if s.OK {
		sj.Res = s.Res.String()
		sj.Period = s.Period.String()
	}
	return sj
}

func unmarshalSolution(sj solutionJSON) (Solution, error) {
	if !sj.OK {
		return inconsistent(sj.ConflictA, sj.ConflictB), nil
	}
	res, ok := new(big.Int).SetString(sj.Res, 10)
	if !ok {
		return Solution{}, fmt.Errorf("bad solution residue %q", sj.Res)
	}
	period, ok := new(big.Int).SetString(sj.Period, 10)
	if !ok || period.Sign() <= 0 {
		return Solution{}, fmt.Errorf("bad solution period %q", sj.Period)
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

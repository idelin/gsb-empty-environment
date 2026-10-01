package verifier

import "encoding/json"

// Action is one recorded step of a verification session. A slice of actions
// is a fixture: it can be serialized, checked in, and replayed later to
// reproduce a run exactly, including the locally synthesized telemetry.
type Action struct {
	Op       string    `json:"op"` // "stage" or "commit"
	Chunk    *Chunk    `json:"chunk,omitempty"`
	Manifest *Manifest `json:"manifest,omitempty"`
}

// Stage records a chunk-arrival action.
func Stage(c Chunk) Action { return Action{Op: "stage", Chunk: &c} }

// CommitAct records a manifest-commit action.
func CommitAct(m Manifest) Action { return Action{Op: "commit", Manifest: &m} }

// ReplayResult captures the observable outcome of a replay.
type ReplayResult struct {
	Reports    []Report `json:"reports"`
	Rejections []Issue  `json:"rejections"`
	Events     []Event  `json:"events"`
}

// FinalReport returns the last commit report, if any.
func (r ReplayResult) FinalReport() *Report {
	if len(r.Reports) == 0 {
		return nil
	}
	return &r.Reports[len(r.Reports)-1]
}

// Replay re-executes a fixture against a fresh verifier rooted at stateDir.
// If reopenEvery > 0, the verifier is closed and recovered from disk after
// every reopenEvery actions, simulating crash recovery mid-stream. Any
// arrival order plus recovery must yield the same result as one full
// verification, which is exactly what the tests assert through this helper.
func Replay(stateDir string, actions []Action, reopenEvery int) (*ReplayResult, error) {
	em := &SliceEmitter{}
	v, err := Open(stateDir, em)
	if err != nil {
		return nil, err
	}
	res := &ReplayResult{}
	for i, a := range actions {
		if reopenEvery > 0 && i > 0 && i%reopenEvery == 0 {
			if v, err = Open(stateDir, em); err != nil {
				return nil, err
			}
		}
		switch a.Op {
		case "stage":
			if err := v.StageChunk(*a.Chunk); err != nil {
				if rej, ok := err.(*Rejection); ok {
					res.Rejections = append(res.Rejections, rej.Issue)
				} else {
					return nil, err
				}
			}
		case "commit":
			rep, err := v.Commit(*a.Manifest)
			if err != nil {
				if rej, ok := err.(*Rejection); ok {
					res.Rejections = append(res.Rejections, rej.Issue)
				} else {
					return nil, err
				}
			} else {
				res.Reports = append(res.Reports, *rep)
			}
		}
	}
	res.Events = em.Events()
	return res, nil
}

// MarshalFixture serializes a fixture for check-in.
func MarshalFixture(actions []Action) ([]byte, error) {
	return json.MarshalIndent(actions, "", "  ")
}

// UnmarshalFixture loads a checked-in fixture.
func UnmarshalFixture(raw []byte) ([]Action, error) {
	var actions []Action
	err := json.Unmarshal(raw, &actions)
	return actions, err
}

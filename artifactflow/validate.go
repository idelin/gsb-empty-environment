package artifactflow

import "sort"

// indexedReceipt is a receipt with its normalized path and digest.
type indexedReceipt struct {
	receipt    Receipt
	path       Path
	normDigest string
}

type slotIndex struct {
	slot    Slot
	pattern *pattern
	digest  string
}

type ruleIndex struct {
	rule       Rule
	inputName  string
	outputName string
	target     *pattern // nil when the rule keeps the output slot path
}

type candidateIndex struct {
	stepID       string
	candidate    Candidate
	inputs       []slotIndex
	outputs      []slotIndex
	rules        []ruleIndex
	ruleByOutput map[string]*ruleIndex
}

type stepIndex struct {
	step       Step
	candidates []candidateIndex
}

type manifestIndex struct {
	entry ManifestEntry
	path  Path
}

// solved is the validated and indexed representation of a Problem.
type solved struct {
	tenantRoot Path
	receipts   []indexedReceipt
	byID       map[string]int
	steps      []stepIndex
	manifest   []manifestIndex
	budget     int64
}

// prepare validates and indexes a problem without running the search.
func prepare(p *Problem) (*solved, error) {
	if p == nil {
		return nil, &ValidationError{Reason: "problem is nil"}
	}
	root, err := NormalizeTenantRoot(p.TenantRoot)
	if err != nil {
		return nil, err
	}
	if len(p.Steps) > MaxSteps {
		return nil, &ValidationError{Field: "steps", Reason: "at most 16 steps are allowed"}
	}
	if len(p.Manifest) == 0 {
		return nil, &ValidationError{Field: "manifest", Reason: "manifest must contain at least one entry"}
	}

	out := &solved{
		tenantRoot: root,
		byID:       make(map[string]int, len(p.Receipts)),
		budget:     p.Budget,
	}
	if out.budget <= 0 {
		out.budget = DefaultBudget
	}

	// Receipts: unique IDs, valid normalized paths and digests.
	for _, r := range p.Receipts {
		if r.ID == "" {
			return nil, &ValidationError{Field: "receipt", Reason: "receipt ID is empty"}
		}
		if _, dup := out.byID[r.ID]; dup {
			return nil, &ValidationError{Field: "receipt", Reason: "duplicate receipt ID: " + r.ID}
		}
		np, err := NormalizePath(r.Path)
		if err != nil {
			return nil, err
		}
		if root != "" && !WithinRoot(root, np) {
			return nil, &ValidationError{Field: "receipt", Reason: "receipt path escapes the tenant root: " + r.ID}
		}
		if !digestValid(r.Digest) {
			return nil, &ValidationError{Field: "receipt", Reason: "receipt digest is malformed: " + r.ID}
		}
		out.byID[r.ID] = len(out.receipts)
		out.receipts = append(out.receipts, indexedReceipt{
			receipt:    r,
			path:       np,
			normDigest: normalizeDigest(r.Digest),
		})
	}

	// Steps and candidates.
	stepIDs := make(map[string]bool, len(p.Steps))
	for _, st := range p.Steps {
		if st.ID == "" {
			return nil, &ValidationError{Field: "step", Reason: "step ID is empty"}
		}
		if stepIDs[st.ID] {
			return nil, &ValidationError{Field: "step", Reason: "duplicate step ID: " + st.ID}
		}
		stepIDs[st.ID] = true
		if len(st.Candidates) == 0 {
			return nil, &ValidationError{Field: "step", Reason: "step has no candidates: " + st.ID}
		}
		if len(st.Candidates) > MaxCandidates {
			return nil, &ValidationError{Field: "step", Reason: "at most 3 candidates per step are allowed: " + st.ID}
		}
		si := stepIndex{step: st}
		candNames := make(map[string]bool)
		for _, cand := range st.Candidates {
			if cand.Name == "" {
				return nil, &ValidationError{Field: "candidate", Reason: "candidate name is empty in step " + st.ID}
			}
			if candNames[cand.Name] {
				return nil, &ValidationError{Field: "candidate", Reason: "duplicate candidate name " + cand.Name + " in step " + st.ID}
			}
			candNames[cand.Name] = true
			ci := candidateIndex{
				stepID:       st.ID,
				candidate:    cand,
				ruleByOutput: make(map[string]*ruleIndex),
			}
			inputNames := make(map[string]bool)
			for _, s := range cand.Inputs {
				if s.Name == "" {
					return nil, &ValidationError{Field: "slot", Reason: "input slot name is empty in " + st.ID + "/" + cand.Name}
				}
				if inputNames[s.Name] {
					return nil, &ValidationError{Field: "slot", Reason: "duplicate input slot " + s.Name + " in " + st.ID + "/" + cand.Name}
				}
				inputNames[s.Name] = true
				pat, err := compilePattern(s.Path)
				if err != nil {
					return nil, err
				}
				if !digestValid(s.Digest) {
					return nil, &ValidationError{Field: "slot", Reason: "input slot digest is malformed: " + s.Name + " in " + st.ID + "/" + cand.Name}
				}
				if err := checkSigners(s); err != nil {
					return nil, err
				}
				ci.inputs = append(ci.inputs, slotIndex{slot: s, pattern: pat, digest: normalizeDigest(s.Digest)})
			}
			outputNames := make(map[string]bool)
			for _, s := range cand.Outputs {
				if s.Name == "" {
					return nil, &ValidationError{Field: "slot", Reason: "output slot name is empty in " + st.ID + "/" + cand.Name}
				}
				if outputNames[s.Name] {
					return nil, &ValidationError{Field: "slot", Reason: "duplicate output slot " + s.Name + " in " + st.ID + "/" + cand.Name}
				}
				outputNames[s.Name] = true
				if s.Consumed {
					return nil, &ValidationError{Field: "slot", Reason: "output slots cannot be consumed: " + s.Name + " in " + st.ID + "/" + cand.Name}
				}
				pat, err := compilePattern(s.Path)
				if err != nil {
					return nil, err
				}
				if !digestValid(s.Digest) {
					return nil, &ValidationError{Field: "slot", Reason: "output slot digest is malformed: " + s.Name + " in " + st.ID + "/" + cand.Name}
				}
				if err := checkSigners(s); err != nil {
					return nil, err
				}
				ci.outputs = append(ci.outputs, slotIndex{slot: s, pattern: pat, digest: normalizeDigest(s.Digest)})
			}
			for _, r := range cand.Rules {
				if !inputNames[r.Input] {
					return nil, &ValidationError{Field: "rule", Reason: "rule input slot does not exist: " + r.Input + " in " + st.ID + "/" + cand.Name}
				}
				if !outputNames[r.Output] {
					return nil, &ValidationError{Field: "rule", Reason: "rule output slot does not exist: " + r.Output + " in " + st.ID + "/" + cand.Name}
				}
				if ci.ruleByOutput[r.Output] != nil {
					return nil, &ValidationError{Field: "rule", Reason: "output slot has more than one rename rule: " + r.Output + " in " + st.ID + "/" + cand.Name}
				}
				ri := ruleIndex{rule: r, inputName: r.Input, outputName: r.Output}
				if r.Target != "" {
					tp, err := compilePattern(r.Target)
					if err != nil {
						return nil, err
					}
					ri.target = tp
				}
				ci.rules = append(ci.rules, ri)
				ci.ruleByOutput[r.Output] = &ci.rules[len(ci.rules)-1]
			}
			si.candidates = append(si.candidates, ci)
		}
		out.steps = append(out.steps, si)
	}

	// Manifest: normalized paths, valid digests, unique entries.
	manifestKeys := make(map[string]bool)
	for _, e := range p.Manifest {
		np, err := NormalizePath(e.Path)
		if err != nil {
			return nil, err
		}
		if root != "" && !WithinRoot(root, np) {
			return nil, &ValidationError{Field: "manifest", Reason: "manifest path escapes the tenant root: " + string(e.Path)}
		}
		if !digestValid(e.Digest) {
			return nil, &ValidationError{Field: "manifest", Reason: "manifest digest is malformed: " + string(e.Path)}
		}
		key := string(np) + "|" + normalizeDigest(e.Digest)
		if manifestKeys[key] {
			return nil, &ValidationError{Field: "manifest", Reason: "duplicate manifest entry: " + key}
		}
		manifestKeys[key] = true
		out.manifest = append(out.manifest, manifestIndex{entry: e, path: np})
	}

	// Manifest entries must have at least one receipt with the same path/digest.
	for _, e := range out.manifest {
		if !out.hasReceipt(e.path, e.entry.Digest) {
			return nil, &ValidationError{Field: "manifest", Reason: "no receipt matches manifest entry: " + string(e.path)}
		}
	}
	return out, nil
}

func checkSigners(s Slot) error {
	seen := make(map[SignerRole]bool)
	for _, role := range s.AllowedSigners {
		if role == "" {
			return &ValidationError{Field: "slot", Reason: "empty signer role in slot " + s.Name}
		}
		if seen[role] {
			return &ValidationError{Field: "slot", Reason: "duplicate signer role in slot " + s.Name}
		}
		seen[role] = true
	}
	return nil
}

func (s *solved) hasReceipt(path Path, d Digest) bool {
	nd := normalizeDigest(d)
	for _, ir := range s.receipts {
		if ir.path == path && ir.normDigest == nd {
			return true
		}
	}
	return false
}

// signerAllowed reports whether role is accepted by a slot.
func signerAllowed(slot Slot, role SignerRole) bool {
	if len(slot.AllowedSigners) == 0 {
		return true
	}
	for _, allowed := range slot.AllowedSigners {
		if allowed == role {
			return true
		}
	}
	return false
}

// sortedSourceIDs returns a deterministic, sorted copy of a source set.
func sortedSourceIDs(set map[SourceID]bool) []SourceID {
	out := make([]SourceID, 0, len(set))
	for src := range set {
		out = append(out, src)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

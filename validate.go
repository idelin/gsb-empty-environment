package artifactflow

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	maxSteps                = 16
	maxCandidatesPerStep    = 3
	sourceReceiptRootPrefix = "receipt:"
)

type artifactKey struct {
	tenant string
	path   string
	digest string
}

type instKey struct {
	kind InstanceKind
	id   string
	idx  int
}

func rootInstanceKey(id string) instKey {
	return instKey{kind: InstanceRoot, id: id}
}

func outputInstanceKey(receiptID string, idx int) instKey {
	return instKey{kind: InstanceOutput, id: receiptID, idx: idx}
}

func (k instKey) ref() InstanceRef {
	if k.kind == InstanceRoot {
		return InstanceRef{Kind: InstanceRoot, RootID: k.id}
	}
	return InstanceRef{
		Kind:        InstanceOutput,
		ReceiptID:   k.id,
		OutputIndex: k.idx,
	}
}

func (k instKey) String() string {
	if k.kind == InstanceRoot {
		return fmt.Sprintf("root:%d:%s", len(k.id), k.id)
	}
	return fmt.Sprintf("output:%d:%s:%d", len(k.id), k.id, k.idx)
}

type rootInfo struct {
	root RootArtifact
	key  artifactKey
	inst instKey
}

type receiptInfo struct {
	receipt Receipt
	step    *stepInfo
	key     string
	inputs  []InputPort
	outputs []OutputPort
	tenant  string
	rules   []compiledRule
	semKey  string
}

type stepInfo struct {
	id           string
	alternatives []*receiptInfo
}

type finalInfo struct {
	final     FinalArtifact
	key       artifactKey
	sensitive bool
	required  map[Role]struct{}
}

type compiledSpec struct {
	set            *ReceiptSet
	roots          []*rootInfo
	rootByID       map[string]*rootInfo
	steps          []*stepInfo
	stepByID       map[string]*stepInfo
	receiptByID    map[string]*receiptInfo
	finals         []*finalInfo
	allowedSigners map[Role]struct{}
	anySensitive   bool
}

func compileSpec(set *ReceiptSet) (*compiledSpec, error) {
	if set == nil {
		return nil, fmt.Errorf("artifactflow: nil receipt set")
	}
	spec := &compiledSpec{
		set:            set,
		rootByID:       make(map[string]*rootInfo),
		stepByID:       make(map[string]*stepInfo),
		receiptByID:    make(map[string]*receiptInfo),
		allowedSigners: make(map[Role]struct{}),
	}
	if len(set.Finals) == 0 {
		return nil, fmt.Errorf("artifactflow: receipt set has no final artifacts")
	}

	rootIDs := make(map[string]struct{})
	for _, raw := range set.Roots {
		if raw.ID == "" {
			return nil, fmt.Errorf("artifactflow: root artifact has an empty ID")
		}
		if strings.HasPrefix(raw.ID, sourceReceiptRootPrefix) {
			return nil, fmt.Errorf("artifactflow: root ID %q uses reserved prefix %q", raw.ID, sourceReceiptRootPrefix)
		}
		if _, exists := rootIDs[raw.ID]; exists {
			return nil, fmt.Errorf("artifactflow: duplicate root artifact ID %q", raw.ID)
		}
		rootIDs[raw.ID] = struct{}{}
		if raw.Signer == "" {
			return nil, fmt.Errorf("artifactflow: root %q has an empty signer role", raw.ID)
		}
		path, err := normalizeLogicalPath(raw.Path)
		if err != nil {
			return nil, fmt.Errorf("root %q: %w", raw.ID, err)
		}
		if err := validateDigest(raw.Digest); err != nil {
			return nil, fmt.Errorf("root %q: %w", raw.ID, err)
		}
		root := raw
		root.Path = path
		info := &rootInfo{
			root: root,
			key:  artifactKey{tenant: root.Tenant, path: path, digest: root.Digest},
			inst: rootInstanceKey(root.ID),
		}
		spec.roots = append(spec.roots, info)
		spec.rootByID[root.ID] = info
	}
	sort.Slice(spec.roots, func(i, j int) bool { return spec.roots[i].root.ID < spec.roots[j].root.ID })

	for _, role := range set.Policy.AllowedSigners {
		if role == "" {
			return nil, fmt.Errorf("artifactflow: policy contains an empty signer role")
		}
		spec.allowedSigners[role] = struct{}{}
	}

	byStep := make(map[string][]*receiptInfo)
	for i := range set.Receipts {
		raw := set.Receipts[i]
		if raw.ID == "" {
			return nil, fmt.Errorf("artifactflow: receipt has an empty ID")
		}
		if _, exists := spec.receiptByID[raw.ID]; exists {
			return nil, fmt.Errorf("artifactflow: duplicate receipt ID %q", raw.ID)
		}
		if raw.Step == "" {
			return nil, fmt.Errorf("receipt %q: empty step ID", raw.ID)
		}
		if raw.Signer == "" {
			return nil, fmt.Errorf("receipt %q: empty signer role", raw.ID)
		}
		rc := &receiptInfo{receipt: raw, key: raw.ID, tenant: raw.Tenant}
		for j, in := range raw.Inputs {
			path, err := normalizeLogicalPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("receipt %q input %d: %w", raw.ID, j, err)
			}
			if err := validateDigest(in.Digest); err != nil {
				return nil, fmt.Errorf("receipt %q input %d: %w", raw.ID, j, err)
			}
			if in.Consumption != Share && in.Consumption != Consume {
				return nil, fmt.Errorf("receipt %q input %d: invalid consumption mode %d", raw.ID, j, in.Consumption)
			}
			input := in
			input.Path = path
			rc.inputs = append(rc.inputs, input)
		}
		seenOutputs := make(map[artifactKey]struct{})
		for j, out := range raw.Outputs {
			path, err := normalizeLogicalPath(out.Path)
			if err != nil {
				return nil, fmt.Errorf("receipt %q output %d: %w", raw.ID, j, err)
			}
			if err := validateDigest(out.Digest); err != nil {
				return nil, fmt.Errorf("receipt %q output %d: %w", raw.ID, j, err)
			}
			key := artifactKey{tenant: raw.Tenant, path: path, digest: out.Digest}
			if _, dup := seenOutputs[key]; dup {
				return nil, fmt.Errorf("receipt %q declares duplicate output %q (%s)", raw.ID, path, out.Digest)
			}
			seenOutputs[key] = struct{}{}
			output := out
			output.Path = path
			rc.outputs = append(rc.outputs, output)
		}
		for j, rule := range raw.Renames {
			cr, err := compileRule(rule)
			if err != nil {
				return nil, fmt.Errorf("receipt %q rename %d: %w", raw.ID, j, err)
			}
			rc.rules = append(rc.rules, *cr)
		}
		rc.semKey = semanticReceiptKey(rc)
		spec.receiptByID[raw.ID] = rc
		byStep[raw.Step] = append(byStep[raw.Step], rc)
	}

	if len(byStep) > maxSteps {
		return nil, fmt.Errorf("artifactflow: %d steps exceeds the maximum of %d", len(byStep), maxSteps)
	}
	stepIDs := make([]string, 0, len(byStep))
	for id := range byStep {
		stepIDs = append(stepIDs, id)
	}
	sort.Strings(stepIDs)
	for _, id := range stepIDs {
		alts := byStep[id]
		if len(alts) == 0 {
			return nil, fmt.Errorf("artifactflow: step %q has no candidate receipts", id)
		}
		if len(alts) > maxCandidatesPerStep {
			return nil, fmt.Errorf("artifactflow: step %q has %d candidates; maximum is %d", id, len(alts), maxCandidatesPerStep)
		}
		sort.Slice(alts, func(i, j int) bool { return alts[i].key < alts[j].key })
		st := &stepInfo{id: id, alternatives: alts}
		for _, alt := range alts {
			alt.step = st
		}
		spec.steps = append(spec.steps, st)
		spec.stepByID[id] = st
	}

	seenFinals := make(map[artifactKey]struct{})
	for i := range set.Finals {
		raw := set.Finals[i]
		path, err := normalizeLogicalPath(raw.Path)
		if err != nil {
			return nil, fmt.Errorf("final artifact %d: %w", i, err)
		}
		if err := validateDigest(raw.Digest); err != nil {
			return nil, fmt.Errorf("final artifact %d: %w", i, err)
		}
		key := artifactKey{tenant: raw.Tenant, path: path, digest: raw.Digest}
		if _, dup := seenFinals[key]; dup {
			return nil, fmt.Errorf("artifactflow: duplicate final artifact %q in tenant %q", path, raw.Tenant)
		}
		seenFinals[key] = struct{}{}
		final := raw
		final.Path = path
		fi := &finalInfo{
			final:     final,
			key:       key,
			sensitive: raw.Sensitive,
			required:  make(map[Role]struct{}),
		}
		for _, role := range raw.RequiredSigners {
			if role == "" {
				return nil, fmt.Errorf("final artifact %q has an empty required signer role", path)
			}
			fi.required[role] = struct{}{}
		}
		if raw.Sensitive {
			spec.anySensitive = true
		}
		spec.finals = append(spec.finals, fi)
	}
	return spec, nil
}

func validateDigest(d Digest) error {
	if d == "" {
		return fmt.Errorf("artifactflow: empty digest")
	}
	if strings.ContainsRune(d, '\x00') {
		return fmt.Errorf("artifactflow: digest contains NUL")
	}
	return nil
}

// semanticReceiptKey identifies alternatives that are indistinguishable to
// the release decision: same tenant, signer, ports and rename rules. Receipt
// IDs of non-source alternatives are deliberately excluded because they never
// become root provenance. Source alternatives keep their ID because for them
// the receipt itself is the provenance root.
func semanticReceiptKey(rc *receiptInfo) string {
	var b strings.Builder
	part := func(s string) {
		fmt.Fprintf(&b, "%d:%s;", len(s), s)
	}
	count := func(n int) {
		fmt.Fprintf(&b, "%d;", n)
	}
	part(rc.tenant)
	part(rc.receipt.Signer)
	count(len(rc.inputs))
	for _, in := range rc.inputs {
		part(in.Path)
		part(in.Digest)
		fmt.Fprintf(&b, "%d;", in.Consumption)
	}
	count(len(rc.outputs))
	for _, out := range rc.outputs {
		part(out.Path)
		part(out.Digest)
	}
	count(len(rc.rules))
	for _, rule := range rc.rules {
		part(rule.raw)
	}
	if len(rc.inputs) == 0 {
		part(rc.key)
	}
	return b.String()
}

func (s *compiledSpec) signerAllowed(role Role) bool {
	if len(s.allowedSigners) == 0 {
		return true
	}
	_, ok := s.allowedSigners[role]
	return ok
}

func (s *compiledSpec) sensitive(index int) bool {
	if !s.anySensitive {
		return true
	}
	return s.finals[index].sensitive
}

func decodeJSON(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("artifactflow: decode JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("artifactflow: trailing JSON value")
		}
		return fmt.Errorf("artifactflow: decode trailing JSON: %w", err)
	}
	return nil
}

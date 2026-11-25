package artifactflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

func buildCertificate(spec *compiledSpec, st *searchState) (*Certificate, error) {
	cert := &Certificate{
		Version:    CertificateVersion,
		Selections: make([]Selection, 0, len(spec.steps)),
		Edges:      make([]Edge, 0, len(st.edges)),
		Artifacts:  make([]ArtifactRecord, 0, len(st.instances)),
		Finals:     make([]FinalRecord, 0, len(spec.finals)),
	}
	for _, step := range spec.steps {
		rc := st.selected[step.id]
		if rc == nil {
			return nil, fmt.Errorf("artifactflow: internal certificate error: step %q has no selection", step.id)
		}
		cert.Selections = append(cert.Selections, Selection{
			Step:           step.id,
			Receipt:        rc.key,
			Implementation: rc.receipt.Implementation,
			Signer:         rc.receipt.Signer,
		})
	}
	for _, edge := range st.edges {
		cert.Edges = append(cert.Edges, Edge{
			Step:       edge.consumer.receipt.Step,
			Receipt:    edge.consumer.key,
			InputIndex: edge.inputIndex,
			Source:     edge.source.ref(),
			Rule:       edge.rule,
		})
	}
	for _, inst := range sortedInstances(st.instances) {
		cert.Artifacts = append(cert.Artifacts, ArtifactRecord{
			Instance: inst.key.ref(),
			Tenant:   inst.artifact.tenant,
			Path:     inst.artifact.path,
			Digest:   inst.artifact.digest,
			Roots:    inst.prov.rootList(),
			Receipts: inst.prov.receiptList(),
			Signers:  inst.prov.signerList(),
		})
	}
	for i, final := range spec.finals {
		if !st.finalAssigned[i] {
			return nil, fmt.Errorf("artifactflow: internal certificate error: final %d has no source", i)
		}
		inst := st.instances[st.finalSources[i]]
		if inst == nil {
			return nil, fmt.Errorf("artifactflow: internal certificate error: final %d source is missing", i)
		}
		cert.Finals = append(cert.Finals, FinalRecord{
			Index:    i,
			Tenant:   final.final.Tenant,
			Path:     final.final.Path,
			Digest:   final.final.Digest,
			Instance: inst.key.ref(),
			Roots:    inst.prov.rootList(),
			Receipts: inst.prov.receiptList(),
			Signers:  inst.prov.signerList(),
		})
	}
	sort.Slice(cert.Edges, func(i, j int) bool {
		if cert.Edges[i].Step != cert.Edges[j].Step {
			return cert.Edges[i].Step < cert.Edges[j].Step
		}
		if cert.Edges[i].Receipt != cert.Edges[j].Receipt {
			return cert.Edges[i].Receipt < cert.Edges[j].Receipt
		}
		return cert.Edges[i].InputIndex < cert.Edges[j].InputIndex
	})
	cert.BodyHash = certificateHash(cert)
	return cert, nil
}

func sortedInstances(instances map[instKey]*instance) []*instance {
	out := make([]*instance, 0, len(instances))
	for _, inst := range instances {
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key.String() < out[j].key.String() })
	return out
}

func certificateHash(cert *Certificate) string {
	copy := *cert
	copy.BodyHash = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Verify independently checks a certificate against the receipt set. It does
// not rerun the solver and does not execute any build step: it recomputes
// provenance and every graph constraint from the recorded selections and
// edges.
func VerifyCertificate(set *ReceiptSet, cert *Certificate) error {
	spec, err := compileSpec(set)
	if err != nil {
		return err
	}
	if cert == nil {
		return fmt.Errorf("artifactflow: nil certificate")
	}
	if cert.Version != CertificateVersion {
		return fmt.Errorf("artifactflow: certificate version %q, want %q", cert.Version, CertificateVersion)
	}
	if cert.BodyHash == "" {
		return fmt.Errorf("artifactflow: certificate has no body hash")
	}
	if got := certificateHash(cert); got != cert.BodyHash {
		return fmt.Errorf("artifactflow: certificate body hash mismatch: got %s, want %s", got, cert.BodyHash)
	}

	_, selectedReceipts, err := verifySelections(spec, cert)
	if err != nil {
		return err
	}

	artifacts, err := verifyArtifactRecords(spec, cert, selectedReceipts)
	if err != nil {
		return err
	}
	edgesByReceipt, err := verifyEdges(spec, cert, selectedReceipts, artifacts)
	if err != nil {
		return err
	}
	if err := verifyProvenance(spec, cert, selectedReceipts, artifacts, edgesByReceipt); err != nil {
		return err
	}
	if err := verifyFinals(spec, cert, artifacts); err != nil {
		return err
	}
	return nil
}

// Verify is a convenience method equivalent to VerifyCertificate(set, c).
func (c *Certificate) Verify(set *ReceiptSet) error {
	return VerifyCertificate(set, c)
}

// Hash returns the canonical SHA-256 body hash of the certificate. It is
// computed with the BodyHash field cleared, so callers can use it to detect
// edits without depending on package internals.
func (c *Certificate) Hash() string {
	if c == nil {
		return ""
	}
	return certificateHash(c)
}

// DecodeCertificate decodes a certificate from JSON.
func DecodeCertificate(r io.Reader) (*Certificate, error) {
	var cert Certificate
	if err := decodeJSON(r, &cert); err != nil {
		return nil, err
	}
	return &cert, nil
}

func verifySelections(spec *compiledSpec, cert *Certificate) (map[string]*receiptInfo, map[string]*receiptInfo, error) {
	selectedSteps := make(map[string]*receiptInfo, len(cert.Selections))
	selectedReceipts := make(map[string]*receiptInfo, len(cert.Selections))
	for _, sel := range cert.Selections {
		if _, dup := selectedSteps[sel.Step]; dup {
			return nil, nil, fmt.Errorf("artifactflow: duplicate selection for step %q", sel.Step)
		}
		step, ok := spec.stepByID[sel.Step]
		if !ok {
			return nil, nil, fmt.Errorf("artifactflow: certificate selects unknown step %q", sel.Step)
		}
		rc, ok := spec.receiptByID[sel.Receipt]
		if !ok {
			return nil, nil, fmt.Errorf("artifactflow: certificate selects unknown receipt %q", sel.Receipt)
		}
		if rc.receipt.Step != step.id {
			return nil, nil, fmt.Errorf("artifactflow: receipt %q belongs to step %q, not %q", rc.key, rc.receipt.Step, sel.Step)
		}
		if rc.receipt.Implementation != sel.Implementation {
			return nil, nil, fmt.Errorf("artifactflow: receipt %q implementation mismatch", rc.key)
		}
		if rc.receipt.Signer != sel.Signer {
			return nil, nil, fmt.Errorf("artifactflow: receipt %q signer mismatch", rc.key)
		}
		if !spec.signerAllowed(rc.receipt.Signer) {
			return nil, nil, fmt.Errorf("artifactflow: receipt %q signer %q is not allowed", rc.key, rc.receipt.Signer)
		}
		selectedSteps[sel.Step] = rc
		selectedReceipts[sel.Receipt] = rc
	}
	if len(selectedSteps) != len(spec.steps) {
		return nil, nil, fmt.Errorf("artifactflow: certificate selects %d steps, want %d", len(selectedSteps), len(spec.steps))
	}
	for _, step := range spec.steps {
		if _, ok := selectedSteps[step.id]; !ok {
			return nil, nil, fmt.Errorf("artifactflow: certificate does not select step %q", step.id)
		}
	}
	return selectedSteps, selectedReceipts, nil
}

func refKey(ref InstanceRef) (instKey, error) {
	switch ref.Kind {
	case InstanceRoot:
		if ref.RootID == "" {
			return instKey{}, fmt.Errorf("artifactflow: root instance has empty root ID")
		}
		if ref.ReceiptID != "" || ref.OutputIndex != 0 {
			return instKey{}, fmt.Errorf("artifactflow: root instance %q carries output fields", ref.RootID)
		}
		return rootInstanceKey(ref.RootID), nil
	case InstanceOutput:
		if ref.ReceiptID == "" {
			return instKey{}, fmt.Errorf("artifactflow: output instance has empty receipt ID")
		}
		if ref.OutputIndex < 0 {
			return instKey{}, fmt.Errorf("artifactflow: output instance %q has negative index", ref.ReceiptID)
		}
		if ref.RootID != "" {
			return instKey{}, fmt.Errorf("artifactflow: output instance %q carries a root ID", ref.ReceiptID)
		}
		return outputInstanceKey(ref.ReceiptID, ref.OutputIndex), nil
	default:
		return instKey{}, fmt.Errorf("artifactflow: unknown instance kind %q", ref.Kind)
	}
}

func verifyArtifactRecords(spec *compiledSpec, cert *Certificate, selected map[string]*receiptInfo) (map[instKey]*ArtifactRecord, error) {
	out := make(map[instKey]*ArtifactRecord, len(cert.Artifacts))
	for i := range cert.Artifacts {
		rec := &cert.Artifacts[i]
		key, err := refKey(rec.Instance)
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("artifactflow: duplicate artifact record %s", key)
		}
		switch key.kind {
		case InstanceRoot:
			ri, ok := spec.rootByID[key.id]
			if !ok {
				return nil, fmt.Errorf("artifactflow: certificate references unknown root %q", key.id)
			}
			if !spec.signerAllowed(ri.root.Signer) {
				return nil, fmt.Errorf("artifactflow: root %q signer %q is not allowed", key.id, ri.root.Signer)
			}
			if rec.Tenant != ri.key.tenant || rec.Path != ri.key.path || rec.Digest != ri.key.digest {
				return nil, fmt.Errorf("artifactflow: root %q artifact mismatch", key.id)
			}
		case InstanceOutput:
			rc, ok := selected[key.id]
			if !ok {
				return nil, fmt.Errorf("artifactflow: certificate references output of unselected receipt %q", key.id)
			}
			if key.idx >= len(rc.outputs) {
				return nil, fmt.Errorf("artifactflow: receipt %q has no output %d", key.id, key.idx)
			}
			out := rc.outputs[key.idx]
			if rec.Tenant != rc.tenant || rec.Path != out.Path || rec.Digest != out.Digest {
				return nil, fmt.Errorf("artifactflow: output %s artifact mismatch", key)
			}
		}
		out[key] = rec
	}
	return out, nil
}

type edgeKey struct {
	receipt string
	index   int
}

func verifyEdges(spec *compiledSpec, cert *Certificate, selected map[string]*receiptInfo, artifacts map[instKey]*ArtifactRecord) (map[string]map[int]Edge, error) {
	edgesByReceipt := make(map[string]map[int]Edge, len(selected))
	consumeCount := make(map[instKey]int)
	for _, edge := range cert.Edges {
		rc, ok := selected[edge.Receipt]
		if !ok {
			return nil, fmt.Errorf("artifactflow: edge references unselected receipt %q", edge.Receipt)
		}
		if edge.Step != rc.receipt.Step {
			return nil, fmt.Errorf("artifactflow: edge step %q does not match receipt %q", edge.Step, edge.Receipt)
		}
		if edge.InputIndex < 0 || edge.InputIndex >= len(rc.inputs) {
			return nil, fmt.Errorf("artifactflow: receipt %q has no input %d", edge.Receipt, edge.InputIndex)
		}
		srcKey, err := refKey(edge.Source)
		if err != nil {
			return nil, err
		}
		src, ok := artifacts[srcKey]
		if !ok {
			return nil, fmt.Errorf("artifactflow: edge source %s has no artifact record", srcKey)
		}
		input := rc.inputs[edge.InputIndex]
		if src.Tenant != rc.tenant || src.Digest != input.Digest {
			return nil, fmt.Errorf("artifactflow: edge %s[%d] source artifact mismatch", rc.key, edge.InputIndex)
		}
		rule, ok := ruleFor(rc, src.Path, input.Path)
		if !ok {
			return nil, fmt.Errorf("artifactflow: no rename rule allows %s -> %s for receipt %q input %d", src.Path, input.Path, rc.key, edge.InputIndex)
		}
		if edge.Rule == "" {
			if rule != "" {
				return nil, fmt.Errorf("artifactflow: edge %s[%d] omits its rename rule", rc.key, edge.InputIndex)
			}
		} else if edge.Rule != rule {
			valid := false
			for i := range rc.rules {
				if rc.rules[i].raw == edge.Rule && rc.rules[i].permits(src.Path, input.Path) {
					valid = true
					break
				}
			}
			if !valid {
				return nil, fmt.Errorf("artifactflow: edge claims rule %q which does not allow %s -> %s", edge.Rule, src.Path, input.Path)
			}
		}
		if edgesByReceipt[rc.key] == nil {
			edgesByReceipt[rc.key] = make(map[int]Edge)
		}
		if _, dup := edgesByReceipt[rc.key][edge.InputIndex]; dup {
			return nil, fmt.Errorf("artifactflow: duplicate edge for receipt %q input %d", rc.key, edge.InputIndex)
		}
		edgesByReceipt[rc.key][edge.InputIndex] = edge
		if input.Consumption == Consume {
			consumeCount[srcKey]++
			if consumeCount[srcKey] > 1 {
				return nil, fmt.Errorf("artifactflow: consumed input %s is used by more than one edge", srcKey)
			}
		}
	}
	for _, rc := range selected {
		for i := range rc.inputs {
			if edgesByReceipt[rc.key] == nil || edgesByReceipt[rc.key][i].Receipt == "" {
				return nil, fmt.Errorf("artifactflow: receipt %q input %d has no edge", rc.key, i)
			}
		}
	}
	return edgesByReceipt, nil
}

func verifyProvenance(spec *compiledSpec, cert *Certificate, selected map[string]*receiptInfo, artifacts map[instKey]*ArtifactRecord, edgesByReceipt map[string]map[int]Edge) error {
	memo := make(map[string][]provenance, len(selected))
	visiting := make(map[string]bool, len(selected))
	var compute func(string) ([]provenance, error)
	compute = func(receiptID string) ([]provenance, error) {
		if p, ok := memo[receiptID]; ok {
			return p, nil
		}
		if visiting[receiptID] {
			return nil, fmt.Errorf("artifactflow: build graph contains a cycle through receipt %q", receiptID)
		}
		rc, ok := selected[receiptID]
		if !ok {
			return nil, fmt.Errorf("artifactflow: unknown selected receipt %q", receiptID)
		}
		visiting[receiptID] = true
		inputProv := make([]provenance, len(rc.inputs))
		for i := range rc.inputs {
			edge := edgesByReceipt[receiptID][i]
			srcKey, _ := refKey(edge.Source)
			switch srcKey.kind {
			case InstanceRoot:
				ri := spec.rootByID[srcKey.id]
				rp := newProvenance()
				rp.roots[ri.root.ID] = struct{}{}
				rp.signers[ri.root.Signer] = struct{}{}
				inputProv[i] = rp
			case InstanceOutput:
				producer, ok := selected[srcKey.id]
				if !ok {
					return nil, fmt.Errorf("artifactflow: edge source %s has no selected producer", srcKey)
				}
				if srcKey.idx < 0 || srcKey.idx >= len(producer.outputs) {
					return nil, fmt.Errorf("artifactflow: edge source %s is out of range", srcKey)
				}
				sp, err := compute(srcKey.id)
				if err != nil {
					return nil, err
				}
				inputProv[i] = sp[srcKey.idx]
			}
		}
		outputs := make([]provenance, len(rc.outputs))
		for idx := range rc.outputs {
			outputs[idx] = outputProvenance(rc, inputProv, idx)
		}
		delete(visiting, receiptID)
		memo[receiptID] = outputs
		return outputs, nil
	}

	expected := make(map[instKey]struct{})
	for _, rc := range selected {
		outputs, err := compute(rc.key)
		if err != nil {
			return err
		}
		for idx := range rc.outputs {
			key := outputInstanceKey(rc.key, idx)
			expected[key] = struct{}{}
			rec, ok := artifacts[key]
			if !ok {
				return fmt.Errorf("artifactflow: missing artifact record for output %s", key)
			}
			if !equalStrings(rec.Roots, outputs[idx].rootList()) || !equalStrings(rec.Receipts, outputs[idx].receiptList()) || !equalSigners(rec.Signers, outputs[idx].signerList()) {
				return fmt.Errorf("artifactflow: provenance mismatch for output %s", key)
			}
		}
	}
	for _, edge := range cert.Edges {
		srcKey, _ := refKey(edge.Source)
		expected[srcKey] = struct{}{}
	}
	for _, final := range cert.Finals {
		srcKey, err := refKey(final.Instance)
		if err != nil {
			return err
		}
		expected[srcKey] = struct{}{}
	}
	for _, key := range sortedInstKeys(expected) {
		rec, ok := artifacts[key]
		if !ok {
			return fmt.Errorf("artifactflow: missing artifact record for used instance %s", key)
		}
		switch key.kind {
		case InstanceRoot:
			ri := spec.rootByID[key.id]
			if !equalStrings(rec.Roots, []string{ri.root.ID}) || len(rec.Receipts) != 0 || !equalSigners(rec.Signers, []Role{ri.root.Signer}) {
				return fmt.Errorf("artifactflow: provenance mismatch for root %s", key)
			}
		}
	}
	if len(artifacts) != len(expected) {
		return fmt.Errorf("artifactflow: certificate contains %d artifact records, expected %d", len(artifacts), len(expected))
	}
	return nil
}

func equalSigners(a, b []Role) bool {
	return equalStrings(a, b)
}

func sortedInstKeys(set map[instKey]struct{}) []instKey {
	out := make([]instKey, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func verifyFinals(spec *compiledSpec, cert *Certificate, artifacts map[instKey]*ArtifactRecord) error {
	if len(cert.Finals) != len(spec.finals) {
		return fmt.Errorf("artifactflow: certificate has %d final records, want %d", len(cert.Finals), len(spec.finals))
	}
	seen := make(map[int]bool, len(cert.Finals))
	for _, rec := range cert.Finals {
		if rec.Index < 0 || rec.Index >= len(spec.finals) {
			return fmt.Errorf("artifactflow: final record index %d is out of range", rec.Index)
		}
		if seen[rec.Index] {
			return fmt.Errorf("artifactflow: duplicate final record %d", rec.Index)
		}
		seen[rec.Index] = true
		final := spec.finals[rec.Index]
		if rec.Tenant != final.key.tenant || rec.Path != final.key.path || rec.Digest != final.key.digest {
			return fmt.Errorf("artifactflow: final record %d does not match the manifest", rec.Index)
		}
		srcKey, err := refKey(rec.Instance)
		if err != nil {
			return err
		}
		src, ok := artifacts[srcKey]
		if !ok {
			return fmt.Errorf("artifactflow: final record %d source %s has no artifact record", rec.Index, srcKey)
		}
		if src.Tenant != final.key.tenant || src.Path != final.key.path || src.Digest != final.key.digest {
			return fmt.Errorf("artifactflow: final record %d source %s does not carry the requested artifact", rec.Index, srcKey)
		}
		if rec.Tenant != src.Tenant || rec.Path != src.Path || rec.Digest != src.Digest {
			return fmt.Errorf("artifactflow: final record %d artifact fields do not match its source", rec.Index)
		}
		if !equalStrings(rec.Roots, src.Roots) || !equalStrings(rec.Receipts, src.Receipts) || !equalSigners(rec.Signers, src.Signers) {
			return fmt.Errorf("artifactflow: final record %d provenance does not match its source", rec.Index)
		}
		for role := range final.required {
			found := false
			for _, signer := range rec.Signers {
				if signer == role {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("artifactflow: final %d is missing required signer role %q", rec.Index, role)
			}
		}
	}
	return nil
}

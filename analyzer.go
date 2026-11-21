package vex

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const defaultMaxBindingStates = 100000

type analyzer struct {
	ev        Evidence
	require   bool
	verifier  SignatureVerifier
	maxStates int

	summaries     []Object
	exact         map[string]Object
	byMember      map[string][]Object
	candidates    map[string]CandidateConfig
	candidateList []CandidateConfig
	manifests     []LinkManifest
	allowed       []string
	marked        []ObjectID
	digest        string
}

type configEvaluator struct {
	a          *analyzer
	config     string
	tags       []string
	conditions *conditionEvaluator
	proof      ConfigProof
	issues     []string
	issueSeen  map[string]bool

	entries []Reference
	groups  []ArchiveGroup
	extract map[string]*extractedObject
	ordered []*extractedObject

	expectLinkable bool
}

type extractedObject struct {
	id      ObjectID
	summary Object
	defs    []symbolDef
	refs    []Reference
}

type symbolDef struct {
	name   string
	kind   BindingKind
	group  string
	object ObjectID
	refs   []Reference
}

func analyze(ev Evidence, opts Options) (*Certificate, error) {
	a := &analyzer{
		ev:         ev,
		require:    boolOption(firstBool(opts.RequireSignatures, ev.RequireSignatures), true),
		verifier:   firstVerifier(opts.Verifier, ev.Verifier),
		maxStates:  opts.MaxBindingStates,
		exact:      make(map[string]Object),
		byMember:   make(map[string][]Object),
		candidates: make(map[string]CandidateConfig),
	}
	if a.verifier == nil {
		a.verifier = ev.Verifier
	}
	if a.maxStates <= 0 {
		a.maxStates = ev.MaxBindingStates
	}
	if a.maxStates <= 0 {
		a.maxStates = defaultMaxBindingStates
	}

	a.normalize()
	digest, err := evidenceDigest(a)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	a.digest = digest

	cert := &Certificate{
		Version:        Version,
		Artifact:       ev.Artifact,
		Vulnerability:  ev.Vulnerability,
		Marked:         append([]ObjectID(nil), a.marked...),
		AllowedConfigs: append([]string(nil), a.allowed...),
		EvidenceDigest: digest,
	}

	if len(a.allowed) == 0 {
		cert.Verdict = Unknown
		cert.Reason = "no deployer-allowed build configuration was supplied"
	} else {
		var anyAffected, anyUnknown bool
		anyLinkable := false
		for _, config := range a.allowed {
			proof := a.evaluateConfig(config)
			cert.Configs = append(cert.Configs, proof)
			switch proof.Verdict {
			case ConfigAffected:
				anyAffected = true
				anyLinkable = true
			case ConfigNotAffected:
				anyLinkable = anyLinkable || proof.Linkable
			case ConfigNotLinkable:
				// A signed, allowed, but non-linkable configuration cannot put
				// code into a final image.
			case ConfigUnknown:
				anyUnknown = true
			}
		}
		switch {
		case anyAffected:
			cert.Verdict = Affected
			cert.Reason = "a legal weak/group binding reaches a marked object identity"
		case anyUnknown:
			cert.Verdict = Unknown
			cert.Reason = "signed evidence is incomplete for at least one deployer-allowed configuration"
		case anyLinkable:
			cert.Verdict = NotAffected
			cert.Reason = "every legal binding in every allowed linkable configuration is entry-unreachable from marked objects"
		default:
			cert.Verdict = NotAffected
			cert.Reason = "no deployer-allowed configuration is linkable"
		}
	}
	cert.CertificateDigest, err = certificateDigest(cert)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return cert, nil
}

func firstBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstVerifier(values ...SignatureVerifier) SignatureVerifier {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (a *analyzer) normalize() {
	candidates := append(append([]CandidateConfig(nil), a.ev.Configs...), a.ev.Candidates...)
	seenCandidates := make(map[string]bool)
	for _, candidate := range candidates {
		candidate = normalizeCandidate(candidate)
		if candidate.ID == "" {
			continue
		}
		if !seenCandidates[candidate.ID] {
			a.candidateList = append(a.candidateList, candidate)
			seenCandidates[candidate.ID] = true
		}
		a.candidates[candidate.ID] = candidate
	}
	sort.Slice(a.candidateList, func(i, j int) bool { return a.candidateList[i].ID < a.candidateList[j].ID })

	manifests := append(append([]LinkManifest(nil), a.ev.Manifests...), a.ev.LinkManifests...)
	for i := range manifests {
		manifests[i] = normalizeManifest(manifests[i])
	}
	for _, wrapped := range a.ev.SignedManifests {
		manifest := wrapped.Manifest
		if isEmptyManifest(manifest) {
			manifest = wrapped.LinkManifest
		}
		if manifest.Signature == nil {
			manifest.Signature = wrapped.Signature
		}
		if wrapped.Signed && manifest.Signature == nil {
			manifest.Signature = true
		}
		manifests = append(manifests, normalizeManifest(manifest))
	}
	a.manifests = dedupeManifests(manifests)

	summaries := append(append(append([]Object(nil), a.ev.Summaries...), a.ev.Objects...), a.ev.SymbolSummaries...)
	for _, wrapped := range a.ev.SignedSummaries {
		obj := wrapped.Summary
		if isEmptyObject(obj) {
			obj = wrapped.Object
		}
		if isEmptyID(obj.ID) {
			obj.ID = wrapped.ObjectID
		}
		if obj.Signature == nil {
			obj.Signature = wrapped.Signature
		}
		if wrapped.Signed && obj.Signature == nil {
			obj.Signature = true
		}
		summaries = append(summaries, normalizeObject(obj))
	}
	for i := range summaries {
		summaries[i] = normalizeObject(summaries[i])
	}
	for _, obj := range summaries {
		a.addSummary(obj)
	}

	marked := append(append(append([]ObjectID(nil), a.ev.Marked...), a.ev.MarkedObjects...), a.ev.Vulnerable...)
	seenMarked := make(map[string]bool)
	for _, id := range marked {
		id = normalizeID(id)
		key := idKey(id)
		if !seenMarked[key] {
			a.marked = append(a.marked, id)
			seenMarked[key] = true
		}
	}
	sortIDs(a.marked)

	allowListProvided := a.ev.AllowedConfigs != nil || a.ev.Allowed != nil || a.ev.DeployerAllowedConfigs != nil
	explicitAllowed := append(append(append([]string(nil), a.ev.AllowedConfigs...), a.ev.Allowed...), a.ev.DeployerAllowedConfigs...)
	allowedSet := make(map[string]bool)
	for _, config := range explicitAllowed {
		config = strings.TrimSpace(config)
		if config != "" {
			allowedSet[config] = true
		}
	}
	if len(allowedSet) == 0 && !allowListProvided {
		for _, candidate := range a.candidateList {
			if candidate.Allowed || candidate.AllowedByDeployer || candidate.DeployerAllowed {
				allowedSet[candidate.ID] = true
			}
		}
		// An omitted allowed list is treated as "all candidate configurations"
		// only when the host supplied no explicit allow bit at all.
		if len(allowedSet) == 0 {
			for _, candidate := range a.candidateList {
				allowedSet[candidate.ID] = true
			}
		}
	}
	for config := range allowedSet {
		a.allowed = append(a.allowed, config)
	}
	sort.Strings(a.allowed)
}

func normalizeCandidate(candidate CandidateConfig) CandidateConfig {
	candidate.ID = strings.TrimSpace(firstNonEmpty(candidate.ID, candidate.Config, candidate.BuildConfig, candidate.Configuration))
	candidate.Tags = append(append([]string(nil), candidate.Tags...), candidate.BuildTags...)
	candidate.BuildTags = nil
	if candidate.Signed && !signaturePresent(candidate.Signature) {
		candidate.Signature = true
	}
	if candidate.ID == "" && candidate.Manifest != nil {
		candidate.ID = firstNonEmpty(candidate.Manifest.Config, candidate.Manifest.BuildConfig, candidate.Manifest.Configuration, candidate.Manifest.ID)
	}
	if candidate.Manifest != nil {
		manifest := normalizeManifest(*candidate.Manifest)
		candidate.Manifest = &manifest
	}
	return candidate
}

func normalizeManifest(manifest LinkManifest) LinkManifest {
	if manifest.Signed && !signaturePresent(manifest.Signature) {
		manifest.Signature = true
	}
	manifest.Config = strings.TrimSpace(firstNonEmpty(manifest.Config, manifest.BuildConfig, manifest.Configuration))
	manifest.ID = strings.TrimSpace(manifest.ID)
	groups := manifest.ArchiveGroups
	if len(groups) == 0 {
		groups = manifest.Groups
	}
	if len(groups) == 0 && len(manifest.Objects) > 0 {
		groups = []ArchiveGroup{{ID: "objects", Members: manifest.Objects}}
	}
	manifest.ArchiveGroups = groups
	manifest.Groups = nil
	for i := range manifest.ArchiveGroups {
		group := &manifest.ArchiveGroups[i]
		if len(group.Members) == 0 {
			group.Members = append([]ObjectID(nil), group.Objects...)
		}
		group.Objects = nil
		group.ID = firstNonEmpty(group.ID, group.Name, fmt.Sprintf("group-%d", i))
		for j := range group.Members {
			group.Members[j] = normalizeID(group.Members[j])
		}
	}
	return manifest
}

func normalizeObject(obj Object) Object {
	if obj.Signed && !signaturePresent(obj.Signature) {
		obj.Signature = true
	}
	if isEmptyID(obj.ID) {
		obj.ID = obj.ObjectID
	}
	obj.ID = normalizeID(obj.ID)
	if len(obj.Definitions) > 0 {
		obj.Symbols = append(obj.Symbols, obj.Definitions...)
	}
	obj.Definitions = nil
	return obj
}

func (a *analyzer) addSummary(obj Object) {
	key := idKey(obj.ID)
	if _, exists := a.exact[key]; !exists {
		a.exact[key] = obj
	}
	if obj.ID.Compiler != "" || obj.ID.SourceDigest != "" {
		member := memberKey(obj.ID)
		a.byMember[member] = append(a.byMember[member], obj)
	}
	a.summaries = append(a.summaries, obj)
}

func isEmptyID(id ObjectID) bool {
	id = normalizeID(id)
	return id.Compiler == "" && id.SourceDigest == "" && id.BuildConfig == ""
}

func isEmptyObject(obj Object) bool {
	return isEmptyID(obj.ID) && isEmptyID(obj.ObjectID) && len(obj.Symbols) == 0 && len(obj.References) == 0
}

func isEmptyManifest(manifest LinkManifest) bool {
	return manifest.ID == "" && manifest.Config == "" && manifest.BuildConfig == "" && manifest.Configuration == "" &&
		len(manifest.EntrySymbols) == 0 && len(manifest.Entry) == 0 &&
		len(manifest.Entries) == 0 && len(manifest.EntryRefs) == 0 &&
		len(manifest.ArchiveGroups) == 0 && len(manifest.Groups) == 0
}

func dedupeManifests(manifests []LinkManifest) []LinkManifest {
	out := make([]LinkManifest, 0, len(manifests))
	seen := make(map[string]bool)
	for _, manifest := range manifests {
		key := manifest.Config + "\x00" + manifest.ID
		if key == "\x00" {
			if len(manifests) == 1 {
				out = append(out, manifest)
			}
			continue
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, manifest)
		}
	}
	return out
}

func (a *analyzer) evaluateConfig(config string) ConfigProof {
	proof := ConfigProof{
		Config:           config,
		EvidenceComplete: true,
	}
	candidate, ok := a.candidates[config]
	if !ok {
		proof.Verdict = ConfigUnknown
		proof.EvidenceComplete = false
		proof.MissingEvidence = []string{"signed candidate configuration " + config}
		proof.Notes = append(proof.Notes, "deployer-allowed configuration has no signed candidate")
		return proof
	}

	ctx := &configEvaluator{
		a:          a,
		config:     config,
		tags:       append(append([]string(nil), candidate.Tags...), candidate.ActiveConditions...),
		conditions: newConditionEvaluator(append(append([]string(nil), candidate.Tags...), candidate.ActiveConditions...)),
		proof:      proof,
		issueSeen:  make(map[string]bool),
		extract:    make(map[string]*extractedObject),
	}
	ctx.proof = proof

	candidateSignatureOK := a.signatureOK(candidatePayload(candidate), candidate.SignatureOr(candidate.Signed), "candidate "+config)
	if !candidateSignatureOK {
		ctx.issue("signature", "signed candidate configuration "+config)
	}
	if candidate.LinkabilityKnown && candidate.Linkable {
		ctx.expectLinkable = true
	}
	manifest, ok, err := a.findManifest(config, candidate)
	if err != nil {
		ctx.issue("manifest", err.Error())
	}
	if !ok && !(candidate.LinkabilityKnown && !candidate.Linkable) {
		ctx.issue("manifest", "signed link manifest for "+config)
	}
	if candidate.LinkabilityKnown && !candidate.Linkable {
		if ok && manifest.LinkabilityKnown && manifest.Linkable {
			ctx.issue("linkability", "candidate and link manifest disagree about linkability")
			ctx.finish(ConfigUnknown, false, "signed linkability declarations conflict")
		} else if !candidateSignatureOK {
			ctx.finish(ConfigUnknown, false, "cannot trust an unlinkable declaration with invalid evidence")
		} else {
			ctx.finish(ConfigNotLinkable, false, "signed candidate configuration is not linkable")
		}
		return ctx.proof
	}
	if ok && !a.signatureOK(manifestPayload(manifest), manifest.Signature, "manifest "+config) {
		ctx.issue("signature", "signed link manifest for "+config)
	}
	if ok && manifest.LinkabilityKnown && !manifest.Linkable {
		if ctx.expectLinkable {
			ctx.issue("linkability", "candidate and link manifest disagree about linkability")
			ctx.finish(ConfigUnknown, false, "signed linkability declarations conflict")
		} else if len(ctx.issues) > 0 {
			ctx.finish(ConfigUnknown, false, "cannot trust an unlinkable declaration with invalid evidence")
		} else {
			ctx.finish(ConfigNotLinkable, false, "signed link manifest is not linkable")
		}
		return ctx.proof
	}
	if ok && manifest.LinkabilityKnown && manifest.Linkable {
		ctx.expectLinkable = true
	}
	if !ok {
		ctx.finish(ConfigUnknown, false, "signed link manifest is missing")
		return ctx.proof
	}
	if manifest.Incomplete {
		ctx.issue("manifest", "link manifest is marked incomplete")
	}
	if ok, err := ctx.conditions.evalAll("manifest", manifest.Conditions); err != nil {
		ctx.issue("condition", err.Error())
	} else if !ok {
		if len(ctx.issues) > 0 {
			ctx.finish(ConfigUnknown, false, "cannot trust inactive-condition evidence with invalid signatures")
		} else {
			ctx.finish(ConfigNotAffected, true, "manifest condition is inactive")
		}
		return ctx.proof
	}
	if manifest.Condition != "" {
		active, err := ctx.conditions.eval("manifest", manifest.Condition)
		if err != nil {
			ctx.issue("condition", err.Error())
		} else if !active {
			if len(ctx.issues) > 0 {
				ctx.finish(ConfigUnknown, false, "cannot trust inactive-condition evidence with invalid signatures")
			} else {
				ctx.finish(ConfigNotAffected, true, "manifest condition is inactive")
			}
			return ctx.proof
		}
	}

	ctx.entries = manifestEntries(manifest)
	ctx.groups = manifest.ArchiveGroups
	if len(ctx.groups) == 0 && len(ctx.entries) > 0 && len(a.summaries) == 0 {
		ctx.issue("groups", "archive groups for "+config)
	}
	if len(ctx.groups) == 0 && len(a.summaries) > 0 {
		group := ArchiveGroup{ID: "all-summaries"}
		seen := make(map[string]bool)
		for _, obj := range a.summaries {
			if isEmptyID(obj.ID) {
				continue
			}
			objConfig := normalizeID(obj.ID).BuildConfig
			if objConfig != "" && objConfig != config {
				continue
			}
			key := idKey(obj.ID)
			if !seen[key] {
				group.Members = append(group.Members, obj.ID)
				seen[key] = true
			}
		}
		if len(group.Members) > 0 {
			ctx.groups = []ArchiveGroup{group}
		} else if len(ctx.entries) > 0 {
			ctx.issue("groups", "archive groups for "+config)
		}
	}
	ctx.extractObjects()
	ctx.bindAndReach()
	return ctx.proof
}

func (m LinkManifest) SignatureOr(signed bool) any {
	if m.Signature != nil {
		return m.Signature
	}
	return signed
}

func (c CandidateConfig) SignatureOr(signed bool) any {
	if c.Signature != nil {
		return c.Signature
	}
	return signed
}

func manifestEntries(manifest LinkManifest) []Reference {
	refs := make([]Reference, 0)
	for _, name := range append(append([]string(nil), manifest.EntrySymbols...), manifest.Entry...) {
		name = strings.TrimSpace(name)
		if name != "" {
			refs = append(refs, Reference{Symbol: name})
		}
	}
	refs = append(refs, manifest.Entries...)
	refs = append(refs, manifest.EntryRefs...)
	refs = append(refs, manifest.EntryReferences...)
	return normalizeReferences(refs)
}

func (a *analyzer) findManifest(config string, candidate CandidateConfig) (LinkManifest, bool, error) {
	if candidate.Manifest != nil {
		return normalizeManifest(*candidate.Manifest), true, nil
	}
	wanted := firstNonEmpty(candidate.ManifestID, config)
	var matches []LinkManifest
	for _, manifest := range a.manifests {
		id := firstNonEmpty(manifest.Config, manifest.ID)
		if id == wanted || (wanted == "" && id == "") {
			matches = append(matches, manifest)
		}
	}
	if len(matches) == 0 && len(a.manifests) == 1 {
		manifest := a.manifests[0]
		if firstNonEmpty(manifest.Config, manifest.ID) == "" || len(a.candidateList) <= 1 {
			return manifest, true, nil
		}
	}
	if len(matches) == 0 {
		return LinkManifest{}, false, nil
	}
	if len(matches) > 1 {
		return LinkManifest{}, false, fmt.Errorf("multiple link manifests for %s", config)
	}
	return matches[0], true, nil
}

func (c *configEvaluator) finish(verdict ConfigVerdict, linkable bool, reason string) {
	c.proof.Verdict = verdict
	c.proof.Linkable = linkable
	c.proof.Conditions = append([]ConditionResult(nil), c.conditions.results...)
	c.proof.MissingEvidence = uniqueStrings(c.issues)
	c.proof.EvidenceComplete = len(c.issues) == 0
	if reason != "" && len(c.proof.Notes) == 0 {
		c.proof.Notes = append(c.proof.Notes, reason)
	}
	sortIDs(c.proof.Extracted)
	sort.Slice(c.proof.Bindings, func(i, j int) bool { return c.proof.Bindings[i].Index < c.proof.Bindings[j].Index })
	reachable := make(map[string]ObjectID)
	marked := make(map[string]ObjectID)
	for _, set := range c.proof.Bindings {
		if !set.Linkable {
			continue
		}
		for _, id := range set.Reachable {
			reachable[idKey(id)] = id
		}
		for _, id := range set.MarkedReachable {
			marked[idKey(id)] = id
		}
	}
	c.proof.Reachable = c.proof.Reachable[:0]
	c.proof.MarkedReachable = c.proof.MarkedReachable[:0]
	for _, id := range reachable {
		c.proof.Reachable = append(c.proof.Reachable, id)
	}
	for _, id := range marked {
		c.proof.MarkedReachable = append(c.proof.MarkedReachable, id)
	}
	sortIDs(c.proof.Reachable)
	sortIDs(c.proof.MarkedReachable)
	if len(c.proof.Bindings) > 0 {
		c.proof.Groups = append([]GroupSelection(nil), c.proof.Bindings[0].Groups...)
	}
}

func (c *configEvaluator) issue(kind, message string) {
	key := kind + "\x00" + message
	if c.issueSeen[key] {
		return
	}
	c.issueSeen[key] = true
	c.issues = append(c.issues, message)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeReferences(refs []Reference) []Reference {
	out := make([]Reference, 0, len(refs))
	for _, ref := range refs {
		ref.Symbol = firstNonEmpty(strings.TrimSpace(ref.Symbol), strings.TrimSpace(ref.Name), strings.TrimSpace(ref.Target))
		if ref.Symbol == "" {
			continue
		}
		if ref.Condition == "" {
			ref.Condition = ref.When
		}
		ref.When = ""
		ref.Name = ""
		ref.Target = ""
		if ref.Weak {
			ref.Optional = true
		}
		out = append(out, ref)
	}
	return out
}

func stringRefs(refs []string) []Reference {
	out := make([]Reference, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			out = append(out, Reference{Symbol: ref})
		}
	}
	return out
}

func (c *configEvaluator) extractObjects() {
	unresolved := make(map[string]bool)
	for _, ref := range c.entries {
		active, err := c.refActive("entry", ref)
		if err != nil {
			c.issue("condition", err.Error())
			continue
		}
		if active {
			unresolved[ref.Symbol] = true
		}
	}

	for groupIndex, group := range c.groups {
		groupName := firstNonEmpty(group.ID, group.Name, fmt.Sprintf("group-%d", groupIndex))
		groupActive, err := c.conditions.eval("group "+groupName, group.Condition)
		if err != nil {
			c.issue("condition", err.Error())
			continue
		}
		if !groupActive {
			continue
		}
		conditionsOK, err := c.conditions.evalAll("group "+groupName, group.Conditions)
		if err != nil {
			c.issue("condition", err.Error())
			continue
		}
		if !conditionsOK {
			continue
		}
		if group.Incomplete {
			c.issue("group", "archive group "+groupName+" is marked incomplete")
		}
		members := group.Members
		if len(members) == 0 {
			members = group.Objects
		}
		for {
			pending := cloneSet(unresolved)
			if len(pending) == 0 {
				break
			}
			changed := false
			for _, rawID := range members {
				id := normalizeID(rawID)
				if id.BuildConfig == "" {
					id.BuildConfig = c.config
					id.Config = c.config
				}
				key := idKey(id)
				if _, exists := c.extract[key]; exists {
					continue
				}
				obj, effective, ok := c.a.resolveSummary(c.config, id)
				if !ok {
					c.issue("summary", "signed symbol summary for "+id.String())
					continue
				}
				if !c.a.signatureOK(objectPayload(obj), obj.Signature, "symbol summary "+effective.String()) {
					c.issue("signature", "signed symbol summary for "+effective.String())
					continue
				}
				activated := c.activateObject(obj, effective)
				satisfied := make([]string, 0)
				for _, def := range activated.defs {
					if pending[def.name] {
						satisfied = append(satisfied, def.name)
					}
				}
				if len(satisfied) == 0 {
					continue
				}
				sort.Strings(satisfied)
				c.extract[key] = activated
				c.ordered = append(c.ordered, activated)
				newRefs := make(map[string]bool)
				for _, ref := range activated.refs {
					newRefs[ref.Symbol] = true
				}
				for _, def := range activated.defs {
					for _, ref := range def.refs {
						newRefs[ref.Symbol] = true
					}
				}
				var newRefNames []string
				for name := range newRefs {
					newRefNames = append(newRefNames, name)
				}
				sort.Strings(newRefNames)
				c.proof.Extraction = append(c.proof.Extraction, ExtractionStep{
					Group:     groupName,
					Index:     len(c.proof.Extraction),
					Object:    activated.id,
					Satisfied: satisfied,
					NewRefs:   newRefNames,
				})
				changed = true
			}
			unresolved = c.unresolvedRefs()
			if !changed {
				break
			}
		}
	}

	for _, obj := range c.ordered {
		c.proof.Extracted = append(c.proof.Extracted, obj.id)
	}
	sortIDs(c.proof.Extracted)
	if len(unresolved) > 0 && len(c.issues) == 0 {
		c.proof.Notes = append(c.proof.Notes, "some references remain unresolved")
	}
}

func cloneSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (c *configEvaluator) unresolvedRefs() map[string]bool {
	covered := make(map[string]bool)
	for _, obj := range c.ordered {
		for _, def := range obj.defs {
			covered[def.name] = true
		}
	}
	out := make(map[string]bool)
	for _, ref := range c.allRefs() {
		active, err := c.refActive("reference "+ref.Symbol, ref)
		if err != nil {
			c.issue("condition", err.Error())
			continue
		}
		if active && !covered[ref.Symbol] {
			out[ref.Symbol] = true
		}
	}
	return out
}

func (c *configEvaluator) activateObject(obj Object, effectiveID ObjectID) *extractedObject {
	active := &extractedObject{id: effectiveID, summary: obj}
	objActive, err := c.conditions.evalAll("object "+effectiveID.String(), obj.Conditions)
	if err != nil {
		c.issue("condition", err.Error())
		return active
	}
	if !objActive {
		return active
	}
	if obj.Incomplete {
		c.issue("summary", "symbol summary for "+effectiveID.String()+" is marked incomplete")
	}
	active.refs = append(active.refs, normalizeReferences(obj.References)...)
	active.refs = append(active.refs, stringRefs(obj.Refs)...)

	for _, symbol := range obj.Symbols {
		symbol = normalizeSymbol(symbol)
		if symbol.Name == "" {
			continue
		}
		ok, err := c.conditions.eval("symbol "+effectiveID.String()+"."+symbol.Name, symbol.Condition)
		if err != nil {
			c.issue("condition", err.Error())
			continue
		}
		if !ok {
			continue
		}
		ok, err = c.conditions.evalAll("symbol "+effectiveID.String()+"."+symbol.Name, symbol.Conditions)
		if err != nil {
			c.issue("condition", err.Error())
			continue
		}
		if !ok {
			continue
		}
		kind := symbol.Binding
		if kind == "" {
			kind = symbol.Kind
		}
		if kind == "" {
			switch {
			case symbol.Group != "" || symbol.GroupID != "":
				kind = BindingGroup
			case symbol.Weak:
				kind = BindingWeak
			default:
				kind = BindingStrong
			}
		}
		kind = BindingKind(strings.ToLower(string(kind)))
		switch kind {
		case "choose_one", "choose-one", "choice", "same_group", "same-group":
			kind = BindingGroup
		case "global":
			kind = BindingStrong
		}
		if kind != BindingStrong && kind != BindingWeak && kind != BindingGroup {
			c.issue("symbol", fmt.Sprintf("unknown binding %q for %s", kind, symbol.Name))
			continue
		}
		group := firstNonEmpty(symbol.Group, symbol.GroupID)
		if kind == BindingGroup && group == "" {
			group = "default"
		}
		refs := append(normalizeReferences(symbol.References), stringRefs(symbol.Refs)...)
		active.defs = append(active.defs, symbolDef{
			name:   symbol.Name,
			kind:   kind,
			group:  group,
			object: effectiveID,
			refs:   refs,
		})
	}
	return active
}

func normalizeSymbol(symbol Symbol) Symbol {
	if symbol.Binding == "" {
		symbol.Binding = symbol.Kind
	}
	if symbol.Binding == "" {
		symbol.Binding = symbol.Linkage
	}
	if symbol.Group == "" {
		symbol.Group = symbol.GroupID
	}
	if symbol.Group == "" {
		symbol.Group = symbol.ChoiceGroup
	}
	if symbol.Binding == "" {
		switch {
		case symbol.Strong:
			symbol.Binding = BindingStrong
		case symbol.Weak:
			symbol.Binding = BindingWeak
		case symbol.Group != "":
			symbol.Binding = BindingGroup
		}
	}
	return symbol
}

func (c *configEvaluator) refActive(owner string, ref Reference) (bool, error) {
	ok, err := c.conditions.eval(owner, ref.Condition)
	if err != nil || !ok {
		return false, err
	}
	return c.conditions.evalAll(owner, ref.Conditions)
}

func (c *configEvaluator) allRefs() []Reference {
	refs := append([]Reference(nil), c.entries...)
	for _, obj := range c.ordered {
		refs = append(refs, obj.refs...)
		for _, def := range obj.defs {
			refs = append(refs, def.refs...)
		}
	}
	return refs
}

func (a *analyzer) resolveSummary(config string, id ObjectID) (Object, ObjectID, bool) {
	id = normalizeID(id)
	effective := id
	if effective.BuildConfig == "" {
		effective.BuildConfig = config
		effective.Config = config
	}
	if obj, ok := a.exact[idKey(effective)]; ok {
		return obj, effective, true
	}
	if obj, ok := a.exact[idKey(id)]; ok && id.BuildConfig == effective.BuildConfig {
		return obj, effective, true
	}
	candidates := a.byMember[memberKey(effective)]
	if len(candidates) == 0 && id.BuildConfig == "" {
		candidates = a.byMember[memberKey(id)]
	}
	if len(candidates) == 0 {
		return Object{}, effective, false
	}
	var preferred []Object
	for _, candidate := range candidates {
		if candidate.ID.BuildConfig == "" || candidate.ID.BuildConfig == effective.BuildConfig {
			preferred = append(preferred, candidate)
		}
	}
	if len(preferred) == 0 && len(candidates) == 1 {
		preferred = candidates
	}
	if len(preferred) != 1 {
		return Object{}, effective, false
	}
	return preferred[0], effective, true
}

func (a *analyzer) signatureOK(payload any, signature any, owner string) bool {
	_ = owner
	if !a.require {
		return true
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	digest := sha256Hex(data)
	if sig, ok := asSignatureStruct(signature); ok {
		if sig.PayloadDigest != "" && !equalDigest(sig.PayloadDigest, digest) {
			return false
		}
		if !sig.Signed && strings.TrimSpace(sig.Value) == "" {
			return false
		}
	}
	if a.verifier != nil {
		return a.verifier.Verify(data, signature) == nil
	}
	return signaturePresent(signature)
}

func asSignatureStruct(value any) (Signature, bool) {
	switch sig := value.(type) {
	case Signature:
		return sig, true
	case *Signature:
		if sig == nil {
			return Signature{}, false
		}
		return *sig, true
	case map[string]any:
		data, err := json.Marshal(sig)
		if err != nil {
			return Signature{}, false
		}
		var out Signature
		if json.Unmarshal(data, &out) != nil {
			return Signature{}, false
		}
		return out, true
	default:
		return Signature{}, false
	}
}

func signaturePresent(value any) bool {
	switch sig := value.(type) {
	case nil:
		return false
	case Signature:
		return sig.Signed || strings.TrimSpace(sig.Value) != ""
	case *Signature:
		return sig != nil && (sig.Signed || strings.TrimSpace(sig.Value) != "")
	case string:
		return strings.TrimSpace(sig) != ""
	case []byte:
		return len(sig) > 0
	case bool:
		return sig
	case json.RawMessage:
		return len(sig) > 0 && string(sig) != "null" && string(sig) != `""`
	default:
		return true
	}
}

func candidatePayload(candidate CandidateConfig) any {
	copy := candidate
	copy.Signature = nil
	copy.Manifest = nil
	copy.Signed = false
	return copy
}

func manifestPayload(manifest LinkManifest) any {
	copy := manifest
	copy.Signature = nil
	copy.Signed = false
	return copy
}

func objectPayload(obj Object) any {
	copy := obj
	copy.Signature = nil
	copy.Signed = false
	return copy
}

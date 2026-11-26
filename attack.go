package hsm

import (
	"fmt"
	"sort"
	"strings"
)

// MaxSearchDepth is the hard upper bound for the API-level attack search.
const MaxSearchDepth = 6

// AttackConfig describes the initial HSM state and attacker capabilities for
// the bounded API-level search.
//
// Permissions.APIs is the exact API subset available to the attacker. An
// empty APISet grants no operations. If Targets is empty, every key with
// CKA_SENSITIVE=true becomes a target. KnownKeys are the key objects whose
// material the attacker already possesses; non-sensitive objects are known
// automatically.
type AttackConfig struct {
	Keys        []KeySpec
	Policy      Policy
	Permissions Permissions
	Targets     []ID
	KnownKeys   []ID
	MaxDepth    int
	MaxStates   int
}

// AttackStep is one concrete API call in an attack witness.
type AttackStep struct {
	Operation Operation       `json:"operation"`
	KeyID     ID              `json:"key_id,omitempty"`
	TargetID  ID              `json:"target_id,omitempty"`
	ResultID  ID              `json:"result_id,omitempty"`
	Mechanism Mechanism       `json:"mechanism,omitempty"`
	Template  *KeyTemplate    `json:"template,omitempty"`
	Patch     *AttributePatch `json:"patch,omitempty"`
	Blob      []byte          `json:"blob,omitempty"`
}

// Summary returns a compact human-readable description of the step.
func (s AttackStep) Summary() string {
	switch s.Operation {
	case OpWrapKey:
		return fmt.Sprintf("WrapKey(%s -> wraps %s) using %s", s.KeyID, s.TargetID, s.Mechanism)
	case OpUnwrapKey:
		return fmt.Sprintf("UnwrapKey(%s) using %s creates %s", s.KeyID, s.Mechanism, s.ResultID)
	case OpEncrypt:
		return fmt.Sprintf("Encrypt(%s) using %s", s.KeyID, s.Mechanism)
	case OpDecrypt:
		return fmt.Sprintf("Decrypt(%s) using %s", s.KeyID, s.Mechanism)
	case OpDeriveKey:
		return fmt.Sprintf("DeriveKey(%s) using %s creates %s", s.KeyID, s.Mechanism, s.ResultID)
	case OpSetAttributeValue:
		return fmt.Sprintf("SetAttributeValue(%s)", s.KeyID)
	case OpCopyObject:
		return fmt.Sprintf("CopyObject(%s) creates %s", s.KeyID, s.ResultID)
	default:
		return string(s.Operation)
	}
}

// AttackPath is a shortest API sequence and the protected keys it exposes.
type AttackPath struct {
	Steps  []AttackStep `json:"steps"`
	Depth  int          `json:"depth"`
	Leaked []ID         `json:"leaked"`
}

// Explain renders a numbered attack trace.
func (p AttackPath) Explain() string {
	if len(p.Steps) == 0 {
		return "target material is already in attacker knowledge"
	}
	var b strings.Builder
	for i, step := range p.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step.Summary())
	}
	if len(p.Leaked) > 0 {
		fmt.Fprintf(&b, "leaked: %s", joinIDs(p.Leaked))
	}
	return strings.TrimSpace(b.String())
}

// AttackResult is returned by SearchAttacks.
type AttackResult struct {
	Found       bool        `json:"found"`
	Path        *AttackPath `json:"path,omitempty"`
	Explored    int         `json:"explored"`
	Transitions int         `json:"transitions"`
	Truncated   bool        `json:"truncated"`
}

// EnumerationResult records a bounded, deduplicated exploration of the API
// state space. NoLeak is only a proof for the configured depth and key set
// when Exhausted is true and Truncated is false.
type EnumerationResult struct {
	MaxDepth    int          `json:"max_depth"`
	Explored    int          `json:"explored"`
	Transitions int          `json:"transitions"`
	Leaks       []AttackPath `json:"leaks,omitempty"`
	NoLeak      bool         `json:"no_leak"`
	Exhausted   bool         `json:"exhausted"`
	Truncated   bool         `json:"truncated"`
}

// HardeningReport compares the same bounded state space under the insecure
// compatibility policy and under the hardened zero-value policy.
type HardeningReport struct {
	Insecure EnumerationResult `json:"insecure"`
	Hardened EnumerationResult `json:"hardened"`
	Secure   bool              `json:"secure"`
}

type attackArtifact struct {
	blob         []byte
	mechanism    Mechanism
	wrappingKey  ID
	sourceKey    ID
	sourceOrigin ID
	sourceClass  ObjectClass
	sourceType   KeyType
	sourceAttrs  Attributes
}

type attackState struct {
	hsm          *HSM
	knownOrigins map[ID]bool
	artifacts    []attackArtifact
	targets      map[ID]ID
}

type attackAction struct {
	op       Operation
	keyID    ID
	targetID ID
	mech     Mechanism
	template *KeyTemplate
	patch    *AttributePatch
	artifact *attackArtifact
}

type attackNode struct {
	state  *attackState
	parent *attackNode
	step   AttackStep
	depth  int
	leaked []ID
}

// FindAttack returns the shortest attack path, or nil when no target leaks
// within the configured bound.
func FindAttack(cfg AttackConfig) (*AttackPath, error) {
	result, err := SearchAttacks(cfg)
	if err != nil {
		return nil, err
	}
	return result.Path, nil
}

// FindShortestAttack is an alias for FindAttack.
func FindShortestAttack(cfg AttackConfig) (*AttackPath, error) { return FindAttack(cfg) }

// SearchAttacks searches breadth-first and stops at the first shortest leak.
func SearchAttacks(cfg AttackConfig) (AttackResult, error) {
	normalized, root, targets, err := prepareAttack(cfg)
	if err != nil {
		return AttackResult{}, err
	}
	if leaks := root.leakedTargets(targets); len(leaks) > 0 {
		path := &AttackPath{Depth: 0, Leaked: leaks}
		return AttackResult{Found: true, Path: path, Explored: 1}, nil
	}

	frontier := []*attackNode{{state: root, depth: 0}}
	visited := map[string]bool{root.fingerprint(): true}
	result := AttackResult{Explored: 1}
	for depth := 0; depth < normalized.MaxDepth && len(frontier) > 0; depth++ {
		var next []*attackNode
		for _, node := range frontier {
			actions := enumerateActions(node.state)
			for _, action := range actions {
				if len(visited) >= normalized.MaxStates {
					result.Truncated = true
					return result, nil
				}
				child, step, err := applyAction(node.state, action)
				if err != nil {
					continue
				}
				fp := child.fingerprint()
				if visited[fp] {
					continue
				}
				visited[fp] = true
				result.Transitions++
				leaked := child.leakedTargets(targets)
				childNode := &attackNode{
					state: child, parent: node, step: step,
					depth: node.depth + 1, leaked: leaked,
				}
				if len(leaked) > 0 {
					path := materializePath(childNode)
					result.Found = true
					result.Path = &path
					result.Explored = len(visited)
					return result, nil
				}
				next = append(next, childNode)
			}
		}
		frontier = next
		result.Explored = len(visited)
	}
	result.Explored = len(visited)
	return result, nil
}

// EnumerateAttacks exhaustively explores the deduplicated bounded state space
// and records the shortest path for every leaked target. It is intended for
// small key sets and depths.
func EnumerateAttacks(cfg AttackConfig) (EnumerationResult, error) {
	normalized, root, targets, err := prepareAttack(cfg)
	if err != nil {
		return EnumerationResult{}, err
	}
	result := EnumerationResult{MaxDepth: normalized.MaxDepth}
	leakByTarget := make(map[ID]AttackPath)
	recordLeaks := func(node *attackNode) {
		for _, target := range node.leaked {
			if _, exists := leakByTarget[target]; exists {
				continue
			}
			leakByTarget[target] = materializePath(node)
		}
	}

	rootNode := &attackNode{state: root, depth: 0}
	recordLeaks(rootNode)
	frontier := []*attackNode{rootNode}
	visited := map[string]bool{root.fingerprint(): true}
	for depth := 0; depth < normalized.MaxDepth && len(frontier) > 0; depth++ {
		var next []*attackNode
		for _, node := range frontier {
			if allTargetsKnown(node.state, targets) {
				continue
			}
			for _, action := range enumerateActions(node.state) {
				if len(visited) >= normalized.MaxStates {
					result.Truncated = true
					break
				}
				child, step, err := applyAction(node.state, action)
				if err != nil {
					continue
				}
				fp := child.fingerprint()
				if visited[fp] {
					continue
				}
				visited[fp] = true
				result.Transitions++
				childNode := &attackNode{
					state: child, parent: node, step: step,
					depth: node.depth + 1, leaked: child.leakedTargets(targets),
				}
				recordLeaks(childNode)
				next = append(next, childNode)
			}
			if result.Truncated {
				break
			}
		}
		if result.Truncated {
			break
		}
		frontier = next
	}

	result.Explored = len(visited)
	result.Truncated = result.Truncated || len(visited) >= normalized.MaxStates
	result.Exhausted = !result.Truncated
	result.NoLeak = len(leakByTarget) == 0
	for _, path := range leakByTarget {
		result.Leaks = append(result.Leaks, path)
	}
	sort.Slice(result.Leaks, func(i, j int) bool {
		if result.Leaks[i].Depth != result.Leaks[j].Depth {
			return result.Leaks[i].Depth < result.Leaks[j].Depth
		}
		return joinIDs(result.Leaks[i].Leaked) < joinIDs(result.Leaks[j].Leaked)
	})
	return result, nil
}

// VerifyHardening exhaustively checks that the same initial key set, caller
// permissions, and depth bound have at least one leak under InsecurePolicy and
// no leak under HardenedPolicy.
func VerifyHardening(cfg AttackConfig) (HardeningReport, error) {
	insecureCfg := cfg
	insecureCfg.Policy = InsecurePolicy()
	insecure, err := EnumerateAttacks(insecureCfg)
	if err != nil {
		return HardeningReport{}, err
	}
	hardenedCfg := cfg
	hardenedCfg.Policy = HardenedPolicy()
	hardened, err := EnumerateAttacks(hardenedCfg)
	if err != nil {
		return HardeningReport{}, err
	}
	return HardeningReport{
		Insecure: insecure,
		Hardened: hardened,
		Secure:   !insecure.NoLeak && hardened.NoLeak && hardened.Exhausted,
	}, nil
}

type preparedAttack struct {
	MaxDepth  int
	MaxStates int
}

func prepareAttack(cfg AttackConfig) (preparedAttack, *attackState, map[ID]ID, error) {
	normalized := preparedAttack{MaxDepth: cfg.MaxDepth, MaxStates: cfg.MaxStates}
	if normalized.MaxDepth == 0 {
		normalized.MaxDepth = MaxSearchDepth
	}
	if normalized.MaxDepth < 0 || normalized.MaxDepth > MaxSearchDepth {
		return normalized, nil, nil, fmt.Errorf("%w: MaxDepth must be between 0 and %d",
			ErrPolicy, MaxSearchDepth)
	}
	if normalized.MaxStates == 0 {
		normalized.MaxStates = 100000
	}
	if normalized.MaxStates < 1 {
		return normalized, nil, nil, fmt.Errorf("%w: MaxStates must be positive", ErrPolicy)
	}
	h, err := Load(cfg.Policy, cfg.Keys)
	if err != nil {
		return normalized, nil, nil, err
	}
	h.deterministic = true
	h.SetPermissions(cfg.Permissions)

	state := &attackState{
		hsm:          h,
		knownOrigins: make(map[ID]bool),
	}
	for _, id := range cfg.KnownKeys {
		key, ok := h.keys[id]
		if !ok {
			return normalized, nil, nil, fmt.Errorf("%w: known key %q", ErrNotFound, id)
		}
		state.knownOrigins[key.origin] = true
	}
	for _, key := range h.keys {
		if !key.spec.Attributes.Sensitive {
			state.knownOrigins[key.origin] = true
		}
	}
	state.refreshKnowledge()

	targetIDs := append([]ID(nil), cfg.Targets...)
	if len(targetIDs) == 0 {
		for id, key := range h.keys {
			if key.spec.Attributes.Sensitive {
				targetIDs = append(targetIDs, id)
			}
		}
		sort.Slice(targetIDs, func(i, j int) bool { return targetIDs[i] < targetIDs[j] })
	}
	if len(targetIDs) == 0 {
		return normalized, nil, nil, fmt.Errorf("%w: no protected target keys selected", ErrInvalidKey)
	}
	targets := make(map[ID]ID, len(targetIDs))
	for _, id := range targetIDs {
		key, ok := h.keys[id]
		if !ok {
			return normalized, nil, nil, fmt.Errorf("%w: target %q", ErrNotFound, id)
		}
		targets[id] = key.origin
	}
	state.targets = targets
	return normalized, state, targets, nil
}

func (s *attackState) clone() *attackState {
	clone := &attackState{
		hsm:          s.hsm.clone(),
		knownOrigins: make(map[ID]bool, len(s.knownOrigins)),
		artifacts:    append([]attackArtifact(nil), s.artifacts...),
		targets:      s.targets,
	}
	for id := range s.knownOrigins {
		clone.knownOrigins[id] = true
	}
	for i := range clone.artifacts {
		clone.artifacts[i].blob = append([]byte(nil), clone.artifacts[i].blob...)
	}
	return clone
}

func (s *attackState) refreshKnowledge() {
	s.hsm.mu.RLock()
	defer s.hsm.mu.RUnlock()
	for changed := true; changed; {
		changed = false
		knownMaterial := make(map[[32]byte]bool)
		for _, key := range s.hsm.keys {
			if s.knownOrigins[key.origin] {
				knownMaterial[key.materialFingerprint()] = true
			}
		}
		for _, key := range s.hsm.keys {
			if !key.spec.Attributes.Sensitive {
				if !s.knownOrigins[key.origin] {
					s.knownOrigins[key.origin] = true
					changed = true
				}
				knownMaterial[key.materialFingerprint()] = true
			}
			if knownMaterial[key.materialFingerprint()] && !s.knownOrigins[key.origin] {
				s.knownOrigins[key.origin] = true
				changed = true
			}
		}
		for _, artifact := range s.artifacts {
			wrapper, ok := s.hsm.keys[artifact.wrappingKey]
			if !ok {
				continue
			}
			knownWrapper := knownMaterial[wrapper.materialFingerprint()]
			if knownWrapper && !s.knownOrigins[artifact.sourceOrigin] {
				s.knownOrigins[artifact.sourceOrigin] = true
				changed = true
			}
		}
	}
}

func (s *attackState) leakedTargets(targets map[ID]ID) []ID {
	var leaked []ID
	for target, origin := range targets {
		if s.knownOrigins[origin] {
			leaked = append(leaked, target)
		}
	}
	sort.Slice(leaked, func(i, j int) bool { return leaked[i] < leaked[j] })
	return leaked
}

func allTargetsKnown(s *attackState, targets map[ID]ID) bool {
	for _, origin := range targets {
		if !s.knownOrigins[origin] {
			return false
		}
	}
	return true
}

func (s *attackState) fingerprint() string {
	s.hsm.mu.RLock()
	defer s.hsm.mu.RUnlock()

	ids := make([]ID, 0, len(s.hsm.keys))
	for id := range s.hsm.keys {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	for _, id := range ids {
		key := s.hsm.keys[id]
		fp := key.materialFingerprint()
		fmt.Fprintf(&b, "K|%s|%s|%s|%s|%s|%x\n",
			id, key.spec.Class, key.spec.Type, key.origin,
			key.spec.Attributes.String(), fp)
	}
	known := make([]string, 0, len(s.knownOrigins))
	for origin := range s.knownOrigins {
		known = append(known, string(origin))
	}
	sort.Strings(known)
	fmt.Fprintf(&b, "A|%s\n", strings.Join(known, ","))

	artifactSet := make(map[string]struct{})
	for _, artifact := range s.artifacts {
		artifactSet[fmt.Sprintf("%s|%s|%s|%s|%s|%s", artifact.sourceOrigin,
			artifact.wrappingKey, artifact.mechanism, artifact.sourceClass,
			artifact.sourceType, artifact.sourceAttrs.String())] = struct{}{}
	}
	artifactKeys := make([]string, 0, len(artifactSet))
	for key := range artifactSet {
		artifactKeys = append(artifactKeys, key)
	}
	sort.Strings(artifactKeys)
	for _, artifactKey := range artifactKeys {
		fmt.Fprintf(&b, "W|%s\n", artifactKey)
	}
	return b.String()
}

func materializePath(node *attackNode) AttackPath {
	var reversed []AttackStep
	for n := node; n != nil && n.parent != nil; n = n.parent {
		reversed = append(reversed, n.step)
	}
	steps := make([]AttackStep, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		steps = append(steps, reversed[i])
	}
	return AttackPath{
		Steps:  steps,
		Depth:  len(steps),
		Leaked: append([]ID(nil), node.leaked...),
	}
}

func enumerateActions(s *attackState) []attackAction {
	policy := s.hsm.policy
	perms := s.hsm.permissions
	keys := sortedKeys(s.hsm)
	actions := make([]attackAction, 0, len(keys)*6)

	// Attribute mutation and copying are only useful when they can change an
	// effective policy bit. In strict mode the role vector is immutable and
	// trusted promotion is disabled, so no such action is generated.
	if policy.AllowRoleMutation || policy.AllowTrustedPromotion {
		for _, key := range keys {
			if !perms.Allows(OpSetAttributeValue, key.spec.ID) {
				continue
			}
			if policy.AllowRoleMutation {
				maxPatch := attributePatchForMaxRoles(key.spec.Attributes, policy, perms)
				if !maxPatch.IsZero() {
					p := maxPatch
					actions = append(actions, attackAction{
						op: OpSetAttributeValue, keyID: key.spec.ID, patch: &p,
					})
				}
				for _, role := range rolePromotions {
					if role.get(key.spec.Attributes) {
						continue
					}
					attrs := key.spec.Attributes
					role.set(&attrs, true)
					patch := rolePatch(attrs, key.spec.Attributes)
					if patch.IsZero() {
						continue
					}
					p := patch
					actions = append(actions, attackAction{
						op: OpSetAttributeValue, keyID: key.spec.ID, patch: &p,
					})
				}
			}
			if !key.spec.Attributes.Trusted && perms.CanSetTrusted && policy.AllowTrustedPromotion {
				attrs := key.spec.Attributes
				attrs.Trusted = true
				patch := rolePatch(attrs, key.spec.Attributes)
				p := patch
				actions = append(actions, attackAction{
					op: OpSetAttributeValue, keyID: key.spec.ID, patch: &p,
				})
			}
		}

		for _, key := range keys {
			if !perms.Allows(OpCopyObject, key.spec.ID) {
				continue
			}
			templates := copyTemplates(key.spec, policy, perms)
			for i := range templates {
				t := templates[i]
				actions = append(actions, attackAction{
					op: OpCopyObject, keyID: key.spec.ID, template: &t,
				})
			}
		}
	}

	// Wrap operations.
	if perms.APIs.Has(OpWrapKey) {
		for _, wrapper := range keys {
			if !wrapper.spec.Attributes.Wrap || wrapper.spec.Type != TypeAES {
				continue
			}
			if !perms.Allows(OpWrapKey, wrapper.spec.ID) {
				continue
			}
			if policy.operationRoleAllowed(wrapper.spec.Attributes, OpWrapKey) != nil {
				continue
			}
			for _, target := range keys {
				if target.spec.Class == ClassPublicKey {
					continue
				}
				if !perms.Allows(OpWrapKey, target.spec.ID) {
					continue
				}
				if !target.spec.Attributes.Extractable && !policy.AllowWrapOfNonExtractable {
					continue
				}
				if target.spec.Attributes.WrapWithTrusted && !wrapper.spec.Attributes.Trusted &&
					!policy.AllowUntrustedWrapWithTrusted {
					continue
				}
				for _, mech := range []Mechanism{AESKeyWrap, AESCBC, AESGCM} {
					if s.hasArtifact(target.origin, wrapper.spec.ID, mech, target.spec.Attributes) {
						continue
					}
					actions = append(actions, attackAction{
						op: OpWrapKey, keyID: wrapper.spec.ID, targetID: target.spec.ID, mech: mech,
					})
				}
			}
		}
	}

	// Decrypt a target-origin wrapped blob with an eligible key.
	if perms.APIs.Has(OpDecrypt) {
		for i := range s.artifacts {
			artifact := s.artifacts[i]
			if !s.isTargetOrigin(artifact.sourceOrigin) {
				continue
			}
			for _, key := range keys {
				if !key.spec.Attributes.Decrypt || key.spec.Type != TypeAES {
					continue
				}
				if !perms.Allows(OpDecrypt, key.spec.ID) {
					continue
				}
				if policy.operationRoleAllowed(key.spec.Attributes, OpDecrypt) != nil {
					continue
				}
				mechs := []Mechanism{artifact.mechanism}
				if policy.AllowMechanismConfusion {
					for _, mech := range []Mechanism{AESKeyWrap, AESCBC, AESGCM} {
						if mech != artifact.mechanism {
							mechs = append(mechs, mech)
						}
					}
				}
				for _, mech := range mechs {
					artifactCopy := artifact
					actions = append(actions, attackAction{
						op: OpDecrypt, keyID: key.spec.ID, mech: mech, artifact: &artifactCopy,
					})
				}
			}
		}
	}

	// Unwrap a target-origin blob. Templates are chosen to exercise source
	// monotonicity, role mutation, trusted-wrapper creation, and permissive
	// output flags.
	if perms.APIs.Has(OpUnwrapKey) {
		for i := range s.artifacts {
			artifact := s.artifacts[i]
			if !s.isTargetOrigin(artifact.sourceOrigin) {
				continue
			}
			for _, key := range keys {
				if !key.spec.Attributes.Unwrap || key.spec.Type != TypeAES {
					continue
				}
				if !perms.Allows(OpUnwrapKey, key.spec.ID) {
					continue
				}
				if policy.operationRoleAllowed(key.spec.Attributes, OpUnwrapKey) != nil {
					continue
				}
				mechs := []Mechanism{artifact.mechanism}
				if policy.AllowMechanismConfusion {
					for _, mech := range []Mechanism{AESKeyWrap, AESCBC, AESGCM} {
						if mech != artifact.mechanism {
							mechs = append(mechs, mech)
						}
					}
				}
				templates := unwrapTemplates(artifact, policy, perms)
				for i := range templates {
					t := templates[i]
					for _, mech := range mechs {
						artifactCopy := artifact
						actions = append(actions, attackAction{
							op: OpUnwrapKey, keyID: key.spec.ID, mech: mech, template: &t,
							artifact: &artifactCopy,
						})
					}
				}
			}
		}
	}

	// Derivation is relevant when it copies target material, or when a
	// permissive HSM lets the caller mint a dual-use helper.
	if perms.APIs.Has(OpDeriveKey) {
		for _, key := range keys {
			if !key.spec.Attributes.Derive {
				continue
			}
			if !perms.Allows(OpDeriveKey, key.spec.ID) {
				continue
			}
			templates := deriveTemplates(key.spec, policy, perms)
			for i := range templates {
				t := templates[i]
				actions = append(actions, attackAction{
					op: OpDeriveKey, keyID: key.spec.ID, mech: HKDFSHA256, template: &t,
				})
			}
		}
	}
	return actions
}

func sortedKeys(h *HSM) []*keyObject {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]ID, 0, len(h.keys))
	for id := range h.keys {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	keys := make([]*keyObject, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, h.keys[id])
	}
	return keys
}

func (s *attackState) isTargetOrigin(origin ID) bool {
	for _, targetOrigin := range s.targets {
		if targetOrigin == origin {
			return true
		}
	}
	return false
}

func (s *attackState) hasArtifact(origin ID, wrappingKey ID, mech Mechanism, attrs Attributes) bool {
	for _, artifact := range s.artifacts {
		if artifact.sourceOrigin == origin &&
			artifact.wrappingKey == wrappingKey &&
			artifact.mechanism == mech &&
			artifact.sourceAttrs == attrs {
			return true
		}
	}
	return false
}

var rolePromotions = []roleAccessor{
	{
		get: func(a Attributes) bool { return a.Wrap },
		set: func(a *Attributes, v bool) { a.Wrap = v },
	},
	{
		get: func(a Attributes) bool { return a.Decrypt },
		set: func(a *Attributes, v bool) { a.Decrypt = v },
	},
	{
		get: func(a Attributes) bool { return a.Unwrap },
		set: func(a *Attributes, v bool) { a.Unwrap = v },
	},
	{
		get: func(a Attributes) bool { return a.Encrypt },
		set: func(a *Attributes, v bool) { a.Encrypt = v },
	},
	{
		get: func(a Attributes) bool { return a.Derive },
		set: func(a *Attributes, v bool) { a.Derive = v },
	},
}

type roleAccessor struct {
	get func(Attributes) bool
	set func(*Attributes, bool)
}

func attributePatchForMaxRoles(old Attributes, policy Policy, perms Permissions) AttributePatch {
	attrs := old
	for _, role := range rolePromotions {
		role.set(&attrs, true)
	}
	if perms.CanSetTrusted && policy.AllowTrustedPromotion {
		attrs.Trusted = true
	}
	return rolePatch(attrs, old)
}

func rolePatch(next, old Attributes) AttributePatch {
	return AttributePatch{
		Encrypt:          boolDiff(old.Encrypt, next.Encrypt),
		Decrypt:          boolDiff(old.Decrypt, next.Decrypt),
		Wrap:             boolDiff(old.Wrap, next.Wrap),
		Unwrap:           boolDiff(old.Unwrap, next.Unwrap),
		Derive:           boolDiff(old.Derive, next.Derive),
		Trusted:          boolDiff(old.Trusted, next.Trusted),
		WrapWithTrusted:  boolDiff(old.WrapWithTrusted, next.WrapWithTrusted),
		Sensitive:        boolDiff(old.Sensitive, next.Sensitive),
		Extractable:      boolDiff(old.Extractable, next.Extractable),
		AlwaysSensitive:  boolDiff(old.AlwaysSensitive, next.AlwaysSensitive),
		NeverExtractable: boolDiff(old.NeverExtractable, next.NeverExtractable),
	}
}

func boolDiff(old, next bool) *bool {
	if old == next {
		return nil
	}
	return BoolPtr(next)
}

func copyTemplates(source KeySpec, policy Policy, perms Permissions) []KeyTemplate {
	if !policy.AllowRoleMutation && !policy.AllowTrustedPromotion {
		return nil
	}
	var out []KeyTemplate
	add := func(attrs Attributes) {
		attrs.AlwaysSensitive = source.Attributes.AlwaysSensitive
		attrs.NeverExtractable = source.Attributes.NeverExtractable
		if attrs == source.Attributes {
			return
		}
		out = append(out, KeyTemplate{
			Label:      source.Label + "-copy",
			Class:      source.Class,
			Type:       source.Type,
			Attributes: attrs,
		})
	}

	if policy.AllowRoleMutation {
		all := source.Attributes
		for _, role := range rolePromotions {
			role.set(&all, true)
		}
		if perms.CanSetTrusted && policy.AllowTrustedPromotion {
			all.Trusted = true
		}
		add(all)

		for _, role := range rolePromotions {
			if role.get(source.Attributes) {
				continue
			}
			attrs := source.Attributes
			role.set(&attrs, true)
			add(attrs)
		}

		// Role splitting: retain exactly one usage role in the copy. This is
		// relevant when a dual-use key exists but operations on it are
		// rejected by a partial policy.
		for i := range rolePromotions {
			attrs := source.Attributes
			for j, role := range rolePromotions {
				role.set(&attrs, i == j)
			}
			add(attrs)
		}
	}
	if !source.Attributes.Trusted && perms.CanSetTrusted && policy.AllowTrustedPromotion {
		attrs := source.Attributes
		attrs.Trusted = true
		add(attrs)
	}
	return out
}

func unwrapTemplates(artifact attackArtifact, policy Policy, perms Permissions) []KeyTemplate {
	base := KeyTemplate{
		Label:      string(artifact.sourceKey) + "-unwrapped",
		Class:      artifact.sourceClass,
		Type:       artifact.sourceType,
		Attributes: artifact.sourceAttrs,
	}
	var out []KeyTemplate
	add := func(attrs Attributes) {
		attrs.AlwaysSensitive = artifact.sourceAttrs.AlwaysSensitive && attrs.Sensitive
		attrs.NeverExtractable = artifact.sourceAttrs.NeverExtractable && !attrs.Extractable
		if attrs == artifact.sourceAttrs {
			return
		}
		t := base
		t.Attributes = attrs
		out = append(out, t)
	}

	attrs := artifact.sourceAttrs
	if policy.AllowUnwrapTemplateDowngrade {
		attrs.Sensitive = false
		attrs.Extractable = true
		attrs.AlwaysSensitive = false
		attrs.NeverExtractable = false
	}
	if policy.AllowRoleMutation {
		for _, role := range rolePromotions {
			role.set(&attrs, true)
		}
	}
	add(attrs)

	if policy.AllowRoleMutation && !policy.AllowUnwrapTemplateDowngrade {
		roles := artifact.sourceAttrs
		for _, role := range rolePromotions {
			role.set(&roles, true)
		}
		add(roles)
	}
	if !artifact.sourceAttrs.Trusted && perms.CanSetTrusted {
		attrs := artifact.sourceAttrs
		attrs.Trusted = true
		if policy.AllowUnwrapTemplateDowngrade {
			attrs.Sensitive = false
			attrs.Extractable = true
		}
		add(attrs)
	}
	return out
}

func deriveTemplates(base KeySpec, policy Policy, perms Permissions) []KeyTemplate {
	attrs := base.Attributes
	if policy.AllowDeriveMaterialCopy {
		attrs.Sensitive = false
		attrs.Extractable = true
		attrs.AlwaysSensitive = false
		attrs.NeverExtractable = false
	} else {
		attrs.Sensitive = true
		attrs.Extractable = false
		attrs.AlwaysSensitive = base.Attributes.AlwaysSensitive
		attrs.NeverExtractable = base.Attributes.NeverExtractable
	}

	var out []KeyTemplate
	add := func(suffix string, candidate Attributes) {
		if !policy.AllowPermissiveDerivation && (!candidate.Sensitive || candidate.Extractable) {
			return
		}
		out = append(out, KeyTemplate{
			Label:          base.Label + "-" + suffix,
			Class:          ClassSecretKey,
			Type:           TypeAES,
			MaterialLength: 32,
			Attributes:     candidate,
		})
	}

	if policy.AllowRoleMutation || policy.AllowRoleOverlap {
		all := attrs
		for _, role := range rolePromotions {
			role.set(&all, true)
		}
		if !all.Trusted && perms.CanSetTrusted {
			all.Trusted = true
		}
		add("all-roles", all)
	}

	for _, role := range []struct {
		name string
		set  func(*Attributes, bool)
	}{
		{"wrap", func(a *Attributes, v bool) { a.Wrap = v }},
		{"decrypt", func(a *Attributes, v bool) { a.Decrypt = v }},
		{"unwrap", func(a *Attributes, v bool) { a.Unwrap = v }},
		{"encrypt", func(a *Attributes, v bool) { a.Encrypt = v }},
	} {
		roleAttrs := attrs
		roleAttrs.Wrap = false
		roleAttrs.Decrypt = false
		roleAttrs.Unwrap = false
		roleAttrs.Encrypt = false
		role.set(&roleAttrs, true)
		add(role.name, roleAttrs)
	}

	if perms.CanSetTrusted {
		trustedWrap := attrs
		trustedWrap.Wrap = true
		trustedWrap.Trusted = true
		add("trusted-wrap", trustedWrap)
	}
	return out
}

func applyAction(current *attackState, action attackAction) (*attackState, AttackStep, error) {
	next := current.clone()
	h := next.hsm
	step := AttackStep{
		Operation: action.op,
		KeyID:     action.keyID,
		TargetID:  action.targetID,
		Mechanism: action.mech,
	}
	switch action.op {
	case OpWrapKey:
		blob, err := h.WrapKey(action.keyID, action.targetID, action.mech)
		if err != nil {
			return nil, step, err
		}
		record, ok := h.InspectBlob(blob)
		if !ok {
			return nil, step, fmt.Errorf("%w: missing wrap provenance", ErrPolicy)
		}
		next.artifacts = append(next.artifacts, attackArtifact{
			blob:         append([]byte(nil), blob...),
			mechanism:    record.Mechanism,
			wrappingKey:  record.WrappingKey,
			sourceKey:    record.SourceKey,
			sourceOrigin: record.SourceOrigin,
			sourceClass:  record.SourceClass,
			sourceType:   record.SourceType,
			sourceAttrs:  record.SourceAttributes,
		})
		step.Blob = blob
	case OpDecrypt:
		if action.artifact == nil {
			return nil, step, fmt.Errorf("internal: decrypt action missing artifact")
		}
		if _, err := h.Decrypt(action.keyID, action.artifact.blob, action.mech); err != nil {
			return nil, step, err
		}
		step.Blob = append([]byte(nil), action.artifact.blob...)
		next.knownOrigins[action.artifact.sourceOrigin] = true
	case OpUnwrapKey:
		if action.artifact == nil || action.template == nil {
			return nil, step, fmt.Errorf("internal: unwrap action missing artifact")
		}
		template := *action.template
		id, err := h.UnwrapKey(action.keyID, action.artifact.blob, template, action.mech)
		if err != nil {
			return nil, step, err
		}
		step.ResultID = id
		step.Blob = append([]byte(nil), action.artifact.blob...)
		template.ID = id
		step.Template = &template
	case OpDeriveKey:
		if action.template == nil {
			return nil, step, fmt.Errorf("internal: derive action missing template")
		}
		baseKnown := next.keyMaterialKnown(action.keyID)
		template := *action.template
		id, err := h.DeriveKey(action.keyID, template, action.mech)
		if err != nil {
			return nil, step, err
		}
		if baseKnown {
			if derived, ok := h.keys[id]; ok {
				next.knownOrigins[derived.origin] = true
			}
		}
		step.ResultID = id
		template.ID = id
		step.Template = &template
	case OpSetAttributeValue:
		if action.patch == nil {
			return nil, step, fmt.Errorf("internal: set-attribute action missing patch")
		}
		if err := h.SetAttributeValue(action.keyID, *action.patch); err != nil {
			return nil, step, err
		}
		step.Patch = action.patch
	case OpCopyObject:
		if action.template == nil {
			return nil, step, fmt.Errorf("internal: copy action missing template")
		}
		template := *action.template
		id, err := h.CopyObject(action.keyID, template)
		if err != nil {
			return nil, step, err
		}
		step.ResultID = id
		template.ID = id
		step.Template = &template
	case OpEncrypt:
		return nil, step, fmt.Errorf("internal: encrypt action is not generated")
	default:
		return nil, step, fmt.Errorf("internal: unknown action %s", action.op)
	}
	next.refreshKnowledge()
	return next, step, nil
}

func (s *attackState) keyMaterialKnown(id ID) bool {
	key, ok := s.hsm.keys[id]
	if !ok {
		return false
	}
	if s.knownOrigins[key.origin] {
		return true
	}
	fp := key.materialFingerprint()
	for _, other := range s.hsm.keys {
		if s.knownOrigins[other.origin] && other.materialFingerprint() == fp {
			return true
		}
	}
	return false
}

func joinIDs(ids []ID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, ",")
}

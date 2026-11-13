package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func validateText(kind, value string, max int) error {
	if value == "" {
		switch kind {
		case "key":
			return ErrEmptyKey
		case "value":
			return ErrEmptyValue
		default:
			return fmt.Errorf("%w: empty %s", ErrInvalidInput, kind)
		}
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s", ErrInvalidUTF8, kind)
	}
	if max > 0 && len(value) > max {
		switch kind {
		case "key":
			return fmt.Errorf("%w: %d > %d", ErrKeyTooLong, len(value), max)
		case "value":
			return fmt.Errorf("%w: %d > %d", ErrValueTooLong, len(value), max)
		default:
			return fmt.Errorf("%w: %s is %d bytes, limit is %d", ErrInvalidInput, kind, len(value), max)
		}
	}
	return nil
}

func validateKey(key string, lim Limits) error {
	return validateText("key", key, lim.MaxKeyBytes)
}

func validateValue(value string, lim Limits) error {
	return validateText("value", value, lim.MaxValueBytes)
}

func validateRoleID(role string, lim Limits) error {
	if role == "" {
		return fmt.Errorf("%w: empty role ID", ErrInvalidInput)
	}
	if !utf8.ValidString(role) {
		return fmt.Errorf("%w: role ID", ErrInvalidUTF8)
	}
	if lim.MaxRoleIDBytes > 0 && len(role) > lim.MaxRoleIDBytes {
		return fmt.Errorf("%w: role ID is %d bytes, limit is %d", ErrRoleIDTooLong, len(role), lim.MaxRoleIDBytes)
	}
	return nil
}

func validateStringMap(kind string, m map[string]string, lim Limits) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := validateKey(k, lim); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		if err := validateValue(m[k], lim); err != nil {
			return fmt.Errorf("%s[%q]: %w", kind, k, err)
		}
	}
	return nil
}

func validatePairs(kind string, pairs []Pair, lim Limits) error {
	seen := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		if seen[p.Key] {
			return fmt.Errorf("%w: %s key %q", ErrDuplicateKey, kind, p.Key)
		}
		seen[p.Key] = true
		if err := validateKey(p.Key, lim); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		if err := validateValue(p.Value, lim); err != nil {
			return fmt.Errorf("%s[%q]: %w", kind, p.Key, err)
		}
	}
	return nil
}

func validateKeyList(kind string, keys []string, lim Limits) error {
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			return fmt.Errorf("%w: %s key %q", ErrDuplicateKey, kind, k)
		}
		seen[k] = true
		if err := validateKey(k, lim); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
	}
	return nil
}

func listFromSet(set map[string]bool, includeFalse bool) []string {
	out := make([]string, 0, len(set))
	for k, v := range set {
		if v || includeFalse {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func pairsToMap(pairs []Pair) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.Key] = p.Value
	}
	return out
}

func chooseStringMap(canonical map[string]string, aliases ...map[string]string) map[string]string {
	if canonical != nil {
		return canonical
	}
	for _, m := range aliases {
		if m != nil {
			return m
		}
	}
	return nil
}

func chooseStringList(canonical []string, aliases ...[]string) []string {
	if canonical != nil {
		return canonical
	}
	for _, v := range aliases {
		if v != nil {
			return v
		}
	}
	return nil
}

func chooseKeySet(slice []string, set map[string]bool, aliases ...[]string) ([]string, bool) {
	if slice != nil {
		return slice, true
	}
	if set != nil {
		return listFromSet(set, false), true
	}
	for _, v := range aliases {
		if v != nil {
			return v, true
		}
	}
	return nil, false
}

type normalizedRequest struct {
	current             map[string]Label
	currentTransitive   map[string]bool
	nonTransitive       map[string]bool
	requested           map[string]string
	requestedTransitive []string
	fixed               map[string]string
	overrides           map[string]bool
	targetTransitive    map[string]bool
	targetTransDefined  bool
	fixedTransitive     map[string]bool
	depth               int
	roleID              string
	roleIDs             []string
	lim                 Limits
}

func normalizeRequest(req HopRequest, lim Limits) (*normalizedRequest, error) {
	lim = lim.normalized()
	n := &normalizedRequest{lim: lim}

	// Current labels.  Rich provenance input wins over simple maps, and a
	// duplicate-aware sequence wins over any map form.
	var currentMap map[string]string
	if req.CurrentPairs != nil {
		if err := validatePairs("current labels", req.CurrentPairs, lim); err != nil {
			return nil, err
		}
		currentMap = pairsToMap(req.CurrentPairs)
		if req.CurrentLabelSet == nil {
			n.current = make(map[string]Label, len(currentMap))
			for k, v := range currentMap {
				n.current[k] = Label{Key: k, Value: v}
			}
		}
	} else if req.CurrentLabelSet == nil {
		currentMap = chooseStringMap(req.CurrentLabels, req.Current, req.Labels)
	}

	if req.CurrentLabelSet != nil {
		n.current = make(map[string]Label, len(req.CurrentLabelSet))
		for k, l := range req.CurrentLabelSet {
			l = l.clone()
			if l.Key == "" {
				l.Key = k
			}
			if l.Key != k {
				return nil, fmt.Errorf("%w: current label key %q has label key %q", ErrInvalidInput, k, l.Key)
			}
			if err := validateKey(k, lim); err != nil {
				return nil, fmt.Errorf("current labels: %w", err)
			}
			if err := validateValue(l.Value, lim); err != nil {
				return nil, fmt.Errorf("current labels[%q]: %w", k, err)
			}
			n.current[k] = l
		}
	}
	if n.current == nil {
		n.current = make(map[string]Label, len(currentMap))
		if err := validateStringMap("current labels", currentMap, lim); err != nil {
			return nil, err
		}
		for k, v := range currentMap {
			n.current[k] = Label{Key: k, Value: v, Origins: []Origin{{Kind: OriginInitial, Value: v}}}
		}
	}

	// Current transitives.
	currentTrans, defined := chooseKeySet(req.CurrentTransitive, req.CurrentTransitiveSet, req.Transitive)
	n.currentTransitive = make(map[string]bool, len(currentTrans))
	if defined {
		if err := validateKeyList("current transitive", currentTrans, lim); err != nil {
			return nil, err
		}
		for _, k := range currentTrans {
			if _, ok := n.current[k]; !ok {
				// The inheritance rule selects current labels whose keys are
				// in the transitive set.  A set entry without a label simply
				// selects nothing.
				continue
			}
			n.currentTransitive[k] = true
		}
	} else {
		// If provenance carries the flag, honor it.
		for k, l := range n.current {
			if l.Transitive {
				n.currentTransitive[k] = true
			}
		}
	}
	for k, l := range n.current {
		l.Transitive = n.currentTransitive[k]
		n.current[k] = l
	}

	// Persistent non-transitivity taint.  Keys that were ever observed as
	// non-transitive cannot be laundered by dropping and re-adding them.
	tainted := chooseTaintedKeys(req)
	n.nonTransitive = make(map[string]bool, len(tainted)+len(n.current))
	if err := validateKeyList("non-transitive", tainted, lim); err != nil {
		return nil, err
	}
	for _, k := range tainted {
		n.nonTransitive[k] = true
	}
	for k := range n.current {
		if !n.currentTransitive[k] {
			n.nonTransitive[k] = true
		}
	}

	// Requested labels.
	if req.RequestedPairs != nil {
		if err := validatePairs("requested labels", req.RequestedPairs, lim); err != nil {
			return nil, err
		}
		n.requested = pairsToMap(req.RequestedPairs)
	} else {
		n.requested = chooseStringMap(req.RequestedLabels, req.NewLabels, req.Requested, req.RequestedLabelMap)
		if err := validateStringMap("requested labels", n.requested, lim); err != nil {
			return nil, err
		}
	}

	// Requested transitive keys.
	reqTrans, defined := chooseKeySet(req.RequestedTransitive, req.RequestedTransitiveSet, req.RequestedTransitiveKeys)
	n.requestedTransitive = append([]string(nil), reqTrans...)
	if defined {
		if err := validateKeyList("requested transitive", n.requestedTransitive, lim); err != nil {
			return nil, err
		}
	}

	// Target fixed labels.
	if req.FixedPairs != nil {
		if err := validatePairs("fixed labels", req.FixedPairs, lim); err != nil {
			return nil, err
		}
		n.fixed = pairsToMap(req.FixedPairs)
	} else {
		n.fixed = chooseStringMap(req.FixedLabels, req.Fixed, req.TargetFixedLabels)
		if err := validateStringMap("fixed labels", n.fixed, lim); err != nil {
			return nil, err
		}
	}

	// Override keys.
	overrideKeys, defined := chooseKeySet(req.OverrideKeys, req.OverridableSet, req.AllowedOverrideKeys, req.TargetOverrideKeys, req.OverridableKeys)
	n.overrides = make(map[string]bool, len(overrideKeys))
	if defined {
		if err := validateKeyList("override keys", overrideKeys, lim); err != nil {
			return nil, err
		}
		for _, k := range overrideKeys {
			n.overrides[k] = true
		}
	}

	// Optional target transitive policy.
	target, targetDefined := chooseKeySet(req.TargetTransitiveKeys, req.TargetTransitiveSet, req.AllowedTransitiveKeys, req.TargetAllowedTransitive)
	n.targetTransDefined = targetDefined
	n.targetTransitive = make(map[string]bool, len(target))
	if targetDefined {
		if err := validateKeyList("target transitive", target, lim); err != nil {
			return nil, err
		}
		for _, k := range target {
			n.targetTransitive[k] = true
		}
	}

	// Optional target-owned transitive fixed labels.
	fixedTrans, _ := chooseKeySet(req.FixedTransitiveKeys, req.FixedTransitiveSet)
	n.fixedTransitive = make(map[string]bool, len(fixedTrans))
	if err := validateKeyList("fixed transitive", fixedTrans, lim); err != nil {
		return nil, err
	}
	for _, k := range fixedTrans {
		n.fixedTransitive[k] = true
	}

	// Role chain and role ID.
	n.roleIDs = append([]string(nil), req.CurrentRoleIDs...)
	if len(n.roleIDs) == 0 {
		n.roleIDs = inferRoleIDs(n.current)
	}
	for _, role := range n.roleIDs {
		if err := validateRoleID(role, lim); err != nil {
			return nil, fmt.Errorf("current role chain: %w", err)
		}
	}
	n.roleID = req.RoleID
	if n.roleID == "" {
		n.roleID = req.Role
	}
	if n.roleID == "" {
		n.roleID = req.RoleId
	}
	if err := validateRoleID(n.roleID, lim); err != nil {
		return nil, err
	}

	n.depth = req.Depth
	if n.depth == 0 {
		n.depth = req.ChainDepth
	}
	if n.depth == 0 {
		n.depth = len(n.roleIDs) + 1
	}
	if n.depth < 0 {
		return nil, fmt.Errorf("%w: negative depth", ErrInvalidInput)
	}

	return n, nil
}

func chooseTaintedKeys(req HopRequest) []string {
	if req.NonTransitiveKeys != nil {
		return req.NonTransitiveKeys
	}
	if req.NonTransitiveSet != nil {
		return listFromSet(req.NonTransitiveSet, false)
	}
	if req.TaintedKeys != nil {
		return req.TaintedKeys
	}
	if req.TaintedSet != nil {
		return listFromSet(req.TaintedSet, false)
	}
	return nil
}

// ValidateLabels checks every label key and value against the default byte
// limits and UTF-8 rules.
func ValidateLabels(labels map[string]string) error {
	return validateStringMap("labels", labels, DefaultLimits)
}

// ValidateKeys checks non-empty valid UTF-8 keys against the default limit.
func ValidateKeys(keys []string) error {
	return validateKeyList("keys", keys, DefaultLimits)
}

// ValidateHop validates a transition request without applying it.
func ValidateHop(req HopRequest) error {
	_, err := normalizeRequest(req, DefaultLimits)
	return err
}

// ValidateSession checks structural consistency of a chain state.
func ValidateSession(s *Session) error {
	if s == nil {
		return fmt.Errorf("%w: nil session", ErrInvalidInput)
	}
	if s.Depth < 0 || s.Depth > MaxDepth {
		return fmt.Errorf("%w: session depth %d", ErrInvalidInput, s.Depth)
	}
	for _, role := range s.RoleIDs {
		if err := validateRoleID(role, DefaultLimits); err != nil {
			return fmt.Errorf("role chain: %w", err)
		}
	}
	for _, key := range sortedLabelKeys(s.Labels) {
		l := s.Labels[key]
		if err := validateKey(key, DefaultLimits); err != nil {
			return fmt.Errorf("labels: %w", err)
		}
		if err := validateValue(l.Value, DefaultLimits); err != nil {
			return fmt.Errorf("labels[%q]: %w", key, err)
		}
		if l.Key != "" && l.Key != key {
			return fmt.Errorf("%w: label map key %q contains label key %q", ErrInvalidInput, key, l.Key)
		}
	}
	for _, key := range s.NonTransitiveKeys() {
		if err := validateKey(key, DefaultLimits); err != nil {
			return fmt.Errorf("non-transitive: %w", err)
		}
	}
	return nil
}

func inferRoleIDs(labels map[string]Label) []string {
	type step struct {
		role string
		hop  int
	}
	var steps []step
	seen := make(map[step]bool)
	for _, l := range labels {
		for _, o := range l.Origins {
			if o.RoleID == "" {
				continue
			}
			s := step{role: o.RoleID, hop: o.Hop}
			if seen[s] {
				continue
			}
			seen[s] = true
			steps = append(steps, s)
		}
	}
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].hop != steps[j].hop {
			return steps[i].hop < steps[j].hop
		}
		return steps[i].role < steps[j].role
	})
	roles := make([]string, 0, len(steps))
	for _, s := range steps {
		roles = append(roles, s.role)
	}
	return roles
}

func sameRoleInChain(role string, roles []string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

func makeProof(l Label) Proof {
	p := Proof{
		Key:        l.Key,
		Value:      l.Value,
		Fixed:      l.Fixed,
		Transitive: l.Transitive,
		Origins:    append([]Origin(nil), l.Origins...),
	}
	var b strings.Builder
	b.WriteString("v1\x00")
	writeProofField(&b, p.Key)
	writeProofField(&b, p.Value)
	b.WriteString(strconv.FormatBool(p.Fixed))
	b.WriteByte(0)
	b.WriteString(strconv.FormatBool(p.Transitive))
	b.WriteByte(0)
	for _, o := range p.Origins {
		writeProofField(&b, string(o.Kind))
		writeProofField(&b, o.RoleID)
		b.WriteString(strconv.Itoa(o.Hop))
		b.WriteByte(0)
		writeProofField(&b, o.Value)
	}
	sum := sha256.Sum256([]byte(b.String()))
	p.Digest = hex.EncodeToString(sum[:])
	return p
}

// VerifyProof recomputes the digest and reports whether the proof has not
// been altered.
func VerifyProof(p Proof) bool {
	want := makeProof(Label{
		Key:        p.Key,
		Value:      p.Value,
		Fixed:      p.Fixed,
		Transitive: p.Transitive,
		Origins:    append([]Origin(nil), p.Origins...),
	})
	return p.Digest != "" && p.Digest == want.Digest
}

// VerifyResultProofs checks every label proof in a hop result.
func VerifyResultProofs(res *HopResult) bool {
	if res == nil {
		return false
	}
	for key, l := range res.Labels {
		p, ok := res.Proofs[key]
		if !ok || !VerifyProof(p) {
			return false
		}
		if p.Key != key || p.Value != l.Value || p.Fixed != l.Fixed || p.Transitive != l.Transitive {
			return false
		}
	}
	return true
}

// VerifySessionProofs checks every proof retained by a session.
func VerifySessionProofs(s *Session) bool {
	if s == nil {
		return false
	}
	for key, l := range s.Labels {
		p, ok := s.Proofs[key]
		if !ok || !VerifyProof(p) {
			return false
		}
		if p.Value != l.Value || p.Fixed != l.Fixed || p.Transitive != l.Transitive {
			return false
		}
	}
	return true
}

func writeProofField(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
	b.WriteByte(0)
}

func copyOrigins(in []Origin) []Origin { return append([]Origin(nil), in...) }

func appendOrigin(in []Origin, o Origin) []Origin {
	out := make([]Origin, len(in)+1)
	copy(out, in)
	out[len(in)] = o
	return out
}

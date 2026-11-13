package netsec

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

// Field identifies one action input field for collision auditing.
type Field string

const (
	FieldSourceRoot        Field = "source_root"
	FieldCommandArgs       Field = "command_args"
	FieldWorkDir           Field = "work_dir"
	FieldEnvironment       Field = "environment"
	FieldSecretEnvironment Field = "secret_environment"
	FieldToolchain         Field = "toolchain_digest"
	FieldDependencies      Field = "dependency_outputs"
	FieldPlatform          Field = "platform_attributes"
	FieldSecurityPolicy    Field = "security_policy_digest"
)

// AuditedFields is the stable order used when reporting missing fields.
var AuditedFields = []Field{
	FieldSourceRoot,
	FieldCommandArgs,
	FieldWorkDir,
	FieldEnvironment,
	FieldSecretEnvironment,
	FieldToolchain,
	FieldDependencies,
	FieldPlatform,
	FieldSecurityPolicy,
}

// KeyCallback models a proposed cache-key function awaiting review.
type KeyCallback func(ActionInputs) (string, error)

// PendingKeyCallback is an alias for KeyCallback.
type PendingKeyCallback = KeyCallback

// FieldCandidate supplies two finite candidate values for one input field.
// The expected dynamic type of ValueA and ValueB depends on Field:
//
//   - FieldSourceRoot, FieldWorkDir, FieldToolchain,
//     FieldSecurityPolicy: string
//   - FieldCommandArgs: []string
//   - FieldEnvironment, FieldSecretEnvironment: EnvVar
//   - FieldDependencies: DependencyOutput
//   - FieldPlatform: Attribute
//
// SemanticsA and SemanticsB must describe distinct output security meanings.
// The audit never treats two candidate values with the same security meaning
// as a reportable collision.
type FieldCandidate struct {
	Field      Field
	ValueA     any
	ValueB     any
	SemanticsA string
	SemanticsB string
}

// Collision describes two candidate input combinations that a callback maps
// to the same key even though their canonical action keys and output security
// meanings differ.
type Collision struct {
	Fields              []Field
	CallbackKey         string
	LeftKey             ActionKey
	RightKey            ActionKey
	LeftSemantics       string
	RightSemantics      string
	LeftVariant         uint64
	RightVariant        uint64
	DifferingFieldCount int
}

// AuditReport is the result of searching a pending key callback for the
// shortest collision. MissingFields is the union of all fields implicated in
// collisions at the minimum distance. A clean report means the callback kept
// every tested field distinction.
type AuditReport struct {
	Collisions        []Collision
	MissingFields     []Field
	MinDifferingCount int
	EvaluatedStates   int
	InvalidStates     int
	CallbackErrors    int
}

// Clean reports whether no collision was found.
func (r AuditReport) Clean() bool {
	return len(r.Collisions) == 0
}

// MissingField returns the first missing field in stable field order.
func (r AuditReport) MissingField() (Field, bool) {
	if len(r.MissingFields) == 0 {
		return "", false
	}
	return r.MissingFields[0], true
}

// DefaultCandidates returns two security-distinct finite values for every
// input field. Secret and public environment entries use names not already
// present in baseline.
func DefaultCandidates(baseline ActionInputs) []FieldCandidate {
	publicName := uniqueEnvName(baseline.Env, "NETSEC_AUDIT_PUBLIC")
	secretName := uniqueEnvName(baseline.Env, "NETSEC_AUDIT_SECRET")
	depName := uniqueDependencyName(baseline.DependencyOutputs, "netsec.audit.dependency")
	attrName := uniqueAttributeName(baseline.Platform, "netsec.audit.platform")

	return []FieldCandidate{
		{
			Field:      FieldSourceRoot,
			ValueA:     "/netsec/audit/source-a",
			ValueB:     "/netsec/audit/source-b",
			SemanticsA: "source-tree-A",
			SemanticsB: "source-tree-B",
		},
		{
			Field:      FieldCommandArgs,
			ValueA:     []string{"build", "-o", "out-a"},
			ValueB:     []string{"build", "-o", "out-b"},
			SemanticsA: "command-A",
			SemanticsB: "command-B",
		},
		{
			Field:      FieldWorkDir,
			ValueA:     "/netsec/audit/work-a",
			ValueB:     "/netsec/audit/work-b",
			SemanticsA: "working-directory-A",
			SemanticsB: "working-directory-B",
		},
		{
			Field:      FieldEnvironment,
			ValueA:     EnvVar{Name: publicName, Value: "public-a"},
			ValueB:     EnvVar{Name: publicName, Value: "public-b"},
			SemanticsA: "public-environment-A",
			SemanticsB: "public-environment-B",
		},
		{
			Field:      FieldSecretEnvironment,
			ValueA:     EnvVar{Name: secretName, Value: "secret-round-a", Secret: true},
			ValueB:     EnvVar{Name: secretName, Value: "secret-round-b", Secret: true},
			SemanticsA: "secret-version-A",
			SemanticsB: "secret-version-B",
		},
		{
			Field:      FieldToolchain,
			ValueA:     "sha256:toolchain-a",
			ValueB:     "sha256:toolchain-b",
			SemanticsA: "toolchain-A",
			SemanticsB: "toolchain-B",
		},
		{
			Field:      FieldDependencies,
			ValueA:     DependencyOutput{Name: depName, Digest: "sha256:dependency-output-a"},
			ValueB:     DependencyOutput{Name: depName, Digest: "sha256:dependency-output-b"},
			SemanticsA: "dependency-output-A",
			SemanticsB: "dependency-output-B",
		},
		{
			Field:      FieldPlatform,
			ValueA:     Attribute{Name: attrName, Value: "platform-a"},
			ValueB:     Attribute{Name: attrName, Value: "platform-b"},
			SemanticsA: "platform-A",
			SemanticsB: "platform-B",
		},
		{
			Field:      FieldSecurityPolicy,
			ValueA:     "sha256:policy-a",
			ValueB:     "sha256:policy-b",
			SemanticsA: "security-policy-A",
			SemanticsB: "security-policy-B",
		},
	}
}

// AuditKeyCallback searches all 2^n combinations of the candidate values for
// the shortest collision: two combinations whose callback keys match while
// their canonical action keys and output security meanings differ. It returns
// the union of fields implicated in the shortest collisions.
//
// If candidates is nil, DefaultCandidates is used. Secret environment auditing
// requires a non-empty host HMAC key because canonical action keys must use the
// same host-HMAC secret versioning as a real build.
func AuditKeyCallback(
	callback KeyCallback,
	baseline ActionInputs,
	secretHMACKey []byte,
	candidates []FieldCandidate,
) (AuditReport, error) {
	if callback == nil {
		return AuditReport{}, errors.New("netsec: nil key callback")
	}
	if candidates == nil {
		candidates = DefaultCandidates(baseline)
	}
	if len(candidates) == 0 {
		return AuditReport{}, errors.New("netsec: no audit candidates")
	}
	if len(candidates) > 12 {
		return AuditReport{}, fmt.Errorf("netsec: too many audit fields: %d", len(candidates))
	}
	if len(secretHMACKey) == 0 {
		for _, ev := range baseline.Env {
			if ev.Secret {
				return AuditReport{}, fmt.Errorf("%w: environment variable %q", ErrSecretKeyRequired, ev.Name)
			}
		}
		filtered := make([]FieldCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.Field != FieldSecretEnvironment {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	if len(candidates) == 0 {
		return AuditReport{}, errors.New("netsec: cannot audit candidates without a host HMAC key")
	}
	if err := validateCandidates(candidates); err != nil {
		return AuditReport{}, err
	}

	stateCount := 1 << uint(len(candidates))
	type auditedState struct {
		mask      uint64
		key       ActionKey
		callback  string
		semantics string
	}

	states := make([]auditedState, 0, stateCount)
	report := AuditReport{}
	for mask := 0; mask < stateCount; mask++ {
		inputs := cloneInputs(baseline)
		semantics := make([]string, len(candidates))

		for i, candidate := range candidates {
			useB := mask&(1<<uint(i)) != 0
			value := candidate.ValueA
			label := candidate.SemanticsA
			if useB {
				value = candidate.ValueB
				label = candidate.SemanticsB
			}
			if err := applyCandidate(&inputs, candidate.Field, value); err != nil {
				return report, fmt.Errorf("netsec: audit candidate %q: %w", candidate.Field, err)
			}
			semantics[i] = string(candidate.Field) + "=" + label
		}

		canonical, err := inputs.Key(secretHMACKey)
		if err != nil {
			report.InvalidStates++
			continue
		}
		callbackKey, err := callback(inputs)
		if err != nil {
			report.CallbackErrors++
			continue
		}
		report.EvaluatedStates++
		states = append(states, auditedState{
			mask:      uint64(mask),
			key:       canonical,
			callback:  callbackKey,
			semantics: strings.Join(semantics, "\x00"),
		})
	}

	if report.EvaluatedStates == 0 {
		return report, errors.New("netsec: no candidate state produced both a canonical and callback key")
	}

	minDistance := len(candidates) + 1
	seenFieldSets := make(map[string]struct{})
	var collisions []Collision

	for i := 0; i < len(states); i++ {
		for j := i + 1; j < len(states); j++ {
			left, right := states[i], states[j]
			if left.callback != right.callback {
				continue
			}
			if left.key == right.key {
				continue
			}
			if left.semantics == right.semantics {
				continue
			}

			diffMask := left.mask ^ right.mask
			distance := bits.OnesCount64(diffMask)
			if distance == 0 {
				continue
			}
			if distance > minDistance {
				continue
			}
			fields := fieldsInMask(diffMask, candidates)
			if len(fields) == 0 {
				continue
			}

			if distance < minDistance {
				minDistance = distance
				collisions = collisions[:0]
				seenFieldSets = make(map[string]struct{})
			}
			setKey := fieldsKey(fields)
			if _, exists := seenFieldSets[setKey]; exists {
				continue
			}
			seenFieldSets[setKey] = struct{}{}
			collisions = append(collisions, Collision{
				Fields:              fields,
				CallbackKey:         left.callback,
				LeftKey:             left.key,
				RightKey:            right.key,
				LeftSemantics:       left.semantics,
				RightSemantics:      right.semantics,
				LeftVariant:         left.mask,
				RightVariant:        right.mask,
				DifferingFieldCount: distance,
			})
		}
	}

	report.Collisions = collisions
	if len(collisions) == 0 {
		report.MinDifferingCount = 0
	} else {
		report.MinDifferingCount = minDistance
	}
	report.MissingFields = unionFields(collisions)
	return report, nil
}

// AuditPendingKeyCallback is an alias for AuditKeyCallback.
func AuditPendingKeyCallback(
	callback KeyCallback,
	baseline ActionInputs,
	secretHMACKey []byte,
	candidates []FieldCandidate,
) (AuditReport, error) {
	return AuditKeyCallback(callback, baseline, secretHMACKey, candidates)
}

// FindShortestCollision is an alias for AuditKeyCallback.
func FindShortestCollision(
	callback KeyCallback,
	baseline ActionInputs,
	secretHMACKey []byte,
	candidates []FieldCandidate,
) (AuditReport, error) {
	return AuditKeyCallback(callback, baseline, secretHMACKey, candidates)
}

func validateCandidates(candidates []FieldCandidate) error {
	seen := make(map[Field]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.Field == "" {
			return errors.New("netsec: audit candidate has empty field")
		}
		if _, exists := seen[candidate.Field]; exists {
			return fmt.Errorf("%w: audit field %q", ErrDuplicateKey, candidate.Field)
		}
		seen[candidate.Field] = struct{}{}
		if candidate.SemanticsA == "" || candidate.SemanticsB == "" {
			return fmt.Errorf("netsec: audit candidate %q has empty output semantics", candidate.Field)
		}
		if candidate.SemanticsA == candidate.SemanticsB {
			return fmt.Errorf("netsec: audit candidate %q has identical output semantics", candidate.Field)
		}
	}
	return nil
}

func cloneInputs(in ActionInputs) ActionInputs {
	out := in
	out.Args = append([]string(nil), in.Args...)
	out.Env = append([]EnvVar(nil), in.Env...)
	out.DependencyOutputs = append([]DependencyOutput(nil), in.DependencyOutputs...)
	out.Platform = append([]Attribute(nil), in.Platform...)
	return out
}

func applyCandidate(in *ActionInputs, field Field, value any) error {
	switch field {
	case FieldSourceRoot:
		s, ok := value.(string)
		if !ok {
			return typeError(field, value, "string")
		}
		in.SourceRoot = s
	case FieldCommandArgs:
		args, ok := value.([]string)
		if !ok {
			return typeError(field, value, "[]string")
		}
		in.Args = append([]string(nil), args...)
	case FieldWorkDir:
		s, ok := value.(string)
		if !ok {
			return typeError(field, value, "string")
		}
		in.WorkDir = s
	case FieldEnvironment:
		ev, ok := value.(EnvVar)
		if !ok {
			return typeError(field, value, "EnvVar")
		}
		if ev.Secret {
			return fmt.Errorf("%w: %s requires a public EnvVar", ErrInvalidCacheEntry, field)
		}
		in.Env = upsertEnv(in.Env, ev)
	case FieldSecretEnvironment:
		ev, ok := value.(EnvVar)
		if !ok {
			return typeError(field, value, "EnvVar")
		}
		if !ev.Secret {
			return fmt.Errorf("%w: %s requires a secret EnvVar", ErrInvalidCacheEntry, field)
		}
		in.Env = upsertEnv(in.Env, ev)
	case FieldToolchain:
		s, ok := value.(string)
		if !ok {
			return typeError(field, value, "string")
		}
		in.ToolchainDigest = s
	case FieldDependencies:
		dep, ok := value.(DependencyOutput)
		if !ok {
			return typeError(field, value, "DependencyOutput")
		}
		in.DependencyOutputs = upsertDependency(in.DependencyOutputs, dep)
	case FieldPlatform:
		attr, ok := value.(Attribute)
		if !ok {
			return typeError(field, value, "Attribute")
		}
		in.Platform = upsertAttribute(in.Platform, attr)
	case FieldSecurityPolicy:
		s, ok := value.(string)
		if !ok {
			return typeError(field, value, "string")
		}
		in.SecurityPolicyDigest = s
	default:
		return fmt.Errorf("netsec: unknown audit field %q", field)
	}
	return nil
}

func typeError(field Field, value any, want string) error {
	return fmt.Errorf("netsec: audit field %q has type %T, want %s", field, value, want)
}

func upsertEnv(items []EnvVar, value EnvVar) []EnvVar {
	out := append([]EnvVar(nil), items...)
	for i := range out {
		if out[i].Name == value.Name {
			out[i] = value
			return out
		}
	}
	return append(out, value)
}

func upsertDependency(items []DependencyOutput, value DependencyOutput) []DependencyOutput {
	out := append([]DependencyOutput(nil), items...)
	for i := range out {
		if out[i].Name == value.Name {
			out[i] = value
			return out
		}
	}
	return append(out, value)
}

func upsertAttribute(items []Attribute, value Attribute) []Attribute {
	out := append([]Attribute(nil), items...)
	for i := range out {
		if out[i].Name == value.Name {
			out[i] = value
			return out
		}
	}
	return append(out, value)
}

func fieldsInMask(mask uint64, candidates []FieldCandidate) []Field {
	var fields []Field
	for i, candidate := range candidates {
		if mask&(1<<uint(i)) != 0 {
			fields = append(fields, candidate.Field)
		}
	}
	return fields
}

func fieldsKey(fields []Field) string {
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = string(field)
	}
	return strings.Join(parts, "\x00")
}

func unionFields(collisions []Collision) []Field {
	present := make(map[Field]struct{})
	for _, collision := range collisions {
		for _, field := range collision.Fields {
			present[field] = struct{}{}
		}
	}
	var fields []Field
	for _, field := range AuditedFields {
		if _, ok := present[field]; ok {
			fields = append(fields, field)
		}
	}
	for field := range present {
		found := false
		for _, ordered := range AuditedFields {
			if field == ordered {
				found = true
				break
			}
		}
		if !found {
			fields = append(fields, field)
		}
	}
	return fields
}

func uniqueEnvName(env []EnvVar, base string) string {
	existing := make(map[string]struct{}, len(env))
	for _, ev := range env {
		existing[ev.Name] = struct{}{}
	}
	name := base
	for i := 0; ; i++ {
		if _, exists := existing[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, i)
	}
}

func uniqueDependencyName(deps []DependencyOutput, base string) string {
	existing := make(map[string]struct{}, len(deps))
	for _, dep := range deps {
		existing[dep.Name] = struct{}{}
	}
	name := base
	for i := 0; ; i++ {
		if _, exists := existing[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, i)
	}
}

func uniqueAttributeName(attrs []Attribute, base string) string {
	existing := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		existing[attr.Name] = struct{}{}
	}
	name := base
	for i := 0; ; i++ {
		if _, exists := existing[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, i)
	}
}

package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const planKeyVersion = "netsec-plan-key-v1"

// DigestParamTypes returns the digest of an ordered parameter type list.
func DigestParamTypes(types []ParamType) string {
	var b strings.Builder
	writeString(&b, "param-types-v1")
	writeUint64(&b, uint64(len(types)))
	for _, typ := range types {
		writeString(&b, string(typ))
	}
	return digestString(b.String())
}

// DigestRoleClosure returns the digest of a role closure. Order is irrelevant.
func DigestRoleClosure(roles []string) string {
	roles = sortedStrings(roles)
	var b strings.Builder
	writeString(&b, "role-closure-v1")
	writeUint64(&b, uint64(len(roles)))
	for _, role := range roles {
		writeString(&b, role)
	}
	return digestString(b.String())
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeString(b *strings.Builder, value string) {
	writeUint64(b, uint64(len(value)))
	b.WriteString(value)
}

func writeUint64(b *strings.Builder, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	b.Write(buf[:])
}

// Validate checks alias consistency and required identities. It does not
// consult a catalog; snapshot-aware validation lives on SecuritySnapshot.
func (in CompileInput) Validate() error {
	_, err := in.normalize()
	return err
}

// CacheKey returns the complete plan key. Callers that need alias conflict
// diagnostics should use Key.
func (in CompileInput) CacheKey() PlanKey {
	key, err := in.Key()
	if err != nil {
		// The malformed input is still encoded deterministically so that a
		// caller cannot accidentally share a key with a valid plan.
		key = PlanKey("invalid:" + digestString(fmt.Sprintf("%#v", in)))
	}
	return key
}

// PlanKey is an alias for CacheKey.
func (in CompileInput) PlanKey() PlanKey { return in.CacheKey() }

// Key computes a key over every authorization-relevant compile input.
func (in CompileInput) Key() (PlanKey, error) {
	norm, err := in.normalize()
	if err != nil {
		return "", err
	}
	return PlanKey(planKeyDigest(norm)), nil
}

// Canonical returns the unambiguous byte-level representation whose digest is
// the plan key. It is exported to make key-coverage audits straightforward.
func (in CompileInput) Canonical() (string, error) {
	norm, err := in.normalize()
	if err != nil {
		return "", err
	}
	return canonicalInput(norm), nil
}

func planKeyDigest(in CompileInput) string {
	return "plan:" + digestString(canonicalInput(in))
}

func canonicalInput(in CompileInput) string {
	var b strings.Builder
	writeString(&b, planKeyVersion)
	writeString(&b, in.QueryASTDigest)

	writeUint64(&b, uint64(len(in.ParamTypes)))
	for _, typ := range in.ParamTypes {
		writeString(&b, string(typ))
	}
	writeString(&b, in.ParamTypeDigest)

	writeString(&b, string(in.PrincipalID))
	writeUint64(&b, uint64(len(in.Roles)))
	for _, role := range in.Roles {
		writeString(&b, role)
	}
	writeString(&b, in.RoleClosureDigest)

	writeUint64(&b, in.PolicyEpoch)
	writeUint64(&b, uint64(len(in.Objects)))
	for _, dep := range in.Objects {
		writeString(&b, string(dep.ObjectID))
		writeUint64(&b, dep.SchemaVersion)
		writeString(&b, dep.Operations.String())
		writeString(&b, dep.RLSDigest)
	}
	return b.String()
}

func (in CompileInput) normalize() (CompileInput, error) {
	out := cloneCompileInput(in)

	ast, err := chooseAlias("canonical query AST digest",
		in.QueryASTDigest, in.ASTDigest, in.CanonicalASTDigest, in.CanonicalQueryASTDigest)
	if err != nil {
		return CompileInput{}, err
	}
	if ast == "" {
		return CompileInput{}, fmt.Errorf("%w: canonical query AST digest is empty", ErrInvalidCompileInput)
	}
	out.QueryASTDigest, out.ASTDigest, out.CanonicalASTDigest, out.CanonicalQueryASTDigest = ast, "", "", ""

	paramDigest, err := chooseAlias("parameter type list digest", in.ParamTypeDigest, in.ParamTypeListDigest)
	if err != nil {
		return CompileInput{}, err
	}
	if paramDigest == "" {
		paramDigest = DigestParamTypes(in.ParamTypes)
	}
	if paramDigest == "" {
		return CompileInput{}, fmt.Errorf("%w: parameter type digest is empty", ErrInvalidCompileInput)
	}
	out.ParamTypeDigest, out.ParamTypeListDigest = paramDigest, ""

	if out.PrincipalID == "" {
		return CompileInput{}, fmt.Errorf("%w: principal is empty", ErrInvalidCompileInput)
	}
	if out.RoleClosureDigest == "" {
		out.RoleClosureDigest = DigestRoleClosure(out.Roles)
	}
	out.Roles = sortedStrings(out.Roles)

	if len(in.Objects) > 0 && len(in.ObjectDependencies) > 0 && !equalDeps(in.Objects, in.ObjectDependencies) {
		return CompileInput{}, fmt.Errorf("%w: Objects and ObjectDependencies disagree", ErrInvalidCompileInput)
	}
	if len(out.Objects) == 0 {
		out.Objects = cloneDependencies(in.ObjectDependencies)
	}
	out.ObjectDependencies = nil

	seen := make(map[string]struct{}, len(out.Objects))
	for i, dep := range out.Objects {
		if dep.Operations != 0 && dep.RequiredOps != 0 && dep.Operations != dep.RequiredOps {
			return CompileInput{}, fmt.Errorf("%w: object %s has conflicting operation aliases", ErrInvalidCompileInput, dep.ObjectID)
		}
		if dep.Operations == 0 {
			dep.Operations = dep.RequiredOps
		}
		dep.RequiredOps = 0
		out.Objects[i] = dep
		if dep.ObjectID == "" {
			return CompileInput{}, fmt.Errorf("%w: object %d has an empty ID", ErrInvalidCompileInput, i)
		}
		identity := string(dep.ObjectID) + "\x00" + strconv.FormatUint(dep.SchemaVersion, 10) + "\x00" +
			dep.Operations.String() + "\x00" + dep.RLSDigest
		if _, ok := seen[identity]; ok {
			return CompileInput{}, fmt.Errorf("%w: duplicate dependency for object %s", ErrInvalidCompileInput, dep.ObjectID)
		}
		seen[identity] = struct{}{}
	}
	return out, nil
}

func chooseAlias(name string, values ...string) (string, error) {
	var chosen string
	for _, value := range values {
		if value == "" {
			continue
		}
		if chosen == "" {
			chosen = value
			continue
		}
		if chosen != value {
			return "", fmt.Errorf("%w: %s aliases disagree", ErrInvalidCompileInput, name)
		}
	}
	return chosen, nil
}

func equalDeps(a, b []ObjectDependency) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ObjectID != b[i].ObjectID ||
			a[i].SchemaVersion != b[i].SchemaVersion ||
			dependencyOps(a[i]) != dependencyOps(b[i]) ||
			a[i].RLSDigest != b[i].RLSDigest {
			return false
		}
	}
	return true
}

func dependencyOps(dep ObjectDependency) OperationSet {
	if dep.Operations != 0 {
		return dep.Operations
	}
	return dep.RequiredOps
}

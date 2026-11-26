package plancache

import (
	"crypto/rand"
	"encoding/hex"
)

// OperatorFactory builds the physical operators for an authorized compile
// input. It is invoked only after catalog authorization succeeds, so a
// rejected input never materializes operators.
type OperatorFactory func(in CompileInput) ([]Operator, error)

// ManifestOf extracts the immutable dependency manifest from a compile
// input. The manifest is the complete revalidation checklist.
func ManifestOf(in CompileInput) Manifest {
	m := Manifest{
		Key:         KeyOf(in),
		RequiredOps: append([]Operation(nil), in.RequiredOps...),
		PolicyEpoch: in.PolicyEpoch,
		Caller:      in.Caller,
		RoleClosure: in.RoleClosure,
		Tables:      []TableRef{},
		Views:       []ViewRef{},
	}
	for _, node := range in.DepGraph {
		switch node.RefType {
		case "view":
			m.Views = append(m.Views, node.View)
		case "table":
			m.Tables = append(m.Tables, node.Table)
		}
	}
	return m
}

func newPlanID() string {
	var buf [12]byte
	_, _ = rand.Read(buf[:])
	return "plan-" + hex.EncodeToString(buf[:])
}

// Compile authorizes a compile input against the catalog and, on success,
// materializes an immutable Plan via the factory. Authorization failure
// returns before any operator is built.
func Compile(cat Catalog, in CompileInput, factory OperatorFactory) (*Plan, error) {
	if err := cat.AuthorizeCompile(in); err != nil {
		return nil, err
	}
	ops, err := factory(in)
	if err != nil {
		return nil, err
	}
	key := KeyOf(in)
	return &Plan{
		ID:        newPlanID(),
		Key:       key,
		Manifest:  ManifestOf(in),
		Operators: ops,
	}, nil
}

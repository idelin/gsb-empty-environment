package scim_test

import (
	"errors"
	"testing"

	scim "formal.local/w27-4e139ba19e/scim"
)

func TestW27ShimReexportSmoke(t *testing.T) {
	expr, err := scim.ParseFilter(`userName eq "alice" and roles pr`, scim.WithMaxASTDepth(8))
	if err != nil {
		t.Fatal(err)
	}
	if expr.Depth() == 0 {
		t.Fatal("depth should be positive")
	}

	engine := scim.NewEngine()
	engine.Store.Put("User", scim.Resource{"id": "u1", "tenantId": "t1", "userName": "alice"})
	_, err = engine.SecureFilter(scim.Caller{TenantID: "t1"}, `tenantId eq "t2"`)
	if !errors.Is(err, scim.ErrProtectedAttribute) {
		t.Fatalf("shim sentinel identity mismatch: %v", err)
	}
}

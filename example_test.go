package scim_test

import (
	"context"
	"errors"
	"fmt"

	scim "formal.local/w27-4e139ba19e"
)

func ExampleParseFilter() {
	expr, err := scim.ParseFilter(`emails[type eq "work" and value co "@example.com"]`)
	if err != nil {
		panic(err)
	}
	fmt.Println(expr.String())
	// Output: emails[(type eq "work" and value co "@example.com")]
}

func ExampleEngine_ApplyPatch_crossTenantMember() {
	engine := scim.NewEngine()
	engine.Store.Put("User", scim.Resource{"id": "u-bob", "tenantId": "tenant-b", "userName": "bob"})
	engine.Store.Put("Group", scim.Resource{
		"id":       "g-team",
		"tenantId": "tenant-a",
		"members":  []any{scim.Resource{"value": "u-carol"}},
	})

	caller := scim.Caller{TenantID: "tenant-a", SubjectID: "u-alice", TenantAdmin: true}
	_, err := engine.Patch(context.Background(), caller, "Group", "g-team",
		scim.PatchOperation{
			Op:    "add",
			Path:  "members",
			Value: []any{scim.Resource{"value": "u-bob"}},
		})

	fmt.Println(errors.Is(err, scim.ErrCrossTenantMember))
	// Output: true
}

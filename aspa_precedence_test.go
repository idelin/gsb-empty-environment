package pathsec

import "testing"

func TestCustomerConeInvalidDominatesEarlierUnknown(t *testing.T) {
	table := NewASPATable()
	// AS3 explicitly publishes an empty provider set; AS2 is unpublished.
	_ = table.PublishEmpty(3)
	got := VerifyASPA(table, ASPARequest{
		ASPath: []uint32{1, 2, 3},
		Role:   RoleCustomer,
	})
	if got.Status != StatusInvalid {
		t.Fatalf("explicit unauthorized later hop must dominate earlier Unknown, got %s", got.Status)
	}
}

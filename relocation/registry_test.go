package relocation

import "testing"

func newTestRegistry() *Registry {
	registry := NewRegistry()
	_ = registry.RegisterOwner("acme", "com.acme", "org.acme")
	_ = registry.RegisterOwner("evil", "net.evil")
	return registry
}

func TestRegisterOwnerRejectsBadInput(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterOwner("", "com.example"); err == nil {
		t.Error("expected error for empty owner")
	}
	if err := registry.RegisterOwner("acme"); err == nil {
		t.Error("expected error when no namespaces are registered")
	}
	for _, namespace := range []string{"", "com..acme", "com.evil..", "back\\slash"} {
		if err := registry.RegisterOwner("acme", namespace); err == nil {
			t.Errorf("expected error for invalid namespace %q", namespace)
		}
	}
}

func TestOwnerOfLongestPrefix(t *testing.T) {
	registry := newTestRegistry()
	cases := map[string]string{
		"com.acme":           "acme",
		"com.acme.libs.util": "acme",
		"org.acme.plugins":   "acme",
		"net.evil.payload":   "evil",
		"com.acmeevil":       "",
		"com.unknown":        "",
	}
	for groupID, wantOwner := range cases {
		if got := registry.OwnerOf(groupID); got != wantOwner {
			t.Errorf("OwnerOf(%q) = %q, want %q", groupID, got, wantOwner)
		}
	}
}

func TestAllowedSameOwner(t *testing.T) {
	registry := newTestRegistry()
	if !registry.Allowed("com.acme.core", "com.acme.moved") {
		t.Error("relocation between same-owner namespaces should be allowed")
	}
	if !registry.Allowed("com.acme.core", "org.acme.moved") {
		t.Error("relocation across namespaces of the same owner should be allowed")
	}
}

func TestAllowedRejectsCrossOwner(t *testing.T) {
	registry := newTestRegistry()
	if registry.Allowed("com.acme.core", "net.evil.payload") {
		t.Error("cross-owner relocation must be rejected")
	}
	if registry.Allowed("com.unknown.lib", "net.evil.payload") {
		t.Error("unregistered original groupId must fail closed")
	}
	if registry.Allowed("com.acme.core", "com.unknown.lib") {
		t.Error("target without an owner must be rejected")
	}
}

func TestAllowedExplicitApproval(t *testing.T) {
	registry := newTestRegistry()
	target := "net.evil.payload"
	if err := registry.Approve(target); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !registry.IsApproved(target) {
		t.Error("IsApproved should report the approved groupId")
	}
	if !registry.Allowed("com.acme.core", target) {
		t.Error("explicitly approved cross-owner target should be allowed")
	}
	if err := registry.Approve("bad\\..id"); err == nil {
		t.Error("approving an invalid groupId must fail")
	}
}

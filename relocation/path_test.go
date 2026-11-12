package relocation

import "testing"

func TestCheckGroupID(t *testing.T) {
	valid := []string{
		"com.example",
		"org.apache.maven.plugins",
		"a",
		"com.example.v2",
	}
	for _, groupID := range valid {
		if err := CheckGroupID(groupID); err != nil {
			t.Errorf("CheckGroupID(%q) = %v, want nil", groupID, err)
		}
	}

	invalid := []string{
		"",
		".com.example",
		"com.example.",
		"com..example",
		"com...example",
		"com.evil..",
		"com.back\\slash.example",
		"back\\slash",
	}
	for _, groupID := range invalid {
		if err := CheckGroupID(groupID); err == nil {
			t.Errorf("CheckGroupID(%q) = nil, want error", groupID)
		}
	}
}

func TestGroupPathTraversalRejected(t *testing.T) {
	cases := map[string]string{
		"":                     "empty",
		"..":                   "parent segment",
		"com..":                "empty trailing segment",
		"com..evil":            "empty inner segment",
		"com.evil.\\..":        "backslash before parent segment",
		"com.attacker\\..\\..": "backslash smuggled traversal",
		"evil\\..\\..\\etc":    "windows-style traversal",
		"..\\..\\etc":          "leading windows-style traversal",
	}
	for groupID, label := range cases {
		path, err := GroupPath(groupID)
		if err == nil {
			t.Errorf("%s: GroupPath(%q) = %q, want error", label, groupID, path)
		}
		if path != "" {
			t.Errorf("%s: rejected groupId %q produced path %q", label, groupID, path)
		}
	}
}

func TestGroupPathValid(t *testing.T) {
	path, err := GroupPath("com.example.libs")
	if err != nil {
		t.Fatalf("GroupPath returned error: %v", err)
	}
	if path != "com/example/libs" {
		t.Errorf("GroupPath = %q, want %q", path, "com/example/libs")
	}
}

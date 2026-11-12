package universallink

import "testing"

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"/share/*", "/share/abc", true},
		{"/share/*", "/share/", true},
		{"/share/*", "/share", false},
		{"/share/*", "/share/a/b", true},
		{"*", "/anything/at/all", true},
		{"*", "", true},
		{"/home?", "/home1", true},
		{"/home?", "/home", false},
		{"/home?", "/home12", false},
		{"*.pdf", "/docs/report.pdf", true},
		{"*.pdf", "/docs/report.PDF", false},
		{"a*b*c", "abc", true},
		{"a*b*c", "axxbxxc", true},
		{"a*b*c", "axbxcxd", false},
		{"/exact", "/exact", true},
		{"/exact", "/exact/", false},
		{"café?", "café1", true},
		{"café?", "caf", false},
	}

	for _, tc := range tests {
		t.Run(tc.pattern+"|"+tc.name, func(t *testing.T) {
			if got := globMatch(tc.pattern, tc.name); got != tc.want {
				t.Fatalf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
			}
		})
	}
}

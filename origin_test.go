package netsec

import "testing"

func TestResolveEffectiveOrigin(t *testing.T) {
	app := NewTupleOrigin("https", "app.example.com", "")
	appPort := NewTupleOrigin("https", "app.example.com", "8443")
	opaque := OpaqueOrigin()

	cases := []struct {
		name       string
		req        NavigationRequest
		wantOpaque bool
		want       Origin
	}{
		{
			name: "https tuple origin",
			req:  NavigationRequest{URL: "https://app.example.com/path?q=1", Initiator: app},
			want: app,
		},
		{
			name: "tuple origin keeps explicit port",
			req:  NavigationRequest{URL: "https://app.example.com:8443/", Initiator: app},
			want: appPort,
		},
		{
			name: "about:blank inherits initiator",
			req:  NavigationRequest{URL: "about:blank", Initiator: app},
			want: app,
		},
		{
			name: "about:srcdoc inherits initiator",
			req:  NavigationRequest{URL: "about:srcdoc", Initiator: app},
			want: app,
		},
		{
			name:       "about:blank without initiator is opaque",
			req:        NavigationRequest{URL: "about:blank"},
			wantOpaque: true,
		},
		{
			name:       "about:srcdoc from opaque initiator stays opaque",
			req:        NavigationRequest{URL: "about:srcdoc", Initiator: opaque},
			wantOpaque: true,
		},
		{
			name: "blob embeds creator tuple origin",
			req: NavigationRequest{
				URL:       "blob:https://app.example.com/550e8400-e29b-41d4-a716-446655440000",
				Initiator: NewTupleOrigin("https", "other.example", ""),
			},
			want: app,
		},
		{
			name: "blob without embedded origin falls back to initiator",
			req:  NavigationRequest{URL: "blob:null/guid", Initiator: app},
			want: app,
		},
		{
			name:       "blob with no embedded origin and no initiator is opaque",
			req:        NavigationRequest{URL: "blob:null/guid"},
			wantOpaque: true,
		},
		{
			name:       "data url is always opaque even with trusted initiator",
			req:        NavigationRequest{URL: "data:text/html,<h1>hi</h1>", Initiator: app},
			wantOpaque: true,
		},
		{
			name:       "unparseable relative string is opaque",
			req:        NavigationRequest{URL: "just-a-relative-string", Initiator: app},
			wantOpaque: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEffectiveOrigin(tc.req)
			if tc.wantOpaque {
				if !got.IsOpaque() {
					t.Fatalf("expected opaque origin, got %s", got)
				}
				return
			}
			if got.IsOpaque() {
				t.Fatalf("expected tuple origin %s, got opaque", tc.want)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("origin mismatch: got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestOpaqueOriginsNeverEqual(t *testing.T) {
	if OpaqueOrigin().Equal(OpaqueOrigin()) {
		t.Fatal("two freshly minted opaque origins must not be equal")
	}
	app := NewTupleOrigin("https", "app.example.com", "")
	if app.Equal(OpaqueOrigin()) || OpaqueOrigin().Equal(app) {
		t.Fatal("tuple origin must not equal an opaque origin")
	}
	if !app.Equal(NewTupleOrigin("HTTPS", "APP.Example.COM", "")) {
		t.Fatal("scheme/host case differences must normalize to the same origin")
	}
}

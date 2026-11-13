package netsec

import (
	"errors"
	"testing"
)

func TestValidateRedirectURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authorization string
		token         string
		wantErr       error
	}{
		{
			name:          "identical simple URI",
			authorization: "https://app.example/callback",
			token:         "https://app.example/callback",
		},
		{
			name:          "identical URI with query",
			authorization: "https://app.example/callback?state=abc&scope=openid",
			token:         "https://app.example/callback?state=abc&scope=openid",
		},
		{
			name:          "identical URI with duplicate query parameters",
			authorization: "https://app.example/callback?a=1&a=2&b=3",
			token:         "https://app.example/callback?a=1&a=2&b=3",
		},
		{
			name:          "identical loopback URI",
			authorization: "http://127.0.0.1:8080/callback?state=x",
			token:         "http://127.0.0.1:8080/callback?state=x",
		},
		{
			name:          "identical custom scheme URI",
			authorization: "com.example.app:/oauth2redirect",
			token:         "com.example.app:/oauth2redirect",
		},
		{
			name:          "different redirect URI",
			authorization: "https://app.example/callback",
			token:         "https://attacker.example/callback",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "different host",
			authorization: "https://app.example/callback",
			token:         "https://app.example.evil.example/callback",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "different path",
			authorization: "https://app.example/callback",
			token:         "https://app.example/other",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "different port",
			authorization: "https://app.example:443/callback",
			token:         "https://app.example/callback",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "query parameters reordered",
			authorization: "https://app.example/callback?state=abc&scope=openid",
			token:         "https://app.example/callback?scope=openid&state=abc",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "query value changed",
			authorization: "https://app.example/callback?state=abc",
			token:         "https://app.example/callback?state=def",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "query parameter added",
			authorization: "https://app.example/callback?state=abc",
			token:         "https://app.example/callback?state=abc&next=https://attacker.example",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "different percent encoding",
			authorization: "https://app.example/callback?next=https%3A%2F%2Fapp.example",
			token:         "https://app.example/callback?next=https://app.example",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "different scheme case",
			authorization: "https://app.example/callback",
			token:         "HTTPS://app.example/callback",
			wantErr:       ErrRedirectURIMismatch,
		},
		{
			name:          "userinfo added to token URI",
			authorization: "https://app.example/callback",
			token:         "https://app.example@attacker.example/callback",
			wantErr:       ErrRedirectURIUserInfo,
		},
		{
			name:          "userinfo added to identical URI",
			authorization: "https://user:pass@app.example/callback",
			token:         "https://user:pass@app.example/callback",
			wantErr:       ErrRedirectURIUserInfo,
		},
		{
			name:          "empty userinfo added",
			authorization: "https://app.example/callback",
			token:         "https://@app.example/callback",
			wantErr:       ErrRedirectURIUserInfo,
		},
		{
			name:          "fragment added to token URI",
			authorization: "https://app.example/callback",
			token:         "https://app.example/callback#fragment",
			wantErr:       ErrRedirectURIFragment,
		},
		{
			name:          "fragment added to authorization URI",
			authorization: "https://app.example/callback#fragment",
			token:         "https://app.example/callback",
			wantErr:       ErrRedirectURIFragment,
		},
		{
			name:          "empty fragment",
			authorization: "https://app.example/callback",
			token:         "https://app.example/callback#",
			wantErr:       ErrRedirectURIFragment,
		},
		{
			name:          "empty authorization URI",
			authorization: "",
			token:         "",
			wantErr:       ErrInvalidRedirectURI,
		},
		{
			name:          "relative URI",
			authorization: "/callback",
			token:         "/callback",
			wantErr:       ErrInvalidRedirectURI,
		},
		{
			name:          "https URI without host",
			authorization: "https:/callback",
			token:         "https:/callback",
			wantErr:       ErrInvalidRedirectURI,
		},
		{
			name:          "http URI with empty hostname",
			authorization: "https://:443/callback",
			token:         "https://:443/callback",
			wantErr:       ErrInvalidRedirectURI,
		},
		{
			name:          "empty custom scheme target",
			authorization: "com.example.app:",
			token:         "com.example.app:",
			wantErr:       ErrInvalidRedirectURI,
		},
		{
			name:          "malformed escape",
			authorization: "https://app.example/%zz",
			token:         "https://app.example/%zz",
			wantErr:       ErrInvalidRedirectURI,
		},
		{
			name:          "space in URI",
			authorization: "https://app.example/callback path",
			token:         "https://app.example/callback path",
			wantErr:       ErrInvalidRedirectURI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateRedirectURI(tt.authorization, tt.token)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateRedirectURI() error = %v, want nil", err)
				}
				if !RedirectURIMatches(tt.authorization, tt.token) {
					t.Fatal("RedirectURIMatches() = false, want true")
				}
				return
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateRedirectURI() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
			if RedirectURIMatches(tt.authorization, tt.token) {
				t.Fatal("RedirectURIMatches() = true, want false")
			}
		})
	}
}

func TestValidateRedirectURIFormat(t *testing.T) {
	t.Parallel()

	valid := []string{
		"https://app.example/callback",
		"http://localhost:3000/callback?x=1",
		"com.example.app://oauth/callback",
		"com.example.app:/oauth2redirect",
	}
	for _, uri := range valid {
		if err := ValidateRedirectURIFormat(uri); err != nil {
			t.Errorf("ValidateRedirectURIFormat(%q) error = %v, want nil", uri, err)
		}
	}

	invalid := []struct {
		uri     string
		wantErr error
	}{
		{"", ErrInvalidRedirectURI},
		{"callback", ErrInvalidRedirectURI},
		{"https://:443/callback", ErrInvalidRedirectURI},
		{"com.example.app:", ErrInvalidRedirectURI},
		{"https://app.example/callback#frag", ErrRedirectURIFragment},
		{"https://user@app.example/callback", ErrRedirectURIUserInfo},
		{"https://app.example/callback\t", ErrInvalidRedirectURI},
	}
	for _, tt := range invalid {
		err := ValidateRedirectURIFormat(tt.uri)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("ValidateRedirectURIFormat(%q) error = %v, want errors.Is(_, %v)", tt.uri, err, tt.wantErr)
		}
	}
}

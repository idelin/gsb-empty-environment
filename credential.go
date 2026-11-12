package netsec

import "net/http"

// Purpose describes why a request is being made. The same origin may be
// allowed different credentials for different purposes; a module
// download and a metadata fetch must not share an identity blindly.
type Purpose string

const (
	// PurposeModuleDownload is the purpose used when fetching module
	// artifacts (zip files) from a download host.
	PurposeModuleDownload Purpose = "module-download"
	// PurposeMetadata is the purpose used when fetching module
	// metadata such as .info, .mod or go.mod files.
	PurposeMetadata Purpose = "metadata"
)

// Credential is an opaque set of HTTP headers attached to a request.
// It is created only by the calling program through its CredentialStore
// configuration; responses from origin servers can never produce one.
type Credential struct {
	headers http.Header
}

// BearerToken builds a credential carrying an Authorization header with
// the given bearer token.
func BearerToken(token string) Credential {
	return NewCredential(http.Header{"Authorization": []string{"Bearer " + token}})
}

// BasicAuth builds a credential carrying an HTTP basic Authorization
// header.
func BasicAuth(username, password string) Credential {
	return NewCredential(http.Header{"Authorization": []string{basicAuthHeader(username, password)}})
}

// NewCredential builds a credential from explicit headers. The header
// map is copied and later applied verbatim to outgoing requests.
func NewCredential(headers http.Header) Credential {
	cloned := make(http.Header, len(headers))
	for name, values := range headers {
		copied := make([]string, len(values))
		copy(copied, values)
		cloned[http.CanonicalHeaderKey(name)] = copied
	}
	return Credential{headers: cloned}
}

// Rule authorizes one credential for a precise origin and purpose.
// A rule never matches a different port, scheme or purpose, so a token
// configured for the private source cannot be authorized for a mirror.
type Rule struct {
	Origin     Origin
	Purpose    Purpose
	Credential Credential
}

// CredentialStore is the caller-owned authorization policy. It is the
// sole authority capable of supplying credentials: an origin's
// responses (including redirects and userinfo in Location URLs) cannot
// add, replace or upgrade credentials.
type CredentialStore struct {
	rules []Rule
}

// NewCredentialStore constructs a store from the caller's explicit
// rules. The first matching rule wins.
func NewCredentialStore(rules ...Rule) *CredentialStore {
	cloned := make([]Rule, len(rules))
	copy(cloned, rules)
	return &CredentialStore{rules: cloned}
}

// CredentialFor re-selects credentials for the normalized target origin
// and the request purpose. It returns ok=false when no rule authorizes
// this exact origin/purpose pair, in which case the request proceeds
// unauthenticated rather than reusing a previous hop's identity.
func (s *CredentialStore) CredentialFor(origin Origin, purpose Purpose) (cred Credential, ok bool) {
	if s == nil {
		return Credential{}, false
	}
	for _, rule := range s.rules {
		if rule.Origin == origin && rule.Purpose == purpose {
			return rule.Credential, true
		}
	}
	return Credential{}, false
}

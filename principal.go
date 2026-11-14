package netsec

import (
	"encoding/json"
	"strings"
	"time"
)

// IdentityType describes the credential class presented to the gateway.
type IdentityType string

const (
	// IdentityAnonymous is an unauthenticated caller.
	IdentityAnonymous IdentityType = "Anonymous"
	// IdentityUser is a normal authenticated account.
	IdentityUser IdentityType = "User"
	// IdentityService is a service/workload identity.
	IdentityService IdentityType = "Service"
	// IdentityTemporary is a temporary/session credential.
	IdentityTemporary IdentityType = "Temporary"
)

// Identity is the principal whose access is being decided.
//
// ID is the base account/user identifier. Tenant is an optional tenant/account
// scope. SessionID identifies a temporary credential. A temporary identity
// with a non-zero ExpiresAt is denied after that instant. Attributes are
// optional condition context values (for example "aws:PrincipalTag/team").
type Identity struct {
	Type       IdentityType        `json:"type,omitempty"`
	ID         string              `json:"id,omitempty"`
	Tenant     string              `json:"tenant,omitempty"`
	SessionID  string              `json:"session_id,omitempty"`
	Temporary  bool                `json:"temporary,omitempty"`
	ExpiresAt  time.Time           `json:"expires_at,omitempty"`
	Attributes map[string][]string `json:"attributes,omitempty"`
}

// Anonymous is the zero-value identity and is useful in tests/callers that
// want to make the caller class explicit.
func Anonymous() Identity {
	return Identity{Type: IdentityAnonymous}
}

// TemporaryIdentity constructs a temporary credential identity.
func TemporaryIdentity(id, tenant, sessionID string, expiresAt time.Time) Identity {
	return Identity{
		Type:      IdentityTemporary,
		ID:        id,
		Tenant:    tenant,
		SessionID: sessionID,
		Temporary: true,
		ExpiresAt: expiresAt,
	}
}

func (id Identity) normalizedType() IdentityType {
	if id.Temporary ||
		strings.EqualFold(string(id.Type), string(IdentityTemporary)) ||
		(strings.TrimSpace(string(id.Type)) == "" && id.SessionID != "") {
		return IdentityTemporary
	}
	switch strings.ToLower(strings.TrimSpace(string(id.Type))) {
	case "anonymous", "anon", "public":
		return IdentityAnonymous
	case "service", "system":
		return IdentityService
	case "temporary", "temp", "session":
		return IdentityTemporary
	case "user", "aws", "account", "":
		if id.ID == "" && id.Tenant == "" && id.SessionID == "" {
			return IdentityAnonymous
		}
		return IdentityUser
	default:
		return IdentityType(strings.TrimSpace(string(id.Type)))
	}
}

func (id Identity) expired(now time.Time) bool {
	return id.normalizedType() == IdentityTemporary && !id.ExpiresAt.IsZero() && !now.Before(id.ExpiresAt)
}

func (id Identity) fingerprint() string {
	type canonicalIdentity struct {
		Type       string              `json:"type"`
		ID         string              `json:"id"`
		Tenant     string              `json:"tenant"`
		SessionID  string              `json:"session_id"`
		Temporary  bool                `json:"temporary"`
		ExpiresAt  string              `json:"expires_at,omitempty"`
		Attributes map[string][]string `json:"attributes,omitempty"`
	}
	canonical := canonicalIdentity{
		Type:       string(id.normalizedType()),
		ID:         id.ID,
		Tenant:     id.Tenant,
		SessionID:  id.SessionID,
		Temporary:  id.Temporary,
		Attributes: id.Attributes,
	}
	if !id.ExpiresAt.IsZero() {
		canonical.ExpiresAt = id.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	data, _ := json.Marshal(canonical)
	return string(data)
}

func identityCandidates(id Identity, domain string) []string {
	switch principalDomainFromString(domain) {
	case "Anonymous":
		return []string{"anonymous", "anon", "public", "Anonymous"}
	case "Temporary":
		return compactStrings(id.SessionID, id.Tenant, id.ID)
	case "Service":
		return compactStrings(id.ID, id.Tenant)
	case "AWS":
		return compactStrings(id.ID, id.Tenant)
	case "*":
		// The authenticated wildcard matches a base account, not a temporary
		// session identity or an anonymous caller.
		return compactStrings(id.ID, id.Tenant)
	case "Any":
		return compactStrings(id.ID, id.Tenant, id.SessionID, "anonymous")
	default:
		return compactStrings(id.ID, id.Tenant, id.SessionID)
	}
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func (ps PrincipalSet) matches(id Identity) bool {
	if len(ps) == 0 {
		return false
	}
	typ := id.normalizedType()
	for _, principal := range ps {
		if principal.matches(id, typ) {
			return true
		}
	}
	return false
}

func (p Principal) matches(id Identity, typ IdentityType) bool {
	domain := principalDomainFromString(p.Type)
	if domain == "" {
		return false
	}
	ids := p.IDs
	hasWildcard := false
	for _, id := range ids {
		if strings.ContainsAny(id, "*?") {
			hasWildcard = true
			break
		}
	}

	switch domain {
	case "Any":
		return len(ids) > 0
	case "Anonymous":
		if typ != IdentityAnonymous {
			return false
		}
		if len(ids) == 0 {
			return false
		}
		return matchAnyID(ids, identityCandidates(id, "Anonymous"))
	case "*":
		// Authenticated wildcard: deliberately excludes anonymous and
		// temporary credentials.
		if typ == IdentityAnonymous || typ == IdentityTemporary {
			return false
		}
		if len(ids) == 0 {
			return false
		}
		return matchAnyID(ids, identityCandidates(id, "*"))
	case "Temporary":
		if typ != IdentityTemporary {
			return false
		}
		if len(ids) == 0 {
			return false
		}
		return matchAnyID(ids, identityCandidates(id, "Temporary"))
	case "AWS", "User":
		if typ != IdentityUser && typ != IdentityTemporary {
			return false
		}
		if typ == IdentityTemporary && hasWildcard {
			// Temporary credentials are least-privilege by default: a
			// wildcard account grant does not become a session grant.
			return false
		}
		if len(ids) == 0 {
			return false
		}
		return matchAnyID(ids, identityCandidates(id, "AWS"))
	case "Service":
		if typ != IdentityService {
			return false
		}
		if len(ids) == 0 {
			return false
		}
		return matchAnyID(ids, identityCandidates(id, "Service"))
	default:
		if !strings.EqualFold(string(typ), domain) {
			return false
		}
		if len(ids) == 0 {
			return false
		}
		return matchAnyID(ids, identityCandidates(id, domain))
	}
}

func matchAnyID(patterns []string, candidates []string) bool {
	if len(candidates) == 0 {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate == pattern || (strings.ContainsAny(pattern, "*?") && globMatch(pattern, candidate)) {
				return true
			}
		}
	}
	return false
}

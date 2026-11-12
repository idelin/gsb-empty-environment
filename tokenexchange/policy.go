package tokenexchange

// AudiencePolicy decides which target audiences a given requester is allowed
// to exchange a token for.
type AudiencePolicy interface {
	AllowedAudience(requester Requester, audience string) bool
}

// AudienceTable is the caller-supplied mapping from a requesting party (keyed
// by client_id or subject) to the audiences that party may obtain tokens for.
// It must not be mutated after being handed to the Engine.
type AudienceTable map[string][]string

// NewAudienceTable clones table into an AudienceTable.
func NewAudienceTable(table map[string][]string) AudienceTable {
	cloned := make(AudienceTable, len(table))
	for key, audiences := range table {
		cloned[key] = append([]string(nil), audiences...)
	}
	return cloned
}

// AllowedAudience reports whether requester may obtain a token for audience.
func (t AudienceTable) AllowedAudience(requester Requester, audience string) bool {
	for _, key := range []string{requester.ClientID, requester.Subject} {
		if key == "" {
			continue
		}
		for _, allowed := range t[key] {
			if allowed == audience {
				return true
			}
		}
	}
	return false
}

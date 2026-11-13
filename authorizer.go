package netsec

import (
	"errors"
	"sort"
	"strings"
)

// ReplyNoSuchArticle is the canonical reply for both a missing article and an
// article the caller is not permitted to read. The two cases must not be
// distinguishable by status, text or length.
const ReplyNoSuchArticle = "430 No such article"

// NoSuchArticleReply is an alias for ReplyNoSuchArticle.
const NoSuchArticleReply = ReplyNoSuchArticle

// ErrNoSuchArticle is returned for both missing and unreadable articles.
var ErrNoSuchArticle = errors.New(ReplyNoSuchArticle)

// Authorizer holds the groups a caller may read. Entries may be exact group
// names or wildmat patterns such as "comp.*".
type Authorizer struct {
	exact    map[string]struct{}
	patterns []string
}

// NewAuthorizer builds an authorizer from readable group names or wildmat
// patterns.
func NewAuthorizer(readableGroups []string) *Authorizer {
	a := &Authorizer{exact: make(map[string]struct{})}
	for _, group := range readableGroups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if strings.ContainsAny(group, "*?[") {
			a.patterns = append(a.patterns, group)
			continue
		}
		a.exact[group] = struct{}{}
	}
	sort.Strings(a.patterns)
	return a
}

// New is an alias for NewAuthorizer.
func New(readableGroups []string) *Authorizer {
	return NewAuthorizer(readableGroups)
}

// NewAuthorizerFromMap builds an authorizer from a group-to-readable map.
// Only groups mapped to true are readable.
func NewAuthorizerFromMap(readable map[string]bool) *Authorizer {
	groups := make([]string, 0, len(readable))
	for group, ok := range readable {
		if ok {
			groups = append(groups, group)
		}
	}
	return NewAuthorizer(groups)
}

// CanReadGroup reports whether the caller may read group. It considers both
// exact permissions and wildmat permission patterns.
func (a *Authorizer) CanReadGroup(group string) bool {
	if a == nil || group == "" {
		return false
	}
	if _, ok := a.exact[group]; ok {
		return true
	}
	for _, pattern := range a.patterns {
		if MatchGroup(pattern, group) {
			return true
		}
	}
	return false
}

// CanRead is an alias for CanReadGroup.
func (a *Authorizer) CanRead(group string) bool {
	return a.CanReadGroup(group)
}

// FilterGroups returns the readable members of groups, preserving input order
// and removing duplicates. It never expands or evaluates a wildcard supplied
// by the caller.
func (a *Authorizer) FilterGroups(groups []string) []string {
	out := make([]string, 0, len(groups))
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		if a.CanReadGroup(group) {
			out = append(out, group)
		}
	}
	return out
}

// ReadableGroups is an alias for FilterGroups.
func (a *Authorizer) ReadableGroups(groups []string) []string {
	return a.FilterGroups(groups)
}

// ExpandGroups expands pattern against available groups after removing every
// group the caller cannot read. Filtering before expansion is important: a
// wildcard must not make restricted groups observable even indirectly.
func (a *Authorizer) ExpandGroups(pattern string, available []string) []string {
	visible := a.FilterGroups(available)
	out := make([]string, 0, len(visible))
	for _, group := range visible {
		if MatchGroup(pattern, group) {
			out = append(out, group)
		}
	}
	return out
}

// VisibleXrefGroups returns the Xref groups the caller may read.
func (a *Authorizer) VisibleXrefGroups(xref string) []string {
	parsed := ParseXref(xref)
	out := make([]string, 0, len(parsed.Entries))
	seen := make(map[string]struct{}, len(parsed.Entries))
	for _, entry := range parsed.Entries {
		if !a.CanReadGroup(entry.Group) {
			continue
		}
		if _, ok := seen[entry.Group]; ok {
			continue
		}
		seen[entry.Group] = struct{}{}
		out = append(out, entry.Group)
	}
	return out
}

// CanReadXref reports whether at least one group in the Xref header is
// readable. It intentionally evaluates every entry rather than returning at
// the first match, so the position of a readable group does not create an
// obvious timing signal.
func (a *Authorizer) CanReadXref(xref string) bool {
	parsed := ParseXref(xref)
	allowed := false
	for _, entry := range parsed.Entries {
		if a.CanReadGroup(entry.Group) {
			allowed = true
		}
	}
	return allowed
}

// CanReadArticle is an alias for CanReadXref.
func (a *Authorizer) CanReadArticle(xref string) bool {
	return a.CanReadXref(xref)
}

// FilterXref removes unreadable group locations from an Xref value. If no
// readable location remains, it returns an empty string so the caller can
// omit the header entirely.
func (a *Authorizer) FilterXref(xref string) string {
	parsed := ParseXref(xref)
	filtered := Xref{Server: parsed.Server}
	for _, entry := range parsed.Entries {
		if a.CanReadGroup(entry.Group) {
			filtered.Entries = append(filtered.Entries, entry)
		}
	}
	if len(filtered.Entries) == 0 {
		return ""
	}
	return filtered.String()
}

// FilterArticle returns a copy of art with unreadable Xref locations removed.
// The Xref header is deleted when every location is unreadable.
func (a *Authorizer) FilterArticle(art *Article) *Article {
	if art == nil {
		return nil
	}
	out := art.Clone()
	var xrefValues []string
	hasXref := false
	for key := range out.Headers {
		if !strings.EqualFold(key, "Xref") {
			continue
		}
		hasXref = true
		for _, value := range out.Headers[key] {
			if filtered := a.FilterXref(value); filtered != "" {
				xrefValues = append(xrefValues, filtered)
			}
		}
		delete(out.Headers, key)
	}
	if hasXref && len(xrefValues) > 0 {
		out.Headers["Xref"] = xrefValues
	}
	return out
}

// ArticleLoader loads an article by message ID. found is false when no such
// article exists. Implementations should not expose authorization information
// in the error path.
type ArticleLoader func(messageID string) (art *Article, found bool)

// LookupArticle authorizes an ARTICLE, HEAD or BODY lookup by message ID.
// The article is returned only when its Xref header names at least one group
// the caller may read. Missing and unreadable articles both return exactly
// ErrNoSuchArticle, whose reply text and length are identical.
func (a *Authorizer) LookupArticle(messageID string, load ArticleLoader) (*Article, error) {
	// A fixed candidate is used when the article is absent so that the
	// authorization and filtering work below is performed on both paths.
	candidate := &denialArticle
	found := false
	if load != nil {
		if art, ok := load(messageID); ok && art != nil {
			candidate = art
			found = true
		}
	}

	authorized := a.CanReadXref(candidate.Xref())
	filtered := a.FilterArticle(candidate)
	if !found || !authorized {
		return nil, ErrNoSuchArticle
	}
	return filtered, nil
}

// AuthorizeLookup is the authorization-only form for callers that have already
// loaded an article. xref must be the article's Xref header value.
func (a *Authorizer) AuthorizeLookup(messageID, xref string) error {
	_ = messageID
	if !a.CanReadXref(xref) {
		return ErrNoSuchArticle
	}
	return nil
}

// NoSuchArticleResponse returns the canonical wire reply, including CRLF.
func NoSuchArticleResponse() string {
	return ReplyNoSuchArticle + "\r\n"
}

var denialArticle = Article{
	MessageID: "denied@invalid",
	Headers: map[string][]string{
		"Xref": {"localhost denied.group:1"},
	},
}

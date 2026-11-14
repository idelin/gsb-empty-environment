package scim

import (
	"sort"
	"strings"
)

// Query carries the SCIM list-request parameters to authorize and apply.
type Query struct {
	Filter             string
	SortBy             string
	SortOrder          string // "ascending" (default) or "descending"
	StartIndex         int    // 1-based; 0 is treated as 1
	Count              int    // 0 means policy default (MaxPageSize)
	Attributes         []string
	ExcludedAttributes []string
}

// Result is the authorized, paginated and projected response.
type Result struct {
	TotalResults int
	StartIndex   int
	ItemsPerPage int
	Resources    []map[string]any
}

// Authorizer applies a Policy to SCIM queries on behalf of one tenant.
type Authorizer struct {
	pol Policy
}

// NewAuthorizer builds an Authorizer with the given policy.
func NewAuthorizer(p Policy) *Authorizer { return &Authorizer{pol: p} }

// ParseAndValidate parses and authorizes a filter string in one step.
func (a *Authorizer) ParseAndValidate(filter string) (Expr, error) {
	expr, err := Parse(filter)
	if err != nil {
		return nil, err
	}
	if err := a.pol.Validate(expr); err != nil {
		return nil, err
	}
	return expr, nil
}

// ScopeToTenant returns only the users belonging to tenantID. This is the
// hard isolation boundary: filtering, sorting, pagination and projection
// all operate exclusively on the scoped set, so no filter construct
// (OR, NOT, valuePath, cross-tenant id) can widen it.
func ScopeToTenant(users []User, tenantID string) []*User {
	var scoped []*User
	for i := range users {
		if users[i].TenantID == tenantID {
			scoped = append(scoped, &users[i])
		}
	}
	return scoped
}

// Query authorizes and executes q for tenantID against the directory.
func (a *Authorizer) Query(tenantID string, users []User, q Query) (*Result, error) {
	if tenantID == "" {
		return nil, newError(ErrCodeUnauthorizedAttr, "tenant scope must not be empty")
	}

	var expr Expr
	if strings.TrimSpace(q.Filter) != "" {
		var err error
		expr, err = a.ParseAndValidate(q.Filter)
		if err != nil {
			return nil, err
		}
	}

	sortBy, descending, err := a.authorizeSort(q.SortBy, q.SortOrder)
	if err != nil {
		return nil, err
	}
	start, count, err := a.authorizePagination(q.StartIndex, q.Count)
	if err != nil {
		return nil, err
	}
	projection, err := a.authorizeProjection(q.Attributes, q.ExcludedAttributes)
	if err != nil {
		return nil, err
	}

	// Tenant scoping happens before any filter evaluation.
	scoped := ScopeToTenant(users, tenantID)

	var matched []*User
	for _, u := range scoped {
		if expr == nil || eval(expr, func(path string) []Value { return userValues(u, path) }) {
			matched = append(matched, u)
		}
	}

	if sortBy != "" {
		sortUsers(matched, sortBy, descending)
	}

	total := len(matched)
	page := paginate(matched, start, count)

	resources := make([]map[string]any, 0, len(page))
	for _, u := range page {
		resources = append(resources, project(u, projection))
	}
	return &Result{
		TotalResults: total,
		StartIndex:   start,
		ItemsPerPage: len(resources),
		Resources:    resources,
	}, nil
}

func (a *Authorizer) authorizeSort(sortBy, sortOrder string) (string, bool, error) {
	descending := false
	switch strings.ToLower(strings.TrimSpace(sortOrder)) {
	case "", "ascending":
	case "descending":
		descending = true
	default:
		return "", false, newError(ErrCodeInvalidSort, "invalid sortOrder %q", sortOrder)
	}
	if strings.TrimSpace(sortBy) == "" {
		return "", descending, nil
	}
	rule, ok := a.pol.Attributes[normalizePath(sortBy)]
	if !ok || !rule.Sortable {
		return "", false, newError(ErrCodeInvalidSort, "attribute %q is not sortable", sortBy)
	}
	return rule.Name, descending, nil
}

func (a *Authorizer) authorizePagination(startIndex, count int) (int, int, error) {
	start := startIndex
	if start == 0 {
		start = 1
	}
	if start < 1 {
		return 0, 0, newError(ErrCodeInvalidPagination, "startIndex must be >= 1, got %d", startIndex)
	}
	c := count
	if c == 0 {
		c = a.pol.MaxPageSize
	}
	if c < 0 {
		return 0, 0, newError(ErrCodeInvalidPagination, "count must be >= 0, got %d", count)
	}
	if c > a.pol.MaxPageSize {
		c = a.pol.MaxPageSize
	}
	return start, c, nil
}

// authorizeProjection resolves the final set of projectable attributes.
// attributes and excludedAttributes may only reference projectable
// attributes; anything else is rejected rather than silently ignored.
func (a *Authorizer) authorizeProjection(attributes, excluded []string) ([]AttributeRule, error) {
	if len(attributes) > 0 && len(excluded) > 0 {
		return nil, newError(ErrCodeInvalidProjection,
			"attributes and excludedAttributes are mutually exclusive")
	}
	if len(attributes) > 0 {
		seen := map[string]bool{}
		var rules []AttributeRule
		for _, name := range attributes {
			key := normalizePath(name)
			rule, ok := a.pol.Attributes[key]
			if !ok || !rule.Projectable {
				return nil, newError(ErrCodeInvalidProjection,
					"attribute %q is not authorized for selection", name)
			}
			if !seen[key] {
				seen[key] = true
				rules = append(rules, rule)
			}
		}
		return rules, nil
	}
	excludedSet := map[string]bool{}
	for _, name := range excluded {
		key := normalizePath(name)
		rule, ok := a.pol.Attributes[key]
		if !ok || !rule.Projectable {
			return nil, newError(ErrCodeInvalidProjection,
				"attribute %q is not authorized for exclusion", name)
		}
		excludedSet[key] = true
	}
	var names []string
	for key, rule := range a.pol.Attributes {
		if rule.Projectable && !excludedSet[key] {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	rules := make([]AttributeRule, 0, len(names))
	for _, key := range names {
		rules = append(rules, a.pol.Attributes[key])
	}
	return rules, nil
}

func sortUsers(users []*User, sortBy string, descending bool) {
	key := normalizePath(sortBy)
	less := func(i, j int) bool {
		a := userValues(users[i], key)
		b := userValues(users[j], key)
		var av, bv string
		if len(a) > 0 {
			av = strings.ToLower(a[0].Str)
		}
		if len(b) > 0 {
			bv = strings.ToLower(b[0].Str)
		}
		if av != bv {
			return av < bv
		}
		return users[i].ID < users[j].ID
	}
	sort.SliceStable(users, func(i, j int) bool {
		if descending {
			return less(j, i)
		}
		return less(i, j)
	})
}

func paginate(users []*User, start, count int) []*User {
	if start-1 >= len(users) {
		return nil
	}
	end := start - 1 + count
	if end > len(users) {
		end = len(users)
	}
	return users[start-1 : end]
}

// project applies the authorized attribute selection to a user. Only
// attributes with Projectable rules can appear in the output.
func project(u *User, rules []AttributeRule) map[string]any {
	out := make(map[string]any, len(rules))
	for _, rule := range rules {
		switch normalizePath(rule.Name) {
		case "id":
			out[rule.Name] = u.ID
		case "username":
			out[rule.Name] = u.UserName
		case "displayname":
			out[rule.Name] = u.DisplayName
		case "active":
			out[rule.Name] = u.Active
		case "emails":
			out[rule.Name] = append([]string(nil), u.Emails...)
		case "groups":
			out[rule.Name] = append([]string(nil), u.Groups...)
		}
	}
	return out
}

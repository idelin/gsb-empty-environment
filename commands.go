package netsec

import "strings"

// ArticleRef identifies an article produced by a group-scoped or message-id
// command. Group is the group from which the result was obtained. Xref is
// optional metadata when the command produced a message ID rather than a
// group-local article number.
type ArticleRef struct {
	Group     string
	Number    int64
	MessageID string
	Xref      string
}

// HeaderResult is one HDR or XPAT result row.
type HeaderResult struct {
	Group     string
	Number    int64
	MessageID string
	Xref      string
	Field     string
	Value     string
}

func (r HeaderResult) ref() ArticleRef {
	return ArticleRef{
		Group:     r.Group,
		Number:    r.Number,
		MessageID: r.MessageID,
		Xref:      r.Xref,
	}
}

// CanReadRef reports whether an article reference is visible to the caller.
// A group-scoped reference needs a readable group. A message-id reference
// needs a readable group in its Xref header. If both are present, both checks
// apply.
func (a *Authorizer) CanReadRef(ref ArticleRef) bool {
	switch {
	case ref.Group != "" && ref.Xref != "":
		return a.CanReadGroup(ref.Group) && a.CanReadXref(ref.Xref)
	case ref.Group != "":
		return a.CanReadGroup(ref.Group)
	case ref.Xref != "":
		return a.CanReadXref(ref.Xref)
	default:
		return false
	}
}

// FilterRefs removes references the caller may not read.
func (a *Authorizer) FilterRefs(refs []ArticleRef) []ArticleRef {
	out := make([]ArticleRef, 0, len(refs))
	for _, ref := range refs {
		if a.CanReadRef(ref) {
			out = append(out, ref)
		}
	}
	return out
}

// FilterHDR applies HDR result authorization. When group is non-empty the
// command is group-scoped and the whole result set is denied if that group is
// unreadable. When group is empty (a message-id HDR), each result is checked
// against its Xref header.
func (a *Authorizer) FilterHDR(group string, results []HeaderResult) []HeaderResult {
	out := make([]HeaderResult, 0, len(results))
	if group != "" {
		if !a.CanReadGroup(group) {
			return out
		}
		for _, result := range results {
			if result.Group != "" && !a.CanReadGroup(result.Group) {
				continue
			}
			if result.Xref != "" && !a.CanReadXref(result.Xref) {
				continue
			}
			var ok bool
			result, ok = a.filterHeaderResult(result)
			if !ok {
				continue
			}
			out = append(out, result)
		}
		return out
	}

	for _, result := range results {
		// A message-id HDR result is authorized only by its Xref header.
		if result.Xref == "" && strings.EqualFold(result.Field, "Xref") {
			result.Xref = result.Value
		}
		if result.Xref == "" {
			continue
		}
		if a.CanReadRef(result.ref()) {
			var ok bool
			result, ok = a.filterHeaderResult(result)
			if ok {
				out = append(out, result)
			}
		}
	}
	return out
}

// HDR is an alias for FilterHDR.
func (a *Authorizer) HDR(group string, results []HeaderResult) []HeaderResult {
	return a.FilterHDR(group, results)
}

// FilterXPAT applies XPAT result authorization with the same rules as HDR.
func (a *Authorizer) FilterXPAT(group string, results []HeaderResult) []HeaderResult {
	return a.FilterHDR(group, results)
}

// XPAT is an alias for FilterXPAT.
func (a *Authorizer) XPAT(group string, results []HeaderResult) []HeaderResult {
	return a.FilterXPAT(group, results)
}

// FilterNEWNEWS filters NEWNEWS candidates. The caller supplies the server's
// available groups and the command's wildmat; the wildcard is expanded only
// over groups the caller can read. A result is retained only when it came
// from one of those expanded groups.
func (a *Authorizer) FilterNEWNEWS(pattern string, availableGroups []string, refs []ArticleRef) []ArticleRef {
	if len(availableGroups) == 0 {
		availableGroups = groupsFromRefs(refs)
	}
	expanded := a.ExpandGroups(pattern, availableGroups)
	allowed := make(map[string]struct{}, len(expanded))
	for _, group := range expanded {
		allowed[group] = struct{}{}
	}

	out := make([]ArticleRef, 0, len(refs))
	for _, ref := range refs {
		switch {
		case ref.Group != "":
			if _, ok := allowed[ref.Group]; !ok {
				continue
			}
			if ref.Xref != "" && !a.CanReadXref(ref.Xref) {
				continue
			}
			out = append(out, ref)

		case ref.Xref != "":
			if a.xrefInGroups(ref.Xref, allowed) {
				out = append(out, ref)
			}
		}
	}
	return out
}

// FilterNewNews is an alias for FilterNEWNEWS.
func (a *Authorizer) FilterNewNews(pattern string, availableGroups []string, refs []ArticleRef) []ArticleRef {
	return a.FilterNEWNEWS(pattern, availableGroups, refs)
}

// NEWNEWS is an alias for FilterNEWNEWS.
func (a *Authorizer) NEWNEWS(pattern string, availableGroups []string, refs []ArticleRef) []ArticleRef {
	return a.FilterNEWNEWS(pattern, availableGroups, refs)
}

func (a *Authorizer) xrefInGroups(xref string, allowed map[string]struct{}) bool {
	parsed := ParseXref(xref)
	found := false
	for _, entry := range parsed.Entries {
		if _, ok := allowed[entry.Group]; ok {
			found = true
		}
	}
	return found
}

func groupsFromRefs(refs []ArticleRef) []string {
	groups := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	add := func(group string) {
		if group == "" {
			return
		}
		if _, ok := seen[group]; ok {
			return
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	for _, ref := range refs {
		add(ref.Group)
		parsed := ParseXref(ref.Xref)
		for _, entry := range parsed.Entries {
			add(entry.Group)
		}
	}
	return groups
}

func (a *Authorizer) filterHeaderResult(result HeaderResult) (HeaderResult, bool) {
	if !strings.EqualFold(result.Field, "Xref") {
		return result, true
	}
	result.Value = a.FilterXref(result.Value)
	return result, result.Value != ""
}

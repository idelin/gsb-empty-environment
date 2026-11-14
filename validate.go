package netsec

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

func validateName(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidJob, kind)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s has surrounding whitespace", ErrInvalidJob, kind)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalidJob, kind)
		}
	}
	return nil
}

func validateToken(kind, value string) error {
	if err := validateName(kind, value); err != nil {
		return err
	}
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '-', r == '_', r == '.', r == ':', r == '/':
		default:
			return fmt.Errorf("%w: %s contains %q", ErrInvalidJob, kind, r)
		}
	}
	return nil
}

func validateDomain(domain TrustDomain, allowed map[TrustDomain]struct{}) error {
	if domain == "" {
		return fmt.Errorf("%w: domain is empty", ErrUnknownDomain)
	}
	if _, ok := allowed[domain]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownDomain, domain)
	}
	return nil
}

func validateLabels(labels []string) error {
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if err := validateToken("label", label); err != nil {
			return err
		}
		if _, ok := seen[label]; ok {
			return fmt.Errorf("%w: duplicate label %q", ErrInvalidJob, label)
		}
		seen[label] = struct{}{}
	}
	return nil
}

func validateCapabilities(capabilities []Capability) error {
	seen := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if err := validateToken("capability", string(capability)); err != nil {
			return err
		}
		if _, ok := seen[capability]; ok {
			return fmt.Errorf("%w: duplicate capability %q", ErrInvalidJob, capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

// canonicalBranch validates a branch name using the important safety rules
// from git-check-ref-format, without accepting refs outside refs/heads/.
func canonicalBranch(branch string) (string, error) {
	branch = strings.TrimPrefix(branch, "refs/heads/")
	if branch == "" {
		return "", fmt.Errorf("%w: branch is empty", ErrInvalidRef)
	}
	if branch == "@" {
		return "", fmt.Errorf("%w: branch is @", ErrInvalidRef)
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") ||
		strings.Contains(branch, "//") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "@{") || strings.HasSuffix(branch, ".") {
		return "", fmt.Errorf("%w: malformed branch %q", ErrInvalidRef, branch)
	}
	for _, r := range branch {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", fmt.Errorf("%w: branch contains whitespace/control character", ErrInvalidRef)
		}
		switch r {
		case '~', '^', ':', '?', '*', '[', '\\':
			return "", fmt.Errorf("%w: branch contains %q", ErrInvalidRef, r)
		}
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || part == "." || part == ".." ||
			strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return "", fmt.Errorf("%w: malformed branch component %q", ErrInvalidRef, part)
		}
	}
	return branch, nil
}

func validatePattern(pattern string) (string, error) {
	pattern = strings.TrimPrefix(pattern, "refs/heads/")
	if pattern == "" {
		return "", fmt.Errorf("%w: empty protected branch pattern", ErrInvalidRepository)
	}
	if strings.HasPrefix(pattern, "/") || strings.HasSuffix(pattern, "/") ||
		strings.Contains(pattern, "//") || strings.Contains(pattern, "..") ||
		strings.Contains(pattern, "@{") {
		return "", fmt.Errorf("%w: malformed protected branch pattern %q", ErrInvalidRepository, pattern)
	}
	if strings.Count(pattern, "**") > 0 &&
		pattern != "**" && !strings.HasSuffix(pattern, "/**") {
		return "", fmt.Errorf("%w: ** is only supported at the end of a pattern", ErrInvalidRepository)
	}
	for _, r := range pattern {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", fmt.Errorf("%w: pattern contains whitespace/control character", ErrInvalidRepository)
		}
		switch r {
		case '~', '^', ':', '\\':
			return "", fmt.Errorf("%w: pattern contains %q", ErrInvalidRepository, r)
		}
	}
	if _, err := path.Match(pattern, "candidate"); err != nil {
		return "", fmt.Errorf("%w: malformed protected branch pattern %q: %v", ErrInvalidRepository, pattern, err)
	}
	return pattern, nil
}

func branchMatches(pattern, branch string) bool {
	if pattern == "**" {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		parts := strings.Split(branch, "/")
		for i := 1; i < len(parts); i++ {
			prefix := strings.Join(parts[:i], "/")
			if matched, err := path.Match(base, prefix); err == nil && matched {
				return true
			}
		}
		return false
	}
	matched, err := path.Match(pattern, branch)
	return err == nil && matched
}

// resolveRef returns the branch only for refs that actually identify a branch.
// Tags, pull refs, and other refs are valid but are never protected branches.
func resolveRef(job Job) (branch string, isBranch bool, err error) {
	if job.Ref == "" && job.Branch == "" {
		return "", false, fmt.Errorf("%w: branch or ref is required", ErrInvalidRef)
	}

	claimed := job.Branch
	if claimed != "" {
		claimed, err = canonicalBranch(claimed)
		if err != nil {
			return "", false, err
		}
	}

	if job.Ref == "" {
		return claimed, true, nil
	}
	if strings.TrimSpace(job.Ref) != job.Ref {
		return "", false, fmt.Errorf("%w: ref has surrounding whitespace", ErrInvalidRef)
	}

	switch {
	case strings.HasPrefix(job.Ref, "refs/heads/"):
		refBranch, err := canonicalBranch(job.Ref)
		if err != nil {
			return "", false, err
		}
		if claimed != "" && claimed != refBranch {
			return "", false, fmt.Errorf("%w: branch %q disagrees with ref %q", ErrInvalidRef, job.Branch, job.Ref)
		}
		return refBranch, true, nil
	case strings.HasPrefix(job.Ref, "refs/"):
		// A tag or pull/merge ref is never a protected branch. Do not fall
		// back to the caller-supplied Branch field.
		return "", false, nil
	default:
		refBranch, err := canonicalBranch(job.Ref)
		if err != nil {
			return "", false, err
		}
		if claimed != "" && claimed != refBranch {
			return "", false, fmt.Errorf("%w: branch %q disagrees with ref %q", ErrInvalidRef, job.Branch, job.Ref)
		}
		return refBranch, true, nil
	}
}

func containsAll(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, value := range have {
		set[value] = struct{}{}
	}
	for _, value := range want {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func containsAllCaps(have, want []Capability) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[Capability]struct{}, len(have))
	for _, value := range have {
		set[value] = struct{}{}
	}
	for _, value := range want {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

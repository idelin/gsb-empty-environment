package impersonate

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// impersonationRequest is one independent "impersonate" authorization check,
// together with enough information to construct the resulting identity.
type impersonationRequest struct {
	attrs     Attributes
	setUser   bool
	setGroups bool
	setUID    bool
	setExtras bool
	userName  string
	groups    []string
	uid       string
	extras    map[string][]string
}

// splitServiceAccountUsername parses a service account username of the form
// system:serviceaccount:<namespace>:<name>.
func splitServiceAccountUsername(username string) (namespace, name string, err error) {
	if !strings.HasPrefix(username, ServiceAccountNamePrefix) {
		return "", "", errors.New("not a service account username")
	}
	parts := strings.Split(username, ":")
	if len(parts) != 4 {
		return "", "", errors.New("malformed service account username")
	}
	return parts[2], parts[3], nil
}

// buildImpersonationRequests validates the impersonation headers on req and
// returns the ordered list of independent authorization checks. An error is
// returned for any malformed or conflicting header.
func buildImpersonationRequests(req *http.Request) ([]impersonationRequest, error) {
	headers := req.Header

	var requests []impersonationRequest
	actingAs := impersonationRequest{extras: map[string][]string{}}

	users, found := headers[ImpersonateUserHeader]
	switch {
	case found && len(users) > 1:
		// Repeated Impersonate-User headers are ambiguous.
		return nil, errors.New("multiple Impersonate-User headers are not allowed")
	case found && len(users) == 1:
		parts := strings.Split(users[0], ",")
		if len(parts) != 1 {
			// Multiple values merged with commas are also ambiguous.
			return nil, errors.New("multiple Impersonate-User values are not allowed")
		}
		actingAs.setUser = true
		actingAs.userName = strings.TrimSpace(users[0])
	}

	if !actingAs.setUser {
		// Group, uid and extra may only be set together with a user.
		if len(headers.Values(ImpersonateGroupHeader)) != 0 {
			return nil, errors.New("Impersonate-Group requested without Impersonate-User")
		}
		if len(headers.Values(ImpersonateUIDHeader)) != 0 {
			return nil, errors.New("Impersonate-Uid requested without Impersonate-User")
		}
		for key := range headers {
			if strings.HasPrefix(key, ImpersonateExtraHeaderPrefix) {
				return nil, errors.New("Impersonate-Extra requested without Impersonate-User")
			}
		}
		// No impersonation requested at all.
		return nil, nil
	}

	// The user itself is impersonated. A service account username maps to the
	// serviceaccounts resource in the core API group; everything else maps to
	// the users resource with the username as resourceName.
	if namespace, name, err := splitServiceAccountUsername(actingAs.userName); err == nil {
		requests = append(requests, impersonationRequest{
			attrs: Attributes{
				Verb:      VerbImpersonate,
				APIGroup:  ServiceAccountAPIGroup,
				Namespace: namespace,
				Resource:  ResourceServiceAccounts,
				Name:      name,
			},
		})
	} else {
		requests = append(requests, impersonationRequest{
			attrs: Attributes{
				Verb:     VerbImpersonate,
				Resource: ResourceUsers,
				Name:     actingAs.userName,
			},
		})
	}

	// Groups: comma-separated values are merged across repeated headers.
	groups := headers.Values(ImpersonateGroupHeader)
	if len(groups) != 0 {
		actingAs.setGroups = true
		for _, group := range groups {
			for _, part := range strings.Split(group, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				// Each distinct group is one object and therefore one check,
				// even when it appears in several headers.
				if contains(actingAs.groups, part) {
					continue
				}
				actingAs.groups = append(actingAs.groups, part)
				requests = append(requests, impersonationRequest{
					attrs: Attributes{
						Verb:     VerbImpersonate,
						Resource: ResourceGroups,
						Name:     part,
					},
				})
			}
		}
	}

	// UID: a single value, no comma merging.
	uids, found := headers[ImpersonateUIDHeader]
	switch {
	case found && len(uids) > 1:
		return nil, errors.New("multiple Impersonate-Uid headers are not allowed")
	case found && len(uids) == 1:
		parts := strings.Split(uids[0], ",")
		if len(parts) != 1 {
			return nil, errors.New("multiple Impersonate-Uid values are not allowed")
		}
		actingAs.setUID = true
		actingAs.uid = strings.TrimSpace(uids[0])
		requests = append(requests, impersonationRequest{
			attrs: Attributes{
				Verb:     VerbImpersonate,
				Resource: ResourceUIDs,
				Name:     actingAs.uid,
			},
		})
	}

	// Extras: header suffixes are percent-decoded and lower-cased before the
	// authorization check. Each distinct key is one userextras/<key> check.
	extras, extraChecks, err := getImpersonationExtras(headers)
	if err != nil {
		return nil, err
	}
	if len(extras) != 0 {
		actingAs.setExtras = true
		actingAs.extras = extras
		requests = append(requests, extraChecks...)
	}

	requests = append(requests, actingAs)
	return requests, nil
}

// getImpersonationExtras parses every Impersonate-Extra-<key> header.
func getImpersonationExtras(headers http.Header) (map[string][]string, []impersonationRequest, error) {
	extras := map[string][]string{}
	var keys []string

	for header, values := range headers {
		if !strings.HasPrefix(header, ImpersonateExtraHeaderPrefix) {
			continue
		}
		encodedKey := strings.TrimPrefix(header, ImpersonateExtraHeaderPrefix)

		decodedKey, err := url.PathUnescape(encodedKey)
		if err != nil {
			return nil, nil, errors.New("invalid percent-encoding in Impersonate-Extra key: " + encodedKey)
		}
		// A surviving percent sign means the key was percent-encoded a second
		// time to disguise its real spelling; reject rather than re-decode.
		if strings.Contains(decodedKey, "%") {
			return nil, nil, errors.New("double-encoded Impersonate-Extra key is not allowed: " + encodedKey)
		}
		key := strings.ToLower(decodedKey)
		if key == "" {
			return nil, nil, errors.New("empty Impersonate-Extra key is not allowed")
		}

		if _, ok := extras[key]; !ok {
			keys = append(keys, key)
		}
		for _, value := range values {
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if !contains(extras[key], part) {
					extras[key] = append(extras[key], part)
				}
			}
		}
	}

	sort.Strings(keys)
	checks := make([]impersonationRequest, 0, len(keys))
	for _, key := range keys {
		checks = append(checks, impersonationRequest{
			attrs: Attributes{
				Verb:        VerbImpersonate,
				Resource:    ResourceUserExtras,
				Subresource: key,
			},
		})
	}
	return extras, checks, nil
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// requester builds the impersonated identity from a validated request record.
func (r impersonationRequest) requester() UserInfo {
	return &DefaultUserInfo{
		Name:   r.userName,
		UID:    r.uid,
		Groups: r.groups,
		Extra:  r.extras,
	}
}

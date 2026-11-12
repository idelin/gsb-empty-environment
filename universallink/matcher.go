package universallink

import (
	"errors"
	"fmt"
	"strings"
)

// Decision is the outcome of evaluating a URL against the components of an
// association document.
type Decision int

const (
	// DecisionNoMatch means no component matched the URL. Universal links do
	// not open the app in this case.
	DecisionNoMatch Decision = iota
	// DecisionOpen means the first matching component allows the app.
	DecisionOpen
	// DecisionExcluded means the first matching component has exclude set.
	DecisionExcluded
)

// Matcher evaluates URLs against the universal-link rules of one application.
type Matcher struct {
	teamID      string
	association *Association
}

// NewMatcher builds a Matcher for ownAppID (in TEAMID.bundle-identifier form)
// using the given association document. Detail entries whose appIDs do not
// share ownAppID's team ID are ignored.
func NewMatcher(ownAppID string, association *Association) (*Matcher, error) {
	if association == nil {
		return nil, errors.New("universallink: nil association document")
	}
	team, err := teamIDOf(ownAppID)
	if err != nil {
		return nil, err
	}
	return &Matcher{teamID: team, association: association}, nil
}

// ShouldOpen reports whether the app should handle rawURL. Rejected URLs
// (encoded slash, duplicate slash, malformed URL) return an error and false.
func (m *Matcher) ShouldOpen(rawURL string) (bool, error) {
	decision, err := m.Decide(rawURL)
	if err != nil {
		return false, err
	}
	return decision == DecisionOpen, nil
}

// Decide evaluates rawURL against every applicable detail entry, in document
// order. Components are checked in order and the first match wins.
func (m *Matcher) Decide(rawURL string) (Decision, error) {
	target, err := prepareURL(rawURL)
	if err != nil {
		return DecisionNoMatch, err
	}

	for _, detail := range m.association.AppLinks.Details {
		if !m.detailApplies(detail) {
			continue
		}
		for i := range detail.Components {
			component := &detail.Components[i]
			if !component.matches(target) {
				continue
			}
			if component.Exclude {
				return DecisionExcluded, nil
			}
			return DecisionOpen, nil
		}
	}

	return DecisionNoMatch, nil
}

// detailApplies reports whether at least one appID of the detail entry carries
// the matcher's team ID. Malformed appIDs are skipped.
func (m *Matcher) detailApplies(detail Detail) bool {
	for _, appID := range detail.AppIDs {
		team, err := teamIDOf(appID)
		if err != nil {
			continue
		}
		if team == m.teamID {
			return true
		}
	}
	return false
}

// matches reports whether every constrained part of the component matches the
// prepared URL.
func (c *Component) matches(target *preparedURL) bool {
	if c.Path != nil && !globMatch(*c.Path, target.path) {
		return false
	}

	if c.Query != nil {
		values, err := parseQuery(target.rawQuery)
		if err != nil {
			return false
		}
		for key, pattern := range c.Query {
			observed, ok := values[key]
			if !ok {
				return false
			}
			matched := false
			for _, value := range observed {
				if globMatch(pattern, value) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}

	if c.Fragment != nil && !globMatch(*c.Fragment, target.fragment) {
		return false
	}

	return true
}

// teamIDOf extracts the team ID prefix (everything before the first dot) of a
// TEAMID.bundle-identifier appID.
func teamIDOf(appID string) (string, error) {
	team, _, ok := strings.Cut(appID, ".")
	if !ok || team == "" {
		return "", fmt.Errorf("universallink: invalid appID %q, expected TEAMID.bundle-identifier", appID)
	}
	return team, nil
}

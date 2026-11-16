package flowspec

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
)

// FailureCode identifies a validation failure reason.
type FailureCode string

const (
	// FailureUnsupportedAFI: the NLRI address family is neither IPv4 nor IPv6.
	FailureUnsupportedAFI FailureCode = "unsupported_afi"

	// FailureEmptyNLRI: the NLRI contains no components.
	FailureEmptyNLRI FailureCode = "empty_nlri"

	// FailureComponentsOutOfOrder: component types are not strictly ascending.
	FailureComponentsOutOfOrder FailureCode = "components_out_of_order"

	// FailureDuplicateComponent: the same component type appears more than once.
	FailureDuplicateComponent FailureCode = "duplicate_component"

	// FailureMalformedNLRI: a component cannot be decoded or is inconsistent
	// with the address family.
	FailureMalformedNLRI FailureCode = "malformed_nlri"

	// FailureNoBestRoute: no unicast route covers the destination prefix.
	FailureNoBestRoute FailureCode = "no_best_route"

	// FailureForeignOrigin: the best unicast route for the destination prefix
	// is reachable through a different neighbor AS.
	FailureForeignOrigin FailureCode = "foreign_origin"

	// FailureMoreSpecificForeignRoute: a more-specific route inside the
	// destination prefix is reachable through another neighbor AS.
	FailureMoreSpecificForeignRoute FailureCode = "more_specific_foreign_route"
)

// ValidationError describes why a rule failed RFC 8955 section 6 validation.
type ValidationError struct {
	Code   FailureCode
	Rule   Rule
	Prefix netip.Prefix
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Prefix.IsValid() {
		return fmt.Sprintf("flowspec: validation failed (%s) for %s: %s", e.Code, e.Prefix, e.Detail)
	}
	return fmt.Sprintf("flowspec: validation failed (%s): %s", e.Code, e.Detail)
}

// Alert is emitted when a learned rule is rejected.
type Alert struct {
	Code       FailureCode
	Rule       Rule
	Prefix     netip.Prefix
	Reason     string
	NeighborAS ASN
}

// Alerter consumes validation alerts. Implementations must be safe for
// concurrent use.
type Alerter interface {
	FlowSpecAlert(alert Alert)
}

// LogAlerter forwards alerts to a slog.Logger.
type LogAlerter struct {
	Logger *slog.Logger
}

func (a LogAlerter) FlowSpecAlert(alert Alert) {
	logger := a.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("flowspec rule rejected",
		"code", string(alert.Code),
		"neighbor_as", uint32(alert.NeighborAS),
		"prefix", alert.Prefix.String(),
		"reason", alert.Reason,
	)
}

type noopAlerter struct{}

func (noopAlerter) FlowSpecAlert(Alert) {}

// CheckComponentOrder verifies that NLRI components appear in strictly
// ascending order of component type with no duplicates (RFC 8955 section
// 4.2.1/4.2.2, enforced as part of section 6 validation).
func CheckComponentOrder(nlri NLRI) error {
	if len(nlri.Components) == 0 {
		return &ValidationError{
			Code:   FailureEmptyNLRI,
			Detail: "NLRI has no components",
		}
	}
	for i := 1; i < len(nlri.Components); i++ {
		prev, cur := nlri.Components[i-1].Type, nlri.Components[i].Type
		switch {
		case cur == prev:
			return &ValidationError{
				Code:   FailureDuplicateComponent,
				Detail: fmt.Sprintf("duplicate component type %d", cur),
			}
		case cur < prev:
			return &ValidationError{
				Code:   FailureComponentsOutOfOrder,
				Detail: fmt.Sprintf("component type %d follows %d; types must be ascending", cur, prev),
			}
		}
	}
	return nil
}

// Validate applies the RFC 8955 section 6 origin-authentication rules to a
// rule against the supplied unicast RIB. A nil result means the rule may be
// installed.
//
// Rules without a destination-prefix component only undergo structural
// validation, because section 6 constrains the destination prefix.
func Validate(rule Rule, rib RIB) error {
	if rule.NeighborAS == 0 {
		return &ValidationError{
			Code:   FailureForeignOrigin,
			Rule:   rule,
			Detail: "rule has no advertising neighbor AS",
		}
	}
	if rule.NLRI.AFI != AFIIPv4 && rule.NLRI.AFI != AFIIPv6 {
		return &ValidationError{
			Code:   FailureUnsupportedAFI,
			Rule:   rule,
			Detail: fmt.Sprintf("unsupported AFI %d", rule.NLRI.AFI),
		}
	}
	if err := CheckComponentOrder(rule.NLRI); err != nil {
		var vErr *ValidationError
		if errors.As(err, &vErr) {
			vErr.Rule = rule
		}
		return err
	}

	prefix, hasDestination, err := rule.NLRI.DestinationPrefix()
	if err != nil {
		return &ValidationError{
			Code:   FailureMalformedNLRI,
			Rule:   rule,
			Detail: "malformed destination-prefix component: " + err.Error(),
		}
	}
	if !hasDestination {
		return nil
	}

	best, ok := rib.BestRoute(prefix)
	if !ok {
		return &ValidationError{
			Code:   FailureNoBestRoute,
			Rule:   rule,
			Prefix: prefix,
			Detail: "no unicast route covers destination prefix",
		}
	}

	// Section 6 first condition: the best path is reachable through the same
	// neighbor AS. Internal (iBGP/local) best paths satisfy the condition.
	if !best.Internal && best.PeerAS != rule.NeighborAS {
		return &ValidationError{
			Code:   FailureForeignOrigin,
			Rule:   rule,
			Prefix: prefix,
			Detail: fmt.Sprintf("best route reached through neighbor AS %d, rule from AS %d", best.PeerAS, rule.NeighborAS),
		}
	}

	// Section 6 second condition: no more-specific route reached through
	// another neighbor AS may exist inside the destination prefix.
	for _, moreSpecific := range rib.MoreSpecificRoutes(prefix) {
		if moreSpecific.Internal {
			continue
		}
		if moreSpecific.PeerAS != rule.NeighborAS {
			return &ValidationError{
				Code:   FailureMoreSpecificForeignRoute,
				Rule:   rule,
				Prefix: prefix,
				Detail: fmt.Sprintf("more-specific route %s reached through neighbor AS %d",
					moreSpecific.Prefix, moreSpecific.PeerAS),
			}
		}
	}

	return nil
}

package netsec

import (
	"errors"
	"fmt"
	"net/netip"
)

// Mode selects the unicast reverse path forwarding (uRPF) check to apply.
type Mode uint8

const (
	// ModeStrict requires a best route to the source to use the ingress
	// interface. All equal-cost best routes are considered.
	ModeStrict Mode = iota
	// ModeFeasible accepts when any non-blackhole matching route uses the
	// ingress interface, even if that route is not the best route.
	ModeFeasible
	// ModeLoose accepts when any non-blackhole matching route exists. A
	// matching default route is considered only when
	// Config.AllowDefaultForLoose is true.
	ModeLoose
)

// String returns the mode name.
func (m Mode) String() string {
	switch m {
	case ModeStrict:
		return "strict"
	case ModeFeasible:
		return "feasible"
	case ModeLoose:
		return "loose"
	default:
		return "unknown"
	}
}

// Route is one entry in the reverse-path routing table.
//
// Metric is used only to choose best paths in strict mode; lower values are
// preferred. Routes with the same prefix and metric are equal-cost paths. A
// blackhole route never provides a valid return path.
type Route struct {
	Prefix    netip.Prefix
	Egress    string
	Metric    uint32
	Blackhole bool
}

// Config contains table-wide uRPF settings.
type Config struct {
	// AllowDefaultForLoose permits a /0 route to satisfy a loose-mode check.
	// Strict and feasible checks treat default routes as ordinary routes:
	// strict requires the default route to use the ingress interface, while
	// feasible accepts any matching non-blackhole path.
	AllowDefaultForLoose bool
}

// Reason explains the result of a check. Reasons are stable values suitable
// for logging and metrics.
type Reason string

const (
	ReasonAllowStrict       Reason = "strict: best path uses ingress"
	ReasonAllowFeasible     Reason = "feasible: matching path uses ingress"
	ReasonAllowLoose        Reason = "loose: matching route exists"
	ReasonNoRoute           Reason = "no route to source"
	ReasonInvalidSource     Reason = "invalid source address"
	ReasonInvalidMode       Reason = "invalid uRPF mode"
	ReasonNoTable           Reason = "nil routing table"
	ReasonStrictMismatch    Reason = "strict: best path does not use ingress"
	ReasonStrictBlackhole   Reason = "strict: best path is a blackhole"
	ReasonFeasibleMismatch  Reason = "feasible: no matching path uses ingress"
	ReasonFeasibleBlackhole Reason = "feasible: only blackhole routes match"
	ReasonLooseBlackhole    Reason = "loose: only blackhole routes match"
	ReasonDefaultNotAllowed Reason = "loose: default route is not allowed"
)

// Decision is the result of a uRPF check. Allowed is the only field a
// forwarding fast path needs; Reason is intended for diagnostics.
type Decision struct {
	Allowed bool
	Reason  Reason
}

// ErrInvalidRoute is returned by NewTable when a route cannot represent a
// usable reverse path.
var ErrInvalidRoute = errors.New("netsec: invalid route")

// Table is an immutable reverse-path routing table. It is safe to share
// between goroutines and to use concurrently for packet checks.
type Table struct {
	routes []Route
	config Config
}

// NewTable validates and freezes routes for uRPF checks. Prefixes are masked
// to their canonical form. Non-blackhole routes must have a non-empty Egress.
func NewTable(routes []Route, config Config) (*Table, error) {
	frozen := make([]Route, 0, len(routes))
	for i, route := range routes {
		if !route.Prefix.IsValid() {
			return nil, fmt.Errorf("%w at index %d: invalid prefix", ErrInvalidRoute, i)
		}
		route.Prefix = route.Prefix.Masked()
		if !route.Blackhole && route.Egress == "" {
			return nil, fmt.Errorf("%w at index %d: egress interface is empty", ErrInvalidRoute, i)
		}
		frozen = append(frozen, route)
	}
	return &Table{routes: frozen, config: config}, nil
}

// Check evaluates a source address that arrived on ingress.
//
// A source address that is invalid, unspecified, multicast, or IPv4-mapped
// is handled safely: IPv4-mapped addresses are unmapped before lookup, and
// the other invalid source classes are denied.
func (t *Table) Check(source netip.Addr, ingress string, mode Mode) Decision {
	if t == nil {
		return Decision{Reason: ReasonNoTable}
	}
	if !source.IsValid() {
		return Decision{Reason: ReasonInvalidSource}
	}
	source = source.Unmap()
	if source.IsUnspecified() || source.IsMulticast() {
		return Decision{Reason: ReasonInvalidSource}
	}

	switch mode {
	case ModeStrict:
		return t.checkStrict(source, ingress)
	case ModeFeasible:
		return t.checkFeasible(source, ingress)
	case ModeLoose:
		return t.checkLoose(source)
	default:
		return Decision{Reason: ReasonInvalidMode}
	}
}

func (t *Table) checkStrict(source netip.Addr, ingress string) Decision {
	bestBits := -1
	bestMetric := ^uint32(0)
	matched := false

	for _, route := range t.routes {
		if !route.Prefix.Contains(source) {
			continue
		}
		matched = true
		bits := route.Prefix.Bits()
		if bits > bestBits || (bits == bestBits && route.Metric < bestMetric) {
			bestBits = bits
			bestMetric = route.Metric
		}
	}
	if !matched {
		return Decision{Reason: ReasonNoRoute}
	}

	hasViableBest := false
	for _, route := range t.routes {
		if !route.Prefix.Contains(source) ||
			route.Prefix.Bits() != bestBits ||
			route.Metric != bestMetric ||
			route.Blackhole {
			continue
		}
		hasViableBest = true
		if route.Egress == ingress {
			return Decision{Allowed: true, Reason: ReasonAllowStrict}
		}
	}
	if !hasViableBest {
		return Decision{Reason: ReasonStrictBlackhole}
	}
	return Decision{Reason: ReasonStrictMismatch}
}

func (t *Table) checkFeasible(source netip.Addr, ingress string) Decision {
	hasMatch := false
	hasViable := false

	for _, route := range t.routes {
		if !route.Prefix.Contains(source) {
			continue
		}
		hasMatch = true
		if route.Blackhole {
			continue
		}
		hasViable = true
		if route.Egress == ingress {
			return Decision{Allowed: true, Reason: ReasonAllowFeasible}
		}
	}
	if !hasMatch {
		return Decision{Reason: ReasonNoRoute}
	}
	if !hasViable {
		return Decision{Reason: ReasonFeasibleBlackhole}
	}
	return Decision{Reason: ReasonFeasibleMismatch}
}

func (t *Table) checkLoose(source netip.Addr) Decision {
	hasMatch := false
	hasViable := false
	hasDisallowedDefault := false

	for _, route := range t.routes {
		if !route.Prefix.Contains(source) {
			continue
		}
		hasMatch = true
		if route.Blackhole {
			continue
		}
		hasViable = true
		if route.Prefix.Bits() == 0 && !t.config.AllowDefaultForLoose {
			hasDisallowedDefault = true
			continue
		}
		return Decision{Allowed: true, Reason: ReasonAllowLoose}
	}
	if !hasMatch {
		return Decision{Reason: ReasonNoRoute}
	}
	if !hasViable {
		return Decision{Reason: ReasonLooseBlackhole}
	}
	if hasDisallowedDefault {
		return Decision{Reason: ReasonDefaultNotAllowed}
	}
	// Unreachable: hasViable implies a route was either returned or marked as
	// a disallowed default.
	return Decision{Reason: ReasonNoRoute}
}

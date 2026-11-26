package mxadmit

import (
	"fmt"
	"sort"
	"strings"
)

// Tick is an integer time tick.  The package never converts ticks to wall
// clock time; all comparisons are exact integer comparisons.
type Tick int64

// Interval is a closed interval [From, To].  The endpoints are inclusive.
// This matters for signatures whose validity ends exactly when another one
// starts.
type Interval struct {
	From Tick
	To   Tick
}

// Contains reports whether t is inside the closed interval.
func (i Interval) Contains(t Tick) bool {
	return i.From <= t && t <= i.To
}

// Empty reports whether the interval has no integer ticks.
func (i Interval) Empty() bool {
	return i.From > i.To
}

func (i Interval) String() string {
	return fmt.Sprintf("[%d,%d]", i.From, i.To)
}

// Commitment is the set of facts that a connection receipt promises.  The
// Domain field binds the receipt to exactly one message domain.
type Commitment struct {
	Domain        string
	MX            string
	PublicKey     string
	PathDigest    string
	DNSGeneration int64
}

// Equal reports exact commitment equality.  It is intentionally exact: a
// receipt with a different key or path is a different commitment.
func (c Commitment) Equal(other Commitment) bool {
	return c == other
}

func (c Commitment) String() string {
	return fmt.Sprintf("{domain=%q mx=%q key=%q path=%q dns=%d}",
		c.Domain, c.MX, c.PublicKey, c.PathDigest, c.DNSGeneration)
}

// Receipt is an already-observed authenticated connection.  Valid is the
// closed interval in which the connection may be resumed.  Endpoint names the
// observing endpoint; an empty endpoint is a wildcard only when the batch has
// a single concrete endpoint.
type Receipt struct {
	ID        string
	Commitment
	Endpoint string
	Valid    Interval
}

// Authorization is one jointly authorized tuple in a signed configuration.
// A configuration may contain several entries for the same domain, which is
// how same-name key rotation is represented.  PathDigest and PathDigests are
// both accepted for convenience; the receipt path must occur in the union.
type Authorization struct {
	Domain        string
	MX            string
	PublicKey     string
	PathDigest    string
	PathDigests   []string
	DNSGeneration int64
}

func (a Authorization) allowsPath(path string) bool {
	if a.PathDigest != "" && a.PathDigest == path {
		return true
	}
	for _, p := range a.PathDigests {
		if p == path {
			return true
		}
	}
	return false
}

// matchesDNS reports whether the entry has the required domain, MX, key and
// DNS authorization, ignoring the certificate path.
func (a Authorization) matchesDNS(c Commitment) bool {
	return a.Domain == c.Domain &&
		a.MX == c.MX &&
		a.PublicKey == c.PublicKey &&
		a.DNSGeneration == c.DNSGeneration
}

// SignedConfig is a complete signed snapshot for the domains it mentions.
// Entries for other domains are not inherited from another configuration: a
// later configuration that only changes domain "other" does not supersede the
// authorization of the current domain.
//
// Validity is the closed interval in which the signature is effective.
type SignedConfig struct {
	ID       string
	Signer   string
	Validity Interval
	Entries  []Authorization
}

// Domains returns the distinct domains mentioned by the configuration.
func (c SignedConfig) Domains() []string {
	seen := make(map[string]struct{}, len(c.Entries))
	for _, e := range c.Entries {
		seen[e.Domain] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// MentionsDomain reports whether this configuration carries authorization
// entries for domain.  A configuration that does not mention a domain is a
// no-op for that domain.
func (c SignedConfig) MentionsDomain(domain string) bool {
	for _, e := range c.Entries {
		if e.Domain == domain {
			return true
		}
	}
	return false
}

// Authorizes reports whether one entry jointly authorizes the complete
// commitment.  It does not check the signature's validity interval; use
// AuthorizesAt for that.
func (c SignedConfig) Authorizes(want Commitment) bool {
	for _, e := range c.Entries {
		if e.matchesDNS(want) && e.allowsPath(want.PathDigest) {
			return true
		}
	}
	return false
}

// AuthorizesAt additionally requires the signature to be effective at t.
func (c SignedConfig) AuthorizesAt(want Commitment, t Tick) bool {
	return c.Validity.Contains(t) && c.Authorizes(want)
}

// Activation records that a configuration was observed to become active at an
// endpoint at an exact tick.  An empty Endpoint means the activation is
// global and applies to every endpoint.
//
// EventID is used by OrderConstraint.  If empty, a stable identifier is
// derived from config, endpoint and tick.
type Activation struct {
	EventID  string
	ConfigID string
	Endpoint string
	At       Tick
}

// OrderConstraint says that event Before happened before event After.  The
// host may supply these constraints independently for both observed ends.
type OrderConstraint struct {
	Before string
	After  string
}

// Delivery asks whether a message can be sent at At to Endpoint.
//
// ReceiptID identifies an existing connection receipt.  If it is empty, a new
// connection is required, and Candidate must describe the currently observed
// commitment.
//
// ObservedPublicKey and ObservedPathDigest are the certificate facts seen at
// a re-handshake.  They may replace the corresponding receipt facts, but they
// cannot replace MX or DNSGeneration: those remain DNS-authorization facts.
// If Candidate is supplied it must agree with the receipt on Domain, MX and
// DNSGeneration for a re-handshake.
type Delivery struct {
	ID                 string
	Domain             string
	Endpoint           string
	At                 Tick
	ReceiptID          string
	MustDeliver        bool
	ObservedPublicKey  string
	ObservedPathDigest string
	Candidate          *Commitment
}

// Batch is the complete admission input.
type Batch struct {
	Configs     []SignedConfig
	Activations []Activation
	Order       []OrderConstraint
	Receipts    []Receipt
	Deliveries  []Delivery
}

// Action is the result of classifying a delivery.
type Action string

const (
	// ActionUseOriginal means the existing receipt is certainly usable at the
	// delivery tick under every compatible ordering.
	ActionUseOriginal Action = "use-original"
	// ActionRehandshake means the existing receipt is not certainly usable,
	// but a fresh certificate observation can be admitted under every
	// compatible ordering.
	ActionRehandshake Action = "rehandshake"
	// ActionWait means no commitment is certainly authorized now.  A missing
	// DNS authorization or an unresolved ordering cannot be repaired by a
	// re-handshake.
	ActionWait Action = "wait"
)

// Verdict is the classification of one delivery.
type Verdict struct {
	DeliveryID string
	Action     Action
	ReceiptID  string
	Endpoint   string
	Commitment Commitment
	Reasons    []string
}

// PlannedReceipt is one minimal new authentication receipt chosen by Plan.
type PlannedReceipt struct {
	ID          string
	Endpoint    string
	Commitment  Commitment
	Valid       Interval
	DeliveryIDs []string
}

// Obligation identifies an unverified signature or authorization duty that
// prevents a solution.  ConfigID may be empty when no configuration even
// mentions the required tuple.
type Obligation struct {
	DeliveryID string
	ConfigID   string
	Signer     string
	Validity   Interval
	At         Tick
	Required   Commitment
	Reason     string
}

// Plan is the result of PlanSafe.
type Plan struct {
	Feasible    bool
	Verdicts    []Verdict
	NewReceipts []PlannedReceipt
	Obligations []Obligation
	WorldCount  int
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, "; ")
}

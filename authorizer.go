package netsec

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// ControlKind identifies a control operation understood by Authorizer.
type ControlKind string

const (
	ControlNone       ControlKind = ""
	ControlCancel     ControlKind = "cancel"
	ControlSupersedes ControlKind = "supersedes"
)

// Disposition is the action a POST handler should take for an article.
type Disposition string

const (
	// Deliver means the article is an ordinary article and may be stored.
	Deliver Disposition = "deliver"
	// ApplyControl means the cancel or supersedes operation is authorized.
	ApplyControl Disposition = "apply-control"
	// Reject means the article must not be posted or applied.
	Reject Disposition = "reject"
	// Drop means the Path header shows that this site already handled the
	// article, so the article must be discarded rather than posted.
	Drop Disposition = "drop"
)

// Decision is the result of control-message authorization.
type Decision struct {
	Disposition Disposition
	Control     ControlKind
	Target      string
	Reason      string
}

// Allowed reports whether the article may be stored or its control operation
// may be applied.
func (d Decision) Allowed() bool {
	return d.Disposition == Deliver || d.Disposition == ApplyControl
}

const (
	ReasonNotControl          = "not a control message"
	ReasonLoopback            = "loopback path contains local site identifier"
	ReasonMalformedHeader     = "malformed header"
	ReasonFoldedControl       = "Control injected through folded continuation"
	ReasonDuplicateControl    = "duplicate Control header"
	ReasonDuplicateSupersedes = "duplicate Supersedes header"
	ReasonMalformedControl    = "malformed control message"
	ReasonUnsupportedControl  = "unsupported control command"
	ReasonMissingCancelKey    = "cancel or supersedes requires Cancel-Key"
	ReasonMalformedCancelKey  = "malformed Cancel-Key"
	ReasonMalformedCancelLock = "malformed Cancel-Lock"
	ReasonUnknownOriginal     = "original article not found"
	ReasonCancelKeyMismatch   = "Cancel-Key does not match original Cancel-Lock"
	ReasonNoLockAdminOnly     = "original article has no Cancel-Lock; administrator sender required"
	ReasonMatchedCancelKey    = "Cancel-Key matched original Cancel-Lock"
	ReasonAdminSender         = "administrator sender"
)

// Config controls local policy. SiteID is the local Path identity used for
// loop detection. AdminSenders contains mailbox addresses that may issue a
// cancel or supersedes for an original article without a Cancel-Lock.
type Config struct {
	SiteID       string
	AdminSenders []string
}

// OriginalLookup resolves the Message-ID of the article being cancelled or
// superseded. The returned headers should have been produced by ParseHeader.
type OriginalLookup interface {
	Lookup(messageID string) (Header, bool, error)
}

// LookupFunc adapts a function to OriginalLookup.
type LookupFunc func(messageID string) (Header, bool, error)

func (f LookupFunc) Lookup(messageID string) (Header, bool, error) {
	return f(messageID)
}

// Authorizer validates control messages for a POST handler.
type Authorizer struct {
	siteID string
	admins map[string]struct{}
	lookup OriginalLookup
}

// NewAuthorizer constructs an authorizer. The lookup may be nil only when the
// server never expects cancel or supersedes messages; those messages will fail
// closed.
func NewAuthorizer(config Config, lookup OriginalLookup) (*Authorizer, error) {
	siteID := normalizeSiteIdentity(config.SiteID)
	if siteID == "" {
		return nil, errors.New("SiteID must not be empty")
	}

	admins := make(map[string]struct{}, len(config.AdminSenders))
	for _, raw := range config.AdminSenders {
		address, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid administrator sender %q: %w", raw, err)
		}
		admins[strings.ToLower(address.Address)] = struct{}{}
	}

	return &Authorizer{siteID: siteID, admins: admins, lookup: lookup}, nil
}

// Authorize parses and authorizes raw's headers. Header policy failures are
// returned as Reject or Drop decisions. A non-nil error means the configured
// original-article lookup failed and the handler should not accept the post.
func (a *Authorizer) Authorize(raw []byte) (Decision, error) {
	return a.AuthorizeAs(raw, "")
}

// AuthorizeAs is like Authorize, but authenticatedSender is the identity
// established by the POST handler (for example, the authenticated NNTP user).
// When it is non-empty, it is used instead of the article's unauthenticated
// From header for the administrator-only, no-Cancel-Lock fallback. Production
// handlers should use this form whenever they have an authenticated identity.
func (a *Authorizer) AuthorizeAs(raw []byte, authenticatedSender string) (Decision, error) {
	headers, err := ParseHeader(raw)
	if err != nil {
		return parseErrorDecision(err), nil
	}

	if a.pathContainsLocalSite(headers) {
		return Decision{Disposition: Drop, Reason: ReasonLoopback}, nil
	}

	control, err := parseControl(headers)
	if err != nil {
		return Decision{
			Disposition: Reject,
			Reason:      ReasonMalformedControl,
		}, nil
	}
	if control.kind == ControlNone {
		return Decision{
			Disposition: Deliver,
			Reason:      ReasonNotControl,
		}, nil
	}
	if control.kind != ControlCancel && control.kind != ControlSupersedes {
		return Decision{
			Disposition: Reject,
			Control:     control.kind,
			Reason:      ReasonUnsupportedControl,
		}, nil
	}

	keys, err := parseCancelElements(headers.Values("Cancel-Key"))
	if err != nil {
		return Decision{
			Disposition: Reject,
			Control:     control.kind,
			Target:      control.target,
			Reason:      ReasonMalformedCancelKey,
		}, nil
	}
	if len(keys) == 0 {
		return Decision{
			Disposition: Reject,
			Control:     control.kind,
			Target:      control.target,
			Reason:      ReasonMissingCancelKey,
		}, nil
	}

	if a.lookup == nil {
		return Decision{
			Disposition: Reject,
			Control:     control.kind,
			Target:      control.target,
			Reason:      ReasonUnknownOriginal,
		}, nil
	}
	original, found, err := a.lookup.Lookup(control.target)
	if err != nil {
		return Decision{}, fmt.Errorf("look up original article %s: %w", control.target, err)
	}
	if !found {
		return Decision{
			Disposition: Reject,
			Control:     control.kind,
			Target:      control.target,
			Reason:      ReasonUnknownOriginal,
		}, nil
	}
	if original == nil {
		return Decision{
			Disposition: Reject,
			Control:     control.kind,
			Target:      control.target,
			Reason:      ReasonUnknownOriginal,
		}, nil
	}

	lockValues := original.Values("Cancel-Lock")
	base := Decision{
		Disposition: Reject,
		Control:     control.kind,
		Target:      control.target,
	}
	if len(lockValues) > 0 {
		locks, err := parseCancelElements(lockValues)
		if err != nil {
			base.Reason = ReasonMalformedCancelLock
			return base, nil
		}
		if !verifyCancelKeys(keys, locks) {
			base.Reason = ReasonCancelKeyMismatch
			return base, nil
		}
		base.Disposition = ApplyControl
		base.Reason = ReasonMatchedCancelKey
		return base, nil
	}

	if !a.isAdministrator(headers, authenticatedSender) {
		base.Reason = ReasonNoLockAdminOnly
		return base, nil
	}
	base.Disposition = ApplyControl
	base.Reason = ReasonAdminSender
	return base, nil
}

type parsedControl struct {
	kind   ControlKind
	target string
}

func parseControl(headers Header) (parsedControl, error) {
	controlValues := headers.Values("Control")
	supersedesValues := headers.Values("Supersedes")

	if len(controlValues) > 1 {
		return parsedControl{}, ErrDuplicateControl
	}
	if len(supersedesValues) > 1 {
		return parsedControl{}, ErrDuplicateSupersedes
	}
	if len(controlValues) == 1 && len(supersedesValues) == 1 {
		return parsedControl{}, errors.New("Control and Supersedes both present")
	}

	if len(supersedesValues) == 1 {
		target, err := parseMessageID(supersedesValues[0])
		if err != nil {
			return parsedControl{}, err
		}
		return parsedControl{kind: ControlSupersedes, target: target}, nil
	}
	if len(controlValues) != 1 {
		return parsedControl{}, nil
	}

	fields := strings.Fields(controlValues[0])
	if len(fields) == 0 {
		return parsedControl{}, errors.New("empty Control header")
	}
	command := strings.ToLower(fields[0])
	kind := ControlKind(command)
	if kind != ControlCancel && kind != ControlSupersedes {
		return parsedControl{kind: kind}, nil
	}
	if len(fields) != 2 {
		return parsedControl{}, fmt.Errorf("malformed %s control", command)
	}
	target, err := parseMessageID(fields[1])
	if err != nil {
		return parsedControl{}, err
	}
	return parsedControl{kind: kind, target: target}, nil
}

func parseMessageID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || value[0] != '<' || value[len(value)-1] != '>' {
		return "", errors.New("target is not a Message-ID")
	}
	inner := value[1 : len(value)-1]
	if strings.ContainsAny(inner, "<> \t\r\n") {
		return "", errors.New("invalid Message-ID")
	}
	return value, nil
}

func parseErrorDecision(err error) Decision {
	reason := ReasonMalformedHeader
	switch {
	case errors.Is(err, ErrFoldedControl):
		reason = ReasonFoldedControl
	case errors.Is(err, ErrDuplicateControl):
		reason = ReasonDuplicateControl
	case errors.Is(err, ErrDuplicateSupersedes):
		reason = ReasonDuplicateSupersedes
	}
	return Decision{Disposition: Reject, Reason: reason}
}

func (a *Authorizer) pathContainsLocalSite(headers Header) bool {
	for _, path := range headers.Values("Path") {
		for _, component := range strings.Split(path, "!") {
			if normalizeSiteIdentity(component) == a.siteID {
				return true
			}
		}
	}
	return false
}

func normalizeSiteIdentity(identity string) string {
	identity = strings.ToLower(trimWSP(identity))
	if len(identity) >= 2 && identity[0] == '{' && identity[len(identity)-1] == '}' {
		identity = identity[1 : len(identity)-1]
	}
	return strings.TrimSuffix(identity, ".")
}

func (a *Authorizer) isAdministrator(headers Header, authenticatedSender string) bool {
	if authenticatedSender != "" {
		address, err := mail.ParseAddress(strings.TrimSpace(authenticatedSender))
		if err != nil {
			return false
		}
		_, ok := a.admins[strings.ToLower(address.Address)]
		return ok
	}

	// A Sender field, when present, identifies the agent that submitted the
	// article and takes precedence over the author's From field.
	if senderValues := headers.Values("Sender"); len(senderValues) > 0 {
		if len(senderValues) != 1 {
			return false
		}
		address, err := mail.ParseAddress(senderValues[0])
		if err != nil {
			return false
		}
		_, ok := a.admins[strings.ToLower(address.Address)]
		return ok
	}

	fromValues := headers.Values("From")
	if len(fromValues) != 1 {
		return false
	}
	addresses, err := mail.ParseAddressList(fromValues[0])
	if err != nil || len(addresses) != 1 {
		return false
	}
	_, ok := a.admins[strings.ToLower(addresses[0].Address)]
	return ok
}

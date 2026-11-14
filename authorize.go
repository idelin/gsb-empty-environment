package netsec

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	errMissingSlicePolicy       = errors.New("slice policy has no S-NSSAI")
	errConflictingSlicePolicy   = errors.New("slice policy has conflicting S-NSSAIs")
	errConflictingQoSGeneration = errors.New("slice policy has conflicting QoS generations")
)

// Rejection describes one denied S-NSSAI and its NAS cause mapping.
type Rejection struct {
	SNSSAI SNSSAI
	Slice  SNSSAI
	Cause  CauseCode
	Reason RejectionReason
	Detail string
}

// Decision is the result of a slice authorization request. A partial decision
// contains the authorized intersection in Allowed and one Rejection per denied
// candidate.
type Decision struct {
	Requested []SNSSAI
	Allowed   []SNSSAI
	Rejected  []Rejection
	Cause     CauseCode

	// Reauthorized is true when AMF reselection, session continuity, or an old
	// SMF context was present. It indicates that inherited slices were treated
	// as untrusted candidates rather than copied into Allowed.
	Reauthorized bool
}

// AuthorizationResult and SliceDecision are aliases for Decision.
type AuthorizationResult = Decision
type SliceDecision = Decision
type HandoverDecision = Decision
type PDUSessionDecision = Decision

// Complete reports whether every requested slice was authorized.
func (d Decision) Complete() bool {
	return len(d.Requested) > 0 && len(d.Rejected) == 0
}

// AnyAllowed reports whether at least one slice was authorized.
func (d Decision) AnyAllowed() bool {
	return len(d.Allowed) > 0
}

// OK reports whether at least one slice was authorized and no candidate was
// rejected.
func (d Decision) OK() bool {
	return d.AnyAllowed() && d.Complete()
}

// Err returns an AuthorizationError if any candidate was rejected.
func (d Decision) Err() error {
	if len(d.Rejected) == 0 {
		if len(d.Requested) == 0 {
			return &AuthorizationError{Cause: d.Cause}
		}
		return nil
	}
	return &AuthorizationError{Cause: d.Cause, Rejections: d.Rejected}
}

// AuthorizationError carries the NAS cause and all slice-level rejections.
type AuthorizationError struct {
	Cause      CauseCode
	Rejections []Rejection
}

func (e *AuthorizationError) Error() string {
	if len(e.Rejections) == 0 {
		return e.Cause.String()
	}
	return fmt.Sprintf("%s (%s)", e.Cause, e.Rejections[0].Reason)
}

// SliceRequest authorizes an arbitrary set of S-NSSAIs.
type SliceRequest struct {
	Subscription   Subscription
	Registration   Registration
	Slices         []SNSSAI
	ServingNetwork ServingNetwork
	DNN            string
	QoSGeneration  int
}

// RegistrationRequest authorizes the S-NSSAIs carried by a registration.
type RegistrationRequest struct {
	Subscription   Subscription
	Registration   Registration
	ServingNetwork ServingNetwork
}

// PDUSessionRequest authorizes the S-NSSAIs carried by a PDU session.
type PDUSessionRequest struct {
	Subscription   Subscription
	Registration   Registration
	PDUSession     PDUSession
	ServingNetwork ServingNetwork
}

// HandoverRequest describes a handover to a target access network.
//
// TargetAllowedSlices is the target access network's allowed S-NSSAI set.
// RequestedSlices and PDUSession.Slices are candidates. OldAllowedSlices and
// OldSMFContext are never authoritative: they are only fallback candidates
// when no current request/session slice set exists, and every candidate is
// rechecked against the subscription and target network.
type HandoverRequest struct {
	Subscription   Subscription
	Registration   Registration
	PDUSession     PDUSession
	ServingNetwork ServingNetwork

	TargetAllowedSlices []SNSSAI
	RequestedSlices     []SNSSAI
	OldAllowedSlices    []SNSSAI
	OldSMFContext       *SMFContext

	AMFReselected     bool
	SessionContinuity bool

	// DNN and QoSGeneration are optional handover-level overrides. If both a
	// PDU session value and an override are set, they must agree.
	DNN           string
	QoSGeneration int
}

// Handover is an alias for HandoverRequest.
type Handover = HandoverRequest

// Authorizer holds a default subscription. A request-level subscription takes
// precedence, which allows one Authorizer to be shared while still accepting
// explicitly supplied subscription data.
type Authorizer struct {
	Subscription Subscription
}

// SliceAuthorizer is an alias for Authorizer.
type SliceAuthorizer = Authorizer

// NewAuthorizer, NewSliceAuthorizer, and New are equivalent constructors.
func NewAuthorizer(subscription Subscription) *Authorizer {
	return &Authorizer{Subscription: subscription}
}

func NewSliceAuthorizer(subscription Subscription) *Authorizer {
	return NewAuthorizer(subscription)
}

func New(subscription Subscription) *Authorizer {
	return NewAuthorizer(subscription)
}

// AuthorizeSlices authorizes an arbitrary set of slices against the
// subscription, registration, roaming, DNN, and QoS bindings.
func AuthorizeSlices(request SliceRequest) Decision {
	return authorizeSlices(request)
}

// AuthorizeRegistration authorizes the registration's allowed S-NSSAI set.
func AuthorizeRegistration(request RegistrationRequest) Decision {
	return authorizeRegistration(request)
}

// AuthorizePDUSession authorizes a PDU session's S-NSSAI set.
func AuthorizePDUSession(request PDUSessionRequest) Decision {
	return authorizePDUSession(request)
}

// AuthorizeHandover re-authorizes every handover candidate. No old allowed
// slice or old SMF context slice is copied without an exact subscription and
// target access-network match.
func AuthorizeHandover(request HandoverRequest) Decision {
	return authorizeHandover(request)
}

// EvaluateRegistration, EvaluatePDUSession, and EvaluateHandover are aliases
// for callers that prefer an evaluation-style API.
func EvaluateRegistration(request RegistrationRequest) Decision {
	return authorizeRegistration(request)
}

func EvaluatePDUSession(request PDUSessionRequest) Decision {
	return authorizePDUSession(request)
}

func EvaluateHandover(request HandoverRequest) Decision {
	return authorizeHandover(request)
}

// Authorize dispatches a typed authorization request. It is useful for
// adapters that already carry a request variant.
func Authorize(request any) Decision {
	switch typed := request.(type) {
	case SliceRequest:
		return authorizeSlices(typed)
	case *SliceRequest:
		if typed == nil {
			break
		}
		return authorizeSlices(*typed)
	case RegistrationRequest:
		return authorizeRegistration(typed)
	case *RegistrationRequest:
		if typed == nil {
			break
		}
		return authorizeRegistration(*typed)
	case PDUSessionRequest:
		return authorizePDUSession(typed)
	case *PDUSessionRequest:
		if typed == nil {
			break
		}
		return authorizePDUSession(*typed)
	case HandoverRequest:
		return authorizeHandover(typed)
	case *HandoverRequest:
		if typed == nil {
			break
		}
		return authorizeHandover(*typed)
	}
	return Decision{Cause: CauseRequestRejectedUnspecified}
}

// Check is the error-returning form of Authorize.
func Check(request any) error {
	return Authorize(request).Err()
}

func (a *Authorizer) AuthorizeSlices(request SliceRequest) Decision {
	request.Subscription = a.effectiveSubscription(request.Subscription)
	return authorizeSlices(request)
}

func (a *Authorizer) AuthorizeRegistration(request RegistrationRequest) Decision {
	request.Subscription = a.effectiveSubscription(request.Subscription)
	return authorizeRegistration(request)
}

func (a *Authorizer) AuthorizePDUSession(request PDUSessionRequest) Decision {
	request.Subscription = a.effectiveSubscription(request.Subscription)
	return authorizePDUSession(request)
}

func (a *Authorizer) AuthorizeHandover(request HandoverRequest) Decision {
	request.Subscription = a.effectiveSubscription(request.Subscription)
	return authorizeHandover(request)
}

// Registration, PDUSession, Handover, and Slices are short method aliases.
func (a *Authorizer) Registration(request RegistrationRequest) Decision {
	return a.AuthorizeRegistration(request)
}

func (a *Authorizer) PDUSession(request PDUSessionRequest) Decision {
	return a.AuthorizePDUSession(request)
}

func (a *Authorizer) Handover(request HandoverRequest) Decision {
	return a.AuthorizeHandover(request)
}

func (a *Authorizer) Slices(request SliceRequest) Decision {
	return a.AuthorizeSlices(request)
}

// Authorize dispatches a request against the authorizer's default
// subscription.
func (a *Authorizer) Authorize(request any) Decision {
	switch typed := request.(type) {
	case SliceRequest:
		return a.AuthorizeSlices(typed)
	case *SliceRequest:
		if typed != nil {
			return a.AuthorizeSlices(*typed)
		}
	case RegistrationRequest:
		return a.AuthorizeRegistration(typed)
	case *RegistrationRequest:
		if typed != nil {
			return a.AuthorizeRegistration(*typed)
		}
	case PDUSessionRequest:
		return a.AuthorizePDUSession(typed)
	case *PDUSessionRequest:
		if typed != nil {
			return a.AuthorizePDUSession(*typed)
		}
	case HandoverRequest:
		return a.AuthorizeHandover(typed)
	case *HandoverRequest:
		if typed != nil {
			return a.AuthorizeHandover(*typed)
		}
	}
	return Decision{Cause: CauseRequestRejectedUnspecified}
}

// Check is the error-returning form of Authorize.
func (a *Authorizer) Check(request any) error {
	return a.Authorize(request).Err()
}

func (a *Authorizer) effectiveSubscription(request Subscription) Subscription {
	if len(request.Slices) > 0 || len(request.AllowedSlices) > 0 || a == nil {
		return request
	}
	return a.Subscription
}

// CheckSlice checks one S-NSSAI against a subscription.
func CheckSlice(subscription Subscription, slice SNSSAI) error {
	decision := authorizeSlices(SliceRequest{
		Subscription: subscription,
		Slices:       []SNSSAI{slice},
	})
	return decision.Err()
}

// CheckSlice checks one S-NSSAI against the authorizer's default subscription.
func (a *Authorizer) CheckSlice(slice SNSSAI) error {
	return a.AuthorizeSlices(SliceRequest{Slices: []SNSSAI{slice}}).Err()
}

// CheckHandover is a convenience wrapper returning an error for any rejection.
func CheckHandover(request HandoverRequest) error {
	return authorizeHandover(request).Err()
}

// CheckPDUSession is a convenience wrapper returning an error for any
// rejection.
func CheckPDUSession(request PDUSessionRequest) error {
	return authorizePDUSession(request).Err()
}

// CheckRegistration is a convenience wrapper returning an error for any
// rejection.
func CheckRegistration(request RegistrationRequest) error {
	return authorizeRegistration(request).Err()
}

type policyTable struct {
	bySlice   map[SNSSAI]SlicePolicy
	conflicts map[SNSSAI]struct{}
	sameSST   map[uint8]struct{}
}

func compileSubscription(subscription Subscription) policyTable {
	table := policyTable{
		bySlice:   make(map[SNSSAI]SlicePolicy),
		conflicts: make(map[SNSSAI]struct{}),
		sameSST:   make(map[uint8]struct{}),
	}

	policies := make([]SlicePolicy, 0, len(subscription.Slices)+len(subscription.AllowedSlices))
	policies = append(policies, subscription.Slices...)
	policies = append(policies, subscription.AllowedSlices...)

	for _, policy := range policies {
		identity, err := policy.identity()
		if err != nil {
			continue
		}

		policy.SNSSAI = identity
		policy.Slice = SNSSAI{}
		policy.DNN = strings.TrimSpace(policy.DNN)
		generation, err := policy.qosGeneration()
		if err != nil {
			continue
		}
		policy.QoSGeneration = generation
		policy.QoSVersion = generation
		policy.PolicyGeneration = generation
		table.sameSST[identity.SST] = struct{}{}

		existing, exists := table.bySlice[identity]
		if !exists {
			table.bySlice[identity] = policy
			continue
		}
		if !policiesEqual(existing, policy) {
			table.conflicts[identity] = struct{}{}
		}
	}
	return table
}

func policiesEqual(left, right SlicePolicy) bool {
	if left.SNSSAI != right.SNSSAI ||
		left.RoamingAllowed != right.RoamingAllowed ||
		left.Roaming != right.Roaming ||
		left.DNN != right.DNN ||
		left.QoSGeneration != right.QoSGeneration ||
		left.QoSVersion != right.QoSVersion ||
		left.PolicyGeneration != right.PolicyGeneration {
		return false
	}
	return slices.Equal(left.AllowedPLMNs, right.AllowedPLMNs)
}

func normalizeCandidates(values []SNSSAI) ([]SNSSAI, []Rejection) {
	seen := make(map[SNSSAI]struct{}, len(values))
	normalized := make([]SNSSAI, 0, len(values))
	rejections := make([]Rejection, 0)

	for _, value := range values {
		slice, err := value.Normalize()
		if err != nil {
			rejections = append(rejections, newRejection(
				value,
				CauseRequestRejectedUnspecified,
				ReasonInvalidSNSSAI,
				err.Error(),
			))
			continue
		}
		if _, exists := seen[slice]; exists {
			continue
		}
		seen[slice] = struct{}{}
		normalized = append(normalized, slice)
	}
	return normalized, rejections
}

func normalizeSet(values []SNSSAI) (map[SNSSAI]struct{}, bool) {
	set := make(map[SNSSAI]struct{}, len(values))
	for _, value := range values {
		slice, err := value.Normalize()
		if err != nil {
			continue
		}
		set[slice] = struct{}{}
	}
	return set, len(set) > 0
}

func authorizeSlices(request SliceRequest) Decision {
	policies := compileSubscription(request.Subscription)
	registrationValues := registrationSlices(request.Registration)
	registration, _ := normalizeSet(registrationValues)

	var registrationFilter map[SNSSAI]struct{}
	if len(registrationValues) > 0 {
		registrationFilter = registration
	}

	return authorizeCandidates(
		policies,
		registrationFilter,
		nil,
		request.Slices,
		request.DNN,
		request.QoSGeneration,
		request.ServingNetwork,
		false,
		request.DNN != "" || request.QoSGeneration != 0,
	)
}

func authorizeRegistration(request RegistrationRequest) Decision {
	policies := compileSubscription(request.Subscription)
	slices := registrationSlices(request.Registration)
	decision := authorizeCandidates(
		policies,
		nil,
		nil,
		slices,
		"",
		0,
		request.ServingNetwork,
		false,
		false,
	)
	if len(decision.Requested) == 0 {
		decision.Cause = CauseNoNetworkSlicesAvailable
	}
	return decision
}

func authorizePDUSession(request PDUSessionRequest) Decision {
	policies := compileSubscription(request.Subscription)
	registrationValues := registrationSlices(request.Registration)
	registration, _ := normalizeSet(registrationValues)

	var registrationFilter map[SNSSAI]struct{}
	if len(registrationValues) > 0 {
		registrationFilter = registration
	}

	return authorizeCandidates(
		policies,
		registrationFilter,
		nil,
		request.PDUSession.Slices,
		request.PDUSession.DNN,
		request.PDUSession.QoSGeneration,
		request.ServingNetwork,
		false,
		true,
	)
}

func authorizeHandover(request HandoverRequest) Decision {
	policies := compileSubscription(request.Subscription)

	registrationValues := registrationSlices(request.Registration)
	registration, _ := normalizeSet(registrationValues)
	var registrationFilter map[SNSSAI]struct{}
	if len(registrationValues) > 0 {
		registrationFilter = registration
	}

	target, _ := normalizeSet(request.TargetAllowedSlices)

	candidates := append([]SNSSAI(nil), request.PDUSession.Slices...)
	candidates = append(candidates, request.RequestedSlices...)

	inheritedOnly := false
	if len(candidates) == 0 {
		candidates = append(candidates, request.OldAllowedSlices...)
		if request.OldSMFContext != nil {
			candidates = append(candidates, request.OldSMFContext.AllowedSlices...)
			candidates = append(candidates, request.OldSMFContext.Slices...)
		}
		inheritedOnly = len(candidates) > 0
	}

	dnn := request.PDUSession.DNN
	if request.DNN != "" {
		if dnn != "" && dnn != request.DNN {
			return Decision{
				Cause:        CauseMissingOrUnknownDNN,
				Reauthorized: request.AMFReselected || request.SessionContinuity || request.OldSMFContext != nil,
			}
		}
		dnn = request.DNN
	}

	qosGeneration := request.PDUSession.QoSGeneration
	if request.QoSGeneration != 0 {
		if qosGeneration != 0 && qosGeneration != request.QoSGeneration {
			return Decision{
				Cause:        CauseRequestRejectedUnspecified,
				Reauthorized: request.AMFReselected || request.SessionContinuity || request.OldSMFContext != nil,
			}
		}
		qosGeneration = request.QoSGeneration
	}

	decision := authorizeCandidates(
		policies,
		registrationFilter,
		&target,
		candidates,
		dnn,
		qosGeneration,
		request.ServingNetwork,
		true,
		true,
	)
	decision.Reauthorized = request.AMFReselected ||
		request.SessionContinuity ||
		request.OldSMFContext != nil ||
		len(request.OldAllowedSlices) > 0

	if inheritedOnly && len(decision.Allowed) == 0 && len(decision.Rejected) == 0 {
		decision.Cause = CauseRequestRejectedUnspecified
	}
	return decision
}

func authorizeCandidates(
	policies policyTable,
	registration map[SNSSAI]struct{},
	target *map[SNSSAI]struct{},
	values []SNSSAI,
	dnn string,
	qosGeneration int,
	servingNetwork ServingNetwork,
	requireTarget bool,
	enforceBindings bool,
) Decision {
	candidates, rejections := normalizeCandidates(values)
	decision := Decision{
		Requested: candidates,
		Allowed:   make([]SNSSAI, 0, len(candidates)),
		Rejected:  rejections,
	}

	home := servingNetwork.home()
	serving := servingNetwork.serving()

	for _, slice := range candidates {
		if _, conflicted := policies.conflicts[slice]; conflicted {
			decision.Rejected = append(decision.Rejected, newRejection(
				slice,
				CauseRequestRejectedUnspecified,
				ReasonSubscriptionPolicyConflict,
				"subscription contains conflicting policies for the S-NSSAI",
			))
			continue
		}

		policy, subscribed := policies.bySlice[slice]
		if !subscribed {
			reason := ReasonSliceNotSubscribed
			detail := "S-NSSAI is not present in the subscription"
			if _, sameSST := policies.sameSST[slice.SST]; sameSST {
				reason = ReasonSDMismatch
				detail = "subscription contains the SST with a different SD"
			}
			decision.Rejected = append(decision.Rejected, newRejection(
				slice,
				CauseRequestedServiceOptionNotSubscribed,
				reason,
				detail,
			))
			continue
		}

		if !policy.allowsRoaming(home, serving) {
			decision.Rejected = append(decision.Rejected, newRejection(
				slice,
				CauseRoamingNotAllowedInTrackingArea,
				ReasonRoamingRestricted,
				"subscription does not permit this S-NSSAI in the serving PLMN",
			))
			continue
		}

		if registration != nil {
			if _, allowed := registration[slice]; !allowed {
				decision.Rejected = append(decision.Rejected, newRejection(
					slice,
					CauseServiceOptionNotSupported,
					ReasonRegistrationNotAllowed,
					"S-NSSAI is not in the registration allowed set",
				))
				continue
			}
		}

		if target != nil {
			if _, allowed := (*target)[slice]; !allowed {
				decision.Rejected = append(decision.Rejected, newRejection(
					slice,
					CauseServiceOptionNotSupported,
					ReasonTargetAccessNetworkNotAllowed,
					"S-NSSAI is not allowed by the target access network",
				))
				continue
			}
		} else if requireTarget {
			// A handover without a target allowed set fails closed. This branch
			// is reached only if a caller bypassed the normal target pointer.
			decision.Rejected = append(decision.Rejected, newRejection(
				slice,
				CauseServiceOptionNotSupported,
				ReasonTargetAccessNetworkNotAllowed,
				"target access-network allowed set is empty",
			))
			continue
		}

		if enforceBindings && !policyAllowsDNN(policy, dnn) {
			decision.Rejected = append(decision.Rejected, newRejection(
				slice,
				CauseMissingOrUnknownDNN,
				ReasonDNNMismatch,
				"DNN does not match the subscription binding",
			))
			continue
		}

		if enforceBindings && !policyAllowsQoSGeneration(policy, qosGeneration) {
			decision.Rejected = append(decision.Rejected, newRejection(
				slice,
				CauseRequestRejectedUnspecified,
				ReasonQoSGenerationMismatch,
				"QoS policy generation does not match the subscription binding",
			))
			continue
		}

		decision.Allowed = append(decision.Allowed, slice)
	}

	if len(decision.Rejected) > 0 {
		decision.Cause = decision.Rejected[0].Cause
	} else if len(decision.Allowed) == 0 {
		decision.Cause = CauseNoNetworkSlicesAvailable
	}
	return decision
}

func policyAllowsDNN(policy SlicePolicy, requested string) bool {
	if policy.DNN == "" {
		return true
	}
	return strings.TrimSpace(requested) == policy.DNN
}

func policyAllowsQoSGeneration(policy SlicePolicy, requested int) bool {
	if policy.QoSGeneration == 0 {
		return true
	}
	return requested == policy.QoSGeneration
}

func registrationSlices(registration Registration) []SNSSAI {
	values := make([]SNSSAI, 0, len(registration.AllowedSlices)+len(registration.Slices))
	values = append(values, registration.AllowedSlices...)
	values = append(values, registration.Slices...)
	return values
}

func newRejection(slice SNSSAI, cause CauseCode, reason RejectionReason, detail string) Rejection {
	normalized, err := slice.Normalize()
	if err == nil {
		slice = normalized
	}
	return Rejection{
		SNSSAI: slice,
		Slice:  slice,
		Cause:  cause,
		Reason: reason,
		Detail: detail,
	}
}

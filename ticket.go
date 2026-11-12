package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// ServiceTicketLifetime is the maximum age of a service ticket. A ticket
	// is expired when the validation time is greater than or equal to
	// issuedAt+10s.
	ServiceTicketLifetime = 10 * time.Second

	// ServiceTicketPrefix is the prefix used for service-ticket identifiers.
	ServiceTicketPrefix = "ST-"

	// ProxyTicketPrefix is the prefix used for proxy-ticket identifiers.
	ProxyTicketPrefix = "PT-"

	// DefaultPrincipal is used by the convenience issue form that omits an
	// explicit principal. It keeps the low-level store usable by middleware
	// that only needs to prove ticket semantics.
	DefaultPrincipal = "user"

	// TicketLifetime is an alias for ServiceTicketLifetime.
	TicketLifetime = ServiceTicketLifetime
)

// TicketType distinguishes service tickets from proxy tickets.
type TicketType string

const (
	TicketTypeService TicketType = "ST"
	TicketTypeProxy   TicketType = "PT"

	// Common alternative names.
	ServiceTicketType = TicketTypeService
	ProxyTicketType   = TicketTypeProxy
)

// IssueOptions describes a ticket to issue. The zero value is valid except
// that Service must be set.
type IssueOptions struct {
	Service string

	// Principal and User are aliases; Principal is preferred.
	Principal string
	User      string

	// Renew and FromCredentials are aliases. When either is true, the ticket
	// records that it came from a fresh credential authentication and may be
	// validated with renew=true.
	Renew           bool
	FromCredentials bool

	// IssuedAt overrides the store clock. It is useful for replay and expiry
	// tests and for importing tickets issued by another component.
	IssuedAt time.Time

	// Type defaults to TicketTypeService.
	Type TicketType
}

// ValidationRequest is the input used by a serviceValidate handler.
type ValidationRequest struct {
	Ticket  string
	Service string
	Renew   bool
}

// ValidationResult is returned by detailed validation. Successful validation
// consumes the ticket.
type ValidationResult struct {
	User      string
	Username  string
	Principal string
	Service   string
	Renew     bool
	Type      TicketType
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type ticketRecord struct {
	ticketType      TicketType
	service         string
	principal       string
	fromCredentials bool
	issuedAt        time.Time
	expiresAt       time.Time
	used            bool
	expired         bool
}

// Option configures a TicketStore.
type Option func(*TicketStore)

// WithClock replaces the store's time source. The function is called for each
// issuance and validation.
func WithClock(now func() time.Time) Option {
	return func(s *TicketStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithTimeFunc is an alias for WithClock.
func WithTimeFunc(now func() time.Time) Option {
	return WithClock(now)
}

// WithRandReader replaces the cryptographic random source used to generate
// unguessable ticket identifiers.
func WithRandReader(r io.Reader) Option {
	return func(s *TicketStore) {
		if r != nil {
			s.rand = r
		}
	}
}

// TicketStore is an in-memory CAS ticket repository. All state transitions are
// protected by a mutex; successful validation, lookup, checks, and the
// single-use transition happen atomically.
type TicketStore struct {
	mu      sync.Mutex
	tickets map[string]*ticketRecord
	now     func() time.Time
	rand    io.Reader
}

// NewTicketStore creates an empty ticket store.
func NewTicketStore(options ...Option) *TicketStore {
	s := &TicketStore{
		tickets: make(map[string]*ticketRecord),
		now:     time.Now,
		rand:    rand.Reader,
	}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	return s
}

// NewServiceTicketStore is a compatibility alias for NewTicketStore.
func NewServiceTicketStore(options ...Option) *TicketStore {
	return NewTicketStore(options...)
}

// NewTicketStoreWithClock is a convenience constructor for a deterministic
// store.
func NewTicketStoreWithClock(now func() time.Time) *TicketStore {
	return NewTicketStore(WithClock(now))
}

// Validator is a compatibility alias for TicketStore.
type Validator = TicketStore

// TicketValidator is a compatibility alias for TicketStore.
type TicketValidator = TicketStore

// ServiceTicketStore is a compatibility alias for TicketStore.
type ServiceTicketStore = TicketStore

// NewValidator creates an empty validator.
func NewValidator(options ...Option) *Validator {
	return NewTicketStore(options...)
}

// NewTicketValidator creates an empty validator.
func NewTicketValidator(options ...Option) *TicketValidator {
	return NewTicketStore(options...)
}

// IssueServiceTicket issues a service ticket.
//
// The usual form is:
//
//	store.IssueServiceTicket(principal, service, fromCredentials)
//
// Because service URLs are unambiguous, either string order is accepted:
//
//	store.IssueServiceTicket(service, principal, fromCredentials)
//
// A bool argument records whether the ticket came from a fresh credential
// authentication. A time.Time argument overrides the issuance time. An
// IssueOptions value is also accepted. If no principal is supplied,
// DefaultPrincipal is used.
func (s *TicketStore) IssueServiceTicket(args ...any) (string, error) {
	return s.issue(TicketTypeService, args...)
}

// Issue is an alias for IssueServiceTicket.
func (s *TicketStore) Issue(args ...any) (string, error) {
	return s.IssueServiceTicket(args...)
}

// IssueTicket is an alias for IssueServiceTicket.
func (s *TicketStore) IssueTicket(args ...any) (string, error) {
	return s.IssueServiceTicket(args...)
}

// IssueServiceTicketWithOptions issues a service ticket from a struct.
func (s *TicketStore) IssueServiceTicketWithOptions(options IssueOptions) (string, error) {
	return s.issue(TicketTypeService, options)
}

// IssueServiceTicketAt issues a service ticket with an explicit issuance time.
func (s *TicketStore) IssueServiceTicketAt(issuedAt time.Time, args ...any) (string, error) {
	return s.issue(TicketTypeService, append([]any{issuedAt}, args...)...)
}

// IssueProxyTicket issues a proxy ticket. Proxy tickets are never accepted by
// ValidateServiceTicket.
func (s *TicketStore) IssueProxyTicket(args ...any) (string, error) {
	return s.issue(TicketTypeProxy, args...)
}

// IssueProxyTicketWithOptions issues a proxy ticket from a struct.
func (s *TicketStore) IssueProxyTicketWithOptions(options IssueOptions) (string, error) {
	return s.issue(TicketTypeProxy, options)
}

func (s *TicketStore) issue(ticketType TicketType, args ...any) (string, error) {
	spec, err := parseIssueArgs(ticketType, args)
	if err != nil {
		return "", err
	}
	service, err := NormalizeServiceURL(spec.Service)
	if err != nil {
		return "", err
	}
	if spec.Principal == "" {
		return "", ErrInvalidPrincipal
	}

	issuedAt := spec.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = s.now()
	}
	record := &ticketRecord{
		ticketType:      ticketType,
		service:         service,
		principal:       spec.Principal,
		fromCredentials: spec.FromCredentials,
		issuedAt:        issuedAt,
		expiresAt:       issuedAt.Add(ServiceTicketLifetime),
	}

	prefix := ServiceTicketPrefix
	if ticketType == TicketTypeProxy {
		prefix = ProxyTicketPrefix
	}

	for attempt := 0; attempt < 8; attempt++ {
		ticket, err := s.newTicketID(prefix)
		if err != nil {
			return "", err
		}

		s.mu.Lock()
		if _, exists := s.tickets[ticket]; !exists {
			s.tickets[ticket] = record
			s.mu.Unlock()
			return ticket, nil
		}
		s.mu.Unlock()
	}
	return "", fmt.Errorf("netsec: could not allocate a unique ticket identifier")
}

func (s *TicketStore) newTicketID(prefix string) (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(s.rand, buf); err != nil {
		return "", fmt.Errorf("netsec: generate ticket: %w", err)
	}
	return prefix + hex.EncodeToString(buf), nil
}

// ValidateServiceTicket validates and atomically consumes a service ticket.
//
// It accepts (ticket, service) plus an optional renew bool, either argument
// order for the two strings, or a ValidationRequest. It returns the
// authenticated principal on success. A failed validation does not consume the
// ticket; a successful one marks it used before the lock is released.
func (s *TicketStore) ValidateServiceTicket(args ...any) (string, error) {
	request, err := parseValidationArgs(args)
	if err != nil {
		return "", err
	}
	result, err := s.validate(TicketTypeService, request)
	if err != nil {
		return "", err
	}
	return result.Principal, nil
}

// Validate is an alias for ValidateServiceTicket.
func (s *TicketStore) Validate(args ...any) (string, error) {
	return s.ValidateServiceTicket(args...)
}

// ConsumeServiceTicket is an alias for ValidateServiceTicket.
func (s *TicketStore) ConsumeServiceTicket(args ...any) (string, error) {
	return s.ValidateServiceTicket(args...)
}

// ValidateServiceTicketRequest validates a request received by the CAS
// serviceValidate endpoint.
func (s *TicketStore) ValidateServiceTicketRequest(request ValidationRequest) (string, error) {
	return s.ValidateServiceTicket(request)
}

// ValidateServiceTicketDetailed behaves like ValidateServiceTicket and also
// returns the ticket's bound metadata.
func (s *TicketStore) ValidateServiceTicketDetailed(args ...any) (*ValidationResult, error) {
	request, err := parseValidationArgs(args)
	if err != nil {
		return nil, err
	}
	return s.validate(TicketTypeService, request)
}

// ValidateTicket is an alias for ValidateServiceTicketDetailed.
func (s *TicketStore) ValidateTicket(args ...any) (*ValidationResult, error) {
	return s.ValidateServiceTicketDetailed(args...)
}

// ValidateProxyTicket validates a proxy ticket. A service ticket is rejected.
func (s *TicketStore) ValidateProxyTicket(args ...any) (string, error) {
	request, err := parseValidationArgs(args)
	if err != nil {
		return "", err
	}
	result, err := s.validate(TicketTypeProxy, request)
	if err != nil {
		return "", err
	}
	return result.Principal, nil
}

func (s *TicketStore) validate(expected TicketType, request ValidationRequest) (*ValidationResult, error) {
	if request.Ticket == "" {
		return nil, ErrInvalidTicket
	}
	service, err := NormalizeServiceURL(request.Service)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.tickets[request.Ticket]
	if !ok {
		return nil, ErrTicketNotFound
	}
	if record.ticketType != expected {
		if record.ticketType == TicketTypeProxy {
			return nil, ErrProxyTicket
		}
		return nil, ErrServiceTicket
	}
	if record.used {
		return nil, ErrTicketUsed
	}
	if record.expired {
		return nil, ErrTicketExpired
	}
	if !s.now().Before(record.expiresAt) {
		// Remember that expiry was observed so a clock adjustment cannot
		// resurrect the ticket later.
		record.expired = true
		return nil, ErrTicketExpired
	}
	if service != record.service {
		return nil, ErrServiceMismatch
	}
	if request.Renew && !record.fromCredentials {
		return nil, ErrRenewRequired
	}

	// The record is still unused and every check passed. Flipping this bit
	// while holding the store lock is the single-use commit point.
	record.used = true

	return &ValidationResult{
		User:      record.principal,
		Username:  record.principal,
		Principal: record.principal,
		Service:   record.service,
		Renew:     record.fromCredentials,
		Type:      record.ticketType,
		IssuedAt:  record.issuedAt,
		ExpiresAt: record.expiresAt,
	}, nil
}

type issueSpec struct {
	Service         string
	Principal       string
	FromCredentials bool
	IssuedAt        time.Time
}

func parseIssueArgs(ticketType TicketType, args []any) (issueSpec, error) {
	spec := issueSpec{}
	var stringsFound []string

	for _, arg := range args {
		switch value := arg.(type) {
		case IssueOptions:
			mergeIssueOptions(&spec, value)
		case *IssueOptions:
			if value != nil {
				mergeIssueOptions(&spec, *value)
			}
		case string:
			stringsFound = append(stringsFound, value)
		case bool:
			spec.FromCredentials = value
		case time.Time:
			spec.IssuedAt = value
		case *time.Time:
			if value != nil {
				spec.IssuedAt = *value
			}
		case TicketType:
			// Accepted for callers that pass an explicit kind. IssueProxyTicket
			// fixes the kind and a mismatch is rejected.
			if value != ticketType {
				return issueSpec{}, ErrInvalidTicket
			}
		}
	}

	if len(stringsFound) == 0 {
		if spec.Service == "" {
			return issueSpec{}, ErrInvalidService
		}
	} else {
		service, principal := classifyIssueStrings(stringsFound)
		if spec.Service == "" {
			spec.Service = service
		} else if principal == "" && len(stringsFound) == 1 {
			principal = stringsFound[0]
		}
		if spec.Principal == "" {
			spec.Principal = principal
		}
	}

	if spec.Service == "" {
		return issueSpec{}, ErrInvalidService
	}
	if spec.Principal == "" {
		spec.Principal = DefaultPrincipal
	}
	return spec, nil
}

func mergeIssueOptions(spec *issueSpec, options IssueOptions) {
	if options.Service != "" {
		spec.Service = options.Service
	}
	if options.Principal != "" {
		spec.Principal = options.Principal
	} else if options.User != "" {
		spec.Principal = options.User
	}
	if options.Renew || options.FromCredentials {
		spec.FromCredentials = true
	}
	if !options.IssuedAt.IsZero() {
		spec.IssuedAt = options.IssuedAt
	}
}

// classifyIssueStrings treats a URL as the service and the remaining string as
// the principal. The first URL wins if multiple URL-looking strings are
// supplied. With no URL, the first string is the service and the second is the
// principal.
func classifyIssueStrings(values []string) (service, principal string) {
	var serviceIndex = -1
	for i, value := range values {
		if _, err := NormalizeServiceURL(value); err == nil {
			serviceIndex = i
			break
		}
	}
	if serviceIndex < 0 {
		serviceIndex = 0
	}
	service = values[serviceIndex]
	for i, value := range values {
		if i != serviceIndex {
			principal = value
			break
		}
	}
	return service, principal
}

func parseValidationArgs(args []any) (ValidationRequest, error) {
	request := ValidationRequest{}
	var stringsFound []string

	for _, arg := range args {
		switch value := arg.(type) {
		case ValidationRequest:
			if value.Ticket != "" {
				request.Ticket = value.Ticket
			}
			if value.Service != "" {
				request.Service = value.Service
			}
			if value.Renew {
				request.Renew = true
			}
		case *ValidationRequest:
			if value != nil {
				if value.Ticket != "" {
					request.Ticket = value.Ticket
				}
				if value.Service != "" {
					request.Service = value.Service
				}
				if value.Renew {
					request.Renew = true
				}
			}
		case string:
			stringsFound = append(stringsFound, value)
		case bool:
			request.Renew = value
		}
	}

	if request.Ticket == "" || request.Service == "" {
		ticket, service := classifyValidationStrings(stringsFound)
		if request.Ticket == "" {
			request.Ticket = ticket
		}
		if request.Service == "" {
			request.Service = service
		}
	}
	if request.Ticket == "" {
		return ValidationRequest{}, ErrInvalidTicket
	}
	if request.Service == "" {
		return ValidationRequest{}, ErrInvalidService
	}
	return request, nil
}

func classifyValidationStrings(values []string) (ticket, service string) {
	ticketIndex := -1
	for i, value := range values {
		if strings.HasPrefix(value, ServiceTicketPrefix) || strings.HasPrefix(value, ProxyTicketPrefix) {
			ticketIndex = i
			break
		}
	}
	if ticketIndex < 0 && len(values) > 0 {
		ticketIndex = 0
	}
	if ticketIndex >= 0 {
		ticket = values[ticketIndex]
	}
	for i, value := range values {
		if i != ticketIndex {
			service = value
			break
		}
	}
	return ticket, service
}

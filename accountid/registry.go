package accountid

import (
	"errors"
	"fmt"
	"sync"
)

// Registry validation errors.
var (
	ErrInstitutionInvalid = errors.New("accountid: invalid institution record")
	ErrPrefixTaken        = errors.New("accountid: routing prefix already registered")
	ErrInstitutionTaken   = errors.New("accountid: institution code already registered")
)

// Institution is a registry entry binding a routing prefix to an
// institution and its account format policy.
type Institution struct {
	Code          string      // unique institution code, 2-8 uppercase alphanumerics
	Name          string      // human-readable name
	Country       string      // 2-letter country code
	Prefix        string      // 4-character routing prefix
	AccountLength int         // required account number length in digits
	AllowedTypes  []ProxyType // proxy types this institution accepts
}

// Allows reports whether the institution accepts proxy type t.
func (inst Institution) Allows(t ProxyType) bool {
	for _, at := range inst.AllowedTypes {
		if at == t {
			return true
		}
	}
	return false
}

func (inst Institution) validate() error {
	if len(inst.Code) < minInstCode || len(inst.Code) > maxInstCode ||
		!isUpperAlnum(inst.Code, len(inst.Code)) {
		return fmt.Errorf("%w: code %q", ErrInstitutionInvalid, inst.Code)
	}
	if !isUpperLetters(inst.Country, countryLen) {
		return fmt.Errorf("%w: country %q", ErrInstitutionInvalid, inst.Country)
	}
	if !isUpperAlnum(inst.Prefix, prefixLen) {
		return fmt.Errorf("%w: prefix %q", ErrInstitutionInvalid, inst.Prefix)
	}
	if inst.AccountLength < 1 || inst.AccountLength > 34 {
		return fmt.Errorf("%w: account length %d", ErrInstitutionInvalid, inst.AccountLength)
	}
	if len(inst.AllowedTypes) == 0 {
		return fmt.Errorf("%w: no allowed proxy types", ErrInstitutionInvalid)
	}
	for _, t := range inst.AllowedTypes {
		if !t.Valid() {
			return fmt.Errorf("%w: proxy type %q", ErrInstitutionInvalid, string(t))
		}
	}
	return nil
}

// Registry maps routing prefixes to institutions. It is safe for
// concurrent use; updates take effect for subsequent Parse calls.
type Registry struct {
	mu       sync.RWMutex
	byPrefix map[string]Institution
	byCode   map[string]string // institution code -> prefix
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byPrefix: make(map[string]Institution),
		byCode:   make(map[string]string),
	}
}

// Register adds or replaces the entry for inst.Prefix. The institution
// code must not be bound to a different prefix.
func (r *Registry) Register(inst Institution) error {
	if err := inst.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, ok := r.byCode[inst.Code]; ok && prev != inst.Prefix {
		return fmt.Errorf("%w: %q already bound to prefix %q",
			ErrInstitutionTaken, inst.Code, prev)
	}
	if prev, ok := r.byPrefix[inst.Prefix]; ok && prev.Code != inst.Code {
		delete(r.byCode, prev.Code)
	}
	r.byPrefix[inst.Prefix] = inst
	r.byCode[inst.Code] = inst.Prefix
	return nil
}

// Unregister removes the entry for prefix, returning whether one existed.
func (r *Registry) Unregister(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.byPrefix[prefix]
	if !ok {
		return false
	}
	delete(r.byPrefix, prefix)
	delete(r.byCode, inst.Code)
	return true
}

// LookupPrefix returns the institution registered for prefix.
func (r *Registry) LookupPrefix(prefix string) (Institution, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inst, ok := r.byPrefix[prefix]
	return inst, ok
}

// LookupCode returns the institution registered under code.
func (r *Registry) LookupCode(code string) (Institution, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prefix, ok := r.byCode[code]
	if !ok {
		return Institution{}, false
	}
	return r.byPrefix[prefix], true
}

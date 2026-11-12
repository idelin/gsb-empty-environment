package netsec

// errOrigin records where an error variable's current value came from.
type errOrigin struct {
	// authSite is non-empty when the value carries an authentication error,
	// identifying the failing OpAuth site.
	authSite string
	// source is the textual right-hand side from the assignment.
	source string
}

// effectState is the per-path abstract state.
type effectState struct {
	authed        bool
	credential    string
	failed        bool
	failSite      string
	masked        bool
	panicInFlight bool
	panicSite     string
	// errors maps error variable name to the origin of its current value.
	// Absent or cleared variables have no entry.
	errors map[string]errOrigin
	// events and findings accumulate along the path.
	events   []PathEvent
	findings []Finding
}

func newEffectState() *effectState {
	return &effectState{errors: map[string]errOrigin{}}
}

// clone produces an independent copy for branch exploration.
func (s *effectState) clone() *effectState {
	cp := *s
	cp.errors = make(map[string]errOrigin, len(s.errors))
	for k, v := range s.errors {
		cp.errors[k] = v
	}
	cp.events = append([]PathEvent(nil), s.events...)
	cp.findings = append([]Finding(nil), s.findings...)
	return &cp
}

func (s *effectState) snapshot() Snapshot {
	held := map[string]string{}
	for name, origin := range s.errors {
		if origin.authSite != "" {
			held[name] = origin.authSite
		}
	}
	return Snapshot{
		Authed:         s.authed,
		Credential:     s.credential,
		Failed:         s.failed,
		FailSite:       s.failSite,
		Masked:         s.masked,
		PanicInFlight:  s.panicInFlight,
		HeldAuthErrors: held,
	}
}

func (s *effectState) record(kind EventKind, block, site, detail string) {
	s.events = append(s.events, PathEvent{
		Kind:   kind,
		Block:  block,
		Site:   site,
		Detail: detail,
		State:  s.snapshot(),
	})
}

func (s *effectState) addFinding(kind FindingKind, block, site, detail string) {
	s.findings = append(s.findings, Finding{
		Kind:   kind,
		Block:  block,
		Site:   site,
		Detail: detail,
	})
}

// holdsAuthError reports whether name currently carries the sticky
// authentication error.
func (s *effectState) holdsAuthError(name string) (errOrigin, bool) {
	if name == "" {
		return errOrigin{}, false
	}
	origin, ok := s.errors[name]
	return origin, ok && origin.authSite != ""
}

// markMasked records that the authentication error value stopped being
// observable. The sticky failure itself is never cleared here.
func (s *effectState) markMasked(block, site, detail string) {
	s.masked = true
	s.record(EventErrorMasked, block, site, detail)
	s.addFinding(FindingErrorMasked, block, site, detail)
}

// applyAuthSuccess is the transfer for a normal edge leaving an OpAuth block.
// An explicit new authentication success is the only operation allowed to
// clear a failure state.
func (s *effectState) applyAuthSuccess(blockID string, auth OpAuth) {
	s.failed = false
	s.failSite = ""
	s.masked = false
	s.panicInFlight = false
	s.panicSite = ""
	s.authed = true
	s.credential = auth.CredVar
	// The success path overwrites the call's error variable with a nil error.
	if auth.ErrVar != "" {
		delete(s.errors, auth.ErrVar)
	}
	s.record(EventAuthSuccess, blockID, auth.Site, "credential="+auth.CredVar)
}

// applyAuthFailure is the transfer for an exception edge leaving an OpAuth
// block carrying a returned error.
func (s *effectState) applyAuthFailure(blockID string, auth OpAuth) {
	s.failed = true
	s.failSite = auth.Site
	s.masked = false
	s.authed = false
	s.credential = ""
	if auth.ErrVar != "" {
		s.errors[auth.ErrVar] = errOrigin{authSite: auth.Site, source: auth.Site}
	} else {
		// No error variable: the failure is observable only via the panic
		// path, so the value is already unobservable to ordinary checks.
		s.masked = true
	}
	s.record(EventAuthFailure, blockID, auth.Site, "error="+auth.ErrVar)
}

// applyAuthPanic is the transfer for an exception edge leaving an OpAuth
// block whose failure surfaces as a panic rather than a returned error.
func (s *effectState) applyAuthPanic(blockID string, auth OpAuth) {
	s.failed = true
	s.failSite = auth.Site
	s.masked = true
	s.authed = false
	s.credential = ""
	s.panicInFlight = true
	s.panicSite = auth.Site
	s.record(EventAuthFailure, blockID, auth.Site, "panic")
}

// applyErrorAssign executes an OpErrorAssign.
func (s *effectState) applyErrorAssign(blockID string, op OpErrorAssign) {
	if op.Clear {
		if _, ok := s.holdsAuthError(op.Var); ok {
			s.markMasked(blockID, op.Site, "cleared auth error in "+op.Var)
		}
		delete(s.errors, op.Var)
		return
	}

	// A self-assignment (err = err) preserves the origin; any other
	// assignment overwrites the variable.
	if op.Source != op.Var {
		if origin, ok := s.holdsAuthError(op.Var); ok {
			s.markMasked(blockID, op.Site,
				"overwrote auth error from "+origin.authSite+" in "+op.Var)
		}
		s.errors[op.Var] = errOrigin{source: op.Source}
	}
}

// applyPanic executes an OpPanic.
func (s *effectState) applyPanic(blockID string, op OpPanic) {
	s.panicInFlight = true
	s.panicSite = op.Site
}

// applyRecover executes an OpRecover reached on an exception edge.
func (s *effectState) applyRecover(blockID string, op OpRecover) {
	consumedAuthPanic := s.panicInFlight && s.failed && s.panicSite == s.failSite
	s.record(EventPanicRecovered, blockID, op.Site,
		"recovered panic from "+s.panicSite)
	s.panicInFlight = false
	s.panicSite = ""

	// Recovery drops the panic's error unless it is explicitly rebound.
	if consumedAuthPanic {
		if op.ErrVar == "" {
			s.markMasked(blockID, op.Site, "recovered auth panic without binding error")
			return
		}
		stillHeld := false
		for _, origin := range s.errors {
			if origin.authSite != "" {
				stillHeld = true
				break
			}
		}
		s.errors[op.ErrVar] = errOrigin{authSite: s.failSite, source: op.Site}
		if !stillHeld {
			// The panic value was the only carrier of the auth error and
			// recovery rebound it: still observable, no masking.
			s.masked = false
		}
	} else if op.ErrVar != "" {
		s.errors[op.ErrVar] = errOrigin{source: op.Site}
	}
}

// applyProtectedCallback executes an OpProtectedCallback.
func (s *effectState) applyProtectedCallback(blockID string, op OpProtectedCallback) {
	allowed := s.authed && !s.failed
	s.record(EventProtectedCallback, blockID, op.Site,
		"callback="+op.Name+" allowed="+boolString(allowed))
	if !allowed {
		s.addFinding(FindingUnauthorizedCallback, blockID, op.Site,
			"protected callback "+op.Name+" ran without a valid credential")
	}
}

// applyReturn executes an OpReturn and returns the terminal classification.
func (s *effectState) applyReturn(blockID string, op OpReturn) Terminal {
	s.record(EventReturn, blockID, op.Site,
		"denied="+boolString(op.Denied)+" errorVar="+op.ErrVar)

	terminal := Terminal{
		Kind:   TerminalReturn,
		Block:  blockID,
		Site:   op.Site,
		Denied: op.Denied,
	}

	if !s.failed {
		return terminal
	}

	// An explicit denial correctly propagates the security decision.
	if op.Denied {
		return terminal
	}

	if origin, ok := s.holdsAuthError(op.ErrVar); ok {
		// The auth error is still the value returned: failure surfaces.
		terminal.FailureSurfaced = true
		_ = origin
		return terminal
	}

	// Failed path, success-shaped return, auth error not surfaced: the
	// intended rejection has been converted into a success.
	detail := "return presents success after auth failure at " + s.failSite
	if s.masked {
		detail += " (error was masked)"
	}
	s.addFinding(FindingRejectionConverted, blockID, op.Site, detail)
	return terminal
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

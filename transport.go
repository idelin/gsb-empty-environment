package netsec

import "net/http"

// RoundTrip validates every request independently, then dials only the
// addresses that were resolved and approved for that request. Redirects made
// by http.Client pass through this method again.
func (v *Validator) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, ErrInvalidURL
	}
	ctx := req.Context()
	target := targetFromContext(ctx)
	if target == nil || target.owner != v || target.requestURL == nil || target.canonical == "" ||
		target.canonical != req.URL.String() {
		var err error
		target, err = v.ValidateURLContext(ctx, req.URL)
		if err != nil {
			return nil, err
		}
	}
	if req.Host != "" && !v.hostHeaderMatches(target, req.Host) {
		return nil, validationError(ErrTargetMismatch, req.URL.String(), target.dialHost, req.Host)
	}

	req2 := req.Clone(withTarget(ctx, target))
	req2.URL = cloneURL(target.requestURL)
	return v.transport.RoundTrip(req2)
}

// CheckRedirect applies static URL policy before Go follows a redirect.
// Resolution and address checks happen again in RoundTrip for the new hop.
func (v *Validator) CheckRedirect(req *http.Request, via []*http.Request) error {
	if v.maxRedirects == 0 || len(via) > v.maxRedirects {
		return ErrTooManyRedirects
	}
	if req == nil || req.URL == nil {
		return ErrInvalidURL
	}
	inspected, err := inspectURL(req.URL)
	if err != nil {
		return err
	}
	if err := v.checkPort(inspected.host, inspected.port, inspected.scheme); err != nil {
		return validationError(err, inspected.u.String(), inspected.host, "")
	}
	return nil
}

// Client returns an HTTP client whose transport pins every hop and whose
// redirect policy rechecks each URL.
func (v *Validator) Client() *http.Client {
	return &http.Client{
		Transport:     v,
		CheckRedirect: v.CheckRedirect,
		Timeout:       v.cfg.ClientTimeout,
	}
}

// Transport returns the validator as an http.RoundTripper.
func (v *Validator) Transport() http.RoundTripper {
	return v
}

// CloseIdleConnections closes idle connections held by the internal transport.
func (v *Validator) CloseIdleConnections() {
	if v.transport != nil {
		v.transport.CloseIdleConnections()
	}
}

// Ensure the public types satisfy the standard interfaces.
var (
	_ http.RoundTripper                   = (*Validator)(nil)
	_ interface{ CloseIdleConnections() } = (*Validator)(nil)
)

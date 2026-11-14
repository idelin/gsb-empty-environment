package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

const maxFormBody = 16 << 10

// HandlerConfig configures the magic-link HTTP handler.
type HandlerConfig struct {
	// Service is the issuing and consuming service. It is required.
	Service *Service

	// AllowedOrigins overrides Service's configured origins for this handler.
	// If both are empty, the handler compares Origin with the request's own
	// scheme and Host. Production deployments behind proxies should configure
	// explicit origins.
	AllowedOrigins []string

	// RequiredIntent, when set, requires the signed token to carry exactly this
	// session intent before it can establish a session.
	RequiredIntent string

	// ExpectedBinding optionally derives binding values from server-side
	// session state, such as a pending-login cookie. The token must match every
	// non-empty returned field.
	ExpectedBinding func(*http.Request) (Binding, error)

	// OnConsume runs only after a valid token has been spent by a trusted POST.
	// This is where the authentication service creates or rotates its session,
	// sets cookies, and records an audit event. It must not write a response
	// body or status; the handler writes the final response. Returning an
	// error produces a 500 response; the token is still spent.
	OnConsume func(context.Context, http.ResponseWriter, *http.Request, Claims) error

	// SuccessURL, when set, receives a 303 redirect after OnConsume succeeds.
	SuccessURL string
}

// Handler serves a confirmation page for GET and consumes a token only for an
// explicit, same-origin POST.
type Handler struct {
	service        *Service
	origins        []string
	requiredIntent string
	expected       func(*http.Request) (Binding, error)
	onConsume      func(context.Context, http.ResponseWriter, *http.Request, Claims) error
	successURL     string
	tmpl           *template.Template
}

// NewHandler validates cfg and returns an HTTP handler.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("%w: handler requires a service", ErrInvalidConfig)
	}
	if strings.ContainsAny(cfg.SuccessURL, "\r\n") {
		return nil, fmt.Errorf("%w: invalid success URL", ErrInvalidConfig)
	}
	if cfg.RequiredIntent != "" {
		if err := validateClaimString("intent", cfg.RequiredIntent, 128); err != nil {
			return nil, fmt.Errorf("%w: invalid required intent", ErrInvalidConfig)
		}
	}

	originSource := cfg.AllowedOrigins
	if len(originSource) == 0 {
		originSource = cfg.Service.allowedOrigins
	}
	origins := make([]string, 0, len(originSource))
	for _, raw := range originSource {
		origin, err := canonicalOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: allowed origin %q: %v", ErrInvalidConfig, raw, err)
		}
		origins = append(origins, origin)
	}

	return &Handler{
		service:        cfg.Service,
		origins:        origins,
		requiredIntent: cfg.RequiredIntent,
		expected:       cfg.ExpectedBinding,
		onConsume:      cfg.OnConsume,
		successURL:     cfg.SuccessURL,
		tmpl:           confirmationTemplate,
	}, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)

	switch r.Method {
	case http.MethodGet:
		h.handleProbe(w, r)
	case http.MethodPost:
		h.handleConsume(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		h.writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET to confirm or POST to continue")
	}
}

func (h *Handler) handleProbe(w http.ResponseWriter, r *http.Request) {
	token, err := tokenFromQuery(r)
	if err != nil {
		h.writeProblem(w, http.StatusBadRequest, "invalid_request", "A single token is required")
		return
	}

	claims, err := h.service.Probe(r.Context(), token)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	data := confirmationData{
		Token:  token,
		Intent: claims.Intent,
	}
	if err := h.tmpl.Execute(w, data); err != nil {
		return
	}
}

func (h *Handler) handleConsume(w http.ResponseWriter, r *http.Request) {
	action, origins, err := h.trustedAction(r)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	token, err := tokenFromPost(w, r)
	if err != nil {
		h.writeProblem(w, http.StatusBadRequest, "invalid_request", "A single token is required")
		return
	}

	expected := Binding{Intent: h.requiredIntent}
	if h.expected != nil {
		fromSession, err := h.expected(r)
		if err != nil {
			h.writeProblem(w, http.StatusForbidden, "binding_mismatch", "The pending session does not match this link")
			return
		}
		if expected.UserID != "" && fromSession.UserID != "" && expected.UserID != fromSession.UserID {
			h.writeProblem(w, http.StatusForbidden, "binding_mismatch", "The pending session does not match this link")
			return
		}
		if expected.Intent != "" && fromSession.Intent != "" && expected.Intent != fromSession.Intent {
			h.writeProblem(w, http.StatusForbidden, "binding_mismatch", "The pending session does not match this link")
			return
		}
		if expected.SessionID != "" && fromSession.SessionID != "" && expected.SessionID != fromSession.SessionID {
			h.writeProblem(w, http.StatusForbidden, "binding_mismatch", "The pending session does not match this link")
			return
		}
		expected = mergeBindings(expected, fromSession)
	}

	claims, err := h.service.consume(r.Context(), token, expected, action, origins)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	if h.onConsume != nil {
		if err := h.onConsume(r.Context(), w, r, claims); err != nil {
			h.writeProblem(w, http.StatusInternalServerError, "session_error", "The link was used but the session could not be established")
			return
		}
	}

	if h.successURL != "" {
		http.Redirect(w, r, h.successURL, http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) trustedAction(r *http.Request) (Action, []string, error) {
	action, err := actionFromRequest(r)
	if err != nil {
		return Action{}, nil, err
	}
	origins := h.origins
	if len(origins) == 0 {
		origin, err := requestOrigin(r)
		if err != nil {
			return Action{}, nil, ErrUntrustedAction
		}
		origins = []string{origin}
	}
	if err := validateAction(action, origins); err != nil {
		return Action{}, nil, err
	}
	return action, origins, nil
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUntrustedAction),
		errors.Is(err, ErrOriginNotConfigured),
		errors.Is(err, ErrBindingMismatch):
		h.writeProblem(w, http.StatusForbidden, "untrusted_request", "This link cannot be used from this request")
	case errors.Is(err, ErrTokenExpired),
		errors.Is(err, ErrTokenConsumed):
		h.writeProblem(w, http.StatusGone, "link_unavailable", "This link is expired or has already been used")
	case errors.Is(err, ErrInvalidToken),
		errors.Is(err, ErrTokenNotYetValid):
		h.writeProblem(w, http.StatusBadRequest, "invalid_token", "This link is not valid")
	case errors.Is(err, ErrStoreUnavailable):
		h.writeProblem(w, http.StatusServiceUnavailable, "temporarily_unavailable", "Try again later")
	default:
		h.writeProblem(w, http.StatusInternalServerError, "internal_error", "The request could not be completed")
	}
}

func (h *Handler) writeProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}

func tokenFromQuery(r *http.Request) (string, error) {
	values, ok := r.URL.Query()["token"]
	if !ok || len(values) != 1 || values[0] == "" {
		return "", errors.New("single token required")
	}
	return values[0], nil
}

func tokenFromPost(w http.ResponseWriter, r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		return "", err
	}

	bodyValues := r.PostForm["token"]
	queryValues := r.URL.Query()["token"]
	if len(bodyValues) > 1 || len(queryValues) > 1 {
		return "", errors.New("ambiguous token")
	}

	var bodyToken, queryToken string
	if len(bodyValues) == 1 {
		bodyToken = bodyValues[0]
	}
	if len(queryValues) == 1 {
		queryToken = queryValues[0]
	}
	if bodyToken != "" && queryToken != "" && bodyToken != queryToken {
		return "", errors.New("ambiguous token")
	}
	if bodyToken != "" {
		return bodyToken, nil
	}
	if queryToken != "" {
		return queryToken, nil
	}
	return "", errors.New("single token required")
}

func mergeBindings(base, override Binding) Binding {
	merged := base
	if override.UserID != "" {
		merged.UserID = override.UserID
	}
	if override.Intent != "" {
		merged.Intent = override.Intent
	}
	if override.SessionID != "" {
		merged.SessionID = override.SessionID
	}
	return merged
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

type confirmationData struct {
	Token  string
	Intent string
}

var confirmationTemplate = template.Must(template.New("confirm").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Confirm sign-in</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:34rem;margin:4rem auto;padding:0 1rem;color:#18212b}
.card{border:1px solid #d7dee8;border-radius:12px;padding:1.5rem}
button{font:inherit;cursor:pointer;border:0;border-radius:8px;padding:.75rem 1.25rem;background:#1259c3;color:#fff}
.note{color:#536273;font-size:.95rem}
</style>
</head>
<body>
<div class="card">
<h1>Confirm sign-in</h1>
<p class="note">A link preview alone does not sign you in. Select Continue to use this magic link.</p>
<form method="post">
<input type="hidden" name="token" value="{{.Token}}">
<button type="submit">Continue</button>
</form>
</div>
</body>
</html>
`))

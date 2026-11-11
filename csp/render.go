package csp

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
)

// Renderer executes an html/template per request with an isolated policy.
// The template gains functions that derive eligible scripts through the
// request's trusted node tree.
type Renderer struct {
	builder  *Builder
	template *template.Template

	// Data builds the template data for an HTTP request. nil data is passed
	// when the hook is unset.
	Data func(*http.Request) (any, error)
}

// NewRenderer binds a builder and a parsed template.
func NewRenderer(b *Builder, t *template.Template) *Renderer {
	return &Renderer{builder: b, template: t}
}

// policyFuncs exposes the trusted derivation interface to templates. The
// same names must exist (possibly as stubs) when the template is parsed.
func policyFuncs(p *Policy) template.FuncMap {
	return template.FuncMap{
		"cspNonce": func() string { return p.Nonce() },
		"trustedScript": func(body string) (template.HTML, error) {
			deriver, ok := p.Root().(ScriptDeriver)
			if !ok {
				return "", ErrUntrusted
			}
			script, err := deriver.DeriveScript(body)
			if err != nil {
				return "", err
			}
			return script.HTML(), nil
		},
		"deriveScript": func(parent ScriptDeriver, body string) (template.HTML, error) {
			script, err := parent.DeriveScript(body)
			if err != nil {
				return "", err
			}
			return script.HTML(), nil
		},
	}
}

// cancelWriter fails fast once the render context is canceled, so a canceled
// request stops producing bytes instead of draining the template.
type cancelWriter struct {
	ctx context.Context
	dst io.Writer
}

func (c cancelWriter) Write(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.dst.Write(b)
}

// Render executes the template against w under the policy carried by ctx.
// The CSP header is emitted and the policy frozen before any body bytes are
// written; the policy is always released when Render returns, including on
// cancellation or enforcement errors.
func (r *Renderer) Render(ctx context.Context, w io.Writer, data any) (err error) {
	p := FromContext(ctx)
	if p == nil {
		p, err = r.builder.NewPolicy()
		if err != nil {
			return err
		}
		ctx = WithPolicy(ctx, p)
	}
	defer func() {
		if releaseErr := p.Release(); releaseErr != nil && err == nil {
			err = releaseErr
		}
	}()

	// Header first, then freeze: no directive changes survive emission.
	if rw, ok := w.(http.ResponseWriter); ok {
		rw.Header().Set("Content-Security-Policy", p.HeaderValue())
	}
	if err := p.Freeze(); err != nil {
		return err
	}

	tmpl, err := r.template.Clone()
	if err != nil {
		return err
	}
	tmpl = tmpl.Funcs(policyFuncs(p))

	sanitizer := NewSanitizingWriter(cancelWriter{ctx: ctx, dst: w}, p)
	if err := tmpl.Execute(sanitizer, data); err != nil {
		return fmt.Errorf("csp: template render failed: %w", err)
	}
	return sanitizer.Close()
}

// ServeHTTP gives every request its own policy, so nonces and approved-script
// sets can never cross request boundaries.
func (r *Renderer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p, err := r.builder.NewPolicy()
	if err != nil {
		http.Error(w, "csp: policy construction failed", http.StatusInternalServerError)
		return
	}
	ctx := WithPolicy(req.Context(), p)

	var data any
	if r.Data != nil {
		data, err = r.Data(req)
		if err != nil {
			_ = p.Release()
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}

	tracker := &commitTracker{ResponseWriter: w}
	w = tracker
	if err := r.Render(ctx, w, data); err != nil {
		committed := tracker.committed
		// The CSP header is committed before the body, so a 500 status is only
		// observable when no body byte has been flushed yet. Once streaming
		// started, enforcement has already prevented the offending bytes and
		// the handler simply aborts the remaining output.
		if !committed {
			http.Error(w, "request aborted by content security policy",
				http.StatusInternalServerError)
		}
	}
}

// commitTracker records whether any body byte has been written, which means
// the response status is now committed.
type commitTracker struct {
	http.ResponseWriter
	committed bool
}

func (c *commitTracker) Write(p []byte) (int, error) {
	if len(p) > 0 {
		c.committed = true
	}
	return c.ResponseWriter.Write(p)
}

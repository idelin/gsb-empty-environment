// Package csp builds request-scoped Content-Security-Policy state for Go
// html/template services that stream server-rendered pages.
//
// A shared Builder hands every request its own Policy with a fresh nonce and
// an empty approved-script set, so a nonce derived for one request can never
// authorize a script in another request. Policies are frozen once response
// headers are emitted and recycled through a pool when the request ends,
// including canceled renders.
//
// Inline scripts inherit eligibility only through the request's trusted node
// tree: the policy root derives a TrustedScript, and each TrustedScript can in
// turn derive child scripts. Nodes created outside that tree are rejected.
// A SanitizingWriter enforces the same rules on every emitted byte, catching
// scripts that bypass the derivation interface.
//
// Templates reach the interface through three functions that must exist at
// parse time (they may be stubs) and are rebound per request:
//
//	cspNonce       -> current request nonce
//	trustedScript  -> derive a script from the trusted root
//	deriveScript   -> derive a child script from a TrustedScript parent
//
// Example:
//
//	tmpl := template.Must(template.New("p").Funcs(template.FuncMap{
//	    "cspNonce":      func() string { return "" },
//	    "trustedScript": func(string) (template.HTML, error) { return "", nil },
//	    "deriveScript":  func(ScriptDeriver, string) (template.HTML, error) { return "", nil },
//	}).Parse(`{{trustedScript "boot()"}}`))
//	renderer := csp.NewRenderer(csp.NewBuilder("default-src 'none'"), tmpl)
//	http.Handle("/", renderer)
package csp

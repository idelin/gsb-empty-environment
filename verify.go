package enumkit

import "strconv"

// Verify walks every migration path in the merged graph: for every
// ordered pair of versions and every token accepted at the source
// version it migrates forward, renders the canonical name at the target
// version, then migrates that name back and requires the same stable
// ID. Any ambiguity or drift is returned as a VerifyError carrying the
// shortest conflict chain (when present).
func (g *Graph) Verify() []error {
	var errs []error
	vers := g.versions
	for _, from := range vers {
		rv := g.reg.versions[from]
		tokens := make([]string, 0, len(rv.resolve))
		for name := range rv.resolve {
			tokens = append(tokens, name)
		}
		for _, to := range vers {
			for _, token := range tokens {
				v, err := g.Migrate(from, to, token)
				if err != nil {
					errs = append(errs, &VerifyError{Enum: g.reg.name, From: from, To: to, Token: token, Err: err})
					continue
				}
				if !v.Known() {
					// Unknown source tokens pass through; nothing
					// identity-preserving to verify beyond lexically identical output.
					if g.CanonicalAt(to, v) != v.Raw {
						errs = append(errs, &VerifyError{
							Enum: g.reg.name, From: from, To: to, Token: token,
							Err: errChanged(v.Raw, g.CanonicalAt(to, v)),
						})
					}
					continue
				}
				canon := g.CanonicalAt(to, v)
				back, err := g.Migrate(to, from, canon)
				if err != nil {
					errs = append(errs, &VerifyError{Enum: g.reg.name, From: from, To: to, Token: token, Err: err})
					continue
				}
				if !back.Known() || back.ID != v.ID {
					errs = append(errs, &VerifyError{
						Enum: g.reg.name, From: from, To: to, Token: token,
						Err: errChangedID(v.ID, back),
					})
				}
			}
		}
	}
	return errs
}

type changedError struct{ msg string }

func (e *changedError) Error() string { return e.msg }
func errChanged(src, dst string) error {
	return &changedError{msg: "passthrough token altered: " + src + " -> " + dst}
}
func errChangedID(expect int64, got Value) error {
	if !got.Known() {
		return &changedError{msg: "identity lost on round-trip: expected #" + strconv.FormatInt(expect, 10) + ", got unknown token " + got.Raw}
	}
	return &changedError{msg: "identity changed on round-trip: expected #" + strconv.FormatInt(expect, 10) + ", got #" + strconv.FormatInt(got.ID, 10)}
}

package netsec

// Arg is a single template argument bound to a fixed template position.
//
// The slice position of an Arg (its index in the slice passed to Format and
// friends) is the only identity the argument has: format verbs address it
// positionally, including explicit indexes such as %[2]s and repeated uses of
// the same position. Sensitive marks travel with the value, so a redaction
// decision can never be silently detached from the secret it protects.
type Arg struct {
	// Sensitive marks the value as a secret. A sensitive value is replaced in
	// its entirety by the redaction token before any width, precision or output
	// budget is applied. The original value is therefore never truncated, never
	// passed to the standard formatter and never written to the output.
	Sensitive bool

	// Value is the argument value. It is only ever formatted when Sensitive is
	// false.
	Value any
}

// Option customizes a formatting call. Options never change the ordering of
// redaction relative to budgeting: redaction always happens first.
type Option func(*config)

type config struct {
	budget         int
	redactionToken string
	truncationMark string
	errorMark      string
}

func defaultConfig() config {
	return config{
		budget:         -1,
		redactionToken: "[REDACTED]",
		truncationMark: "...",
		errorMark:      "[FORMAT_ERROR]",
	}
}

// WithBudget limits the number of bytes written to the output. A non-positive
// budget means nothing is written. When the formatted output exceeds the
// budget it is shortened on UTF-8 code-point boundaries and, when room allows,
// the truncation marker is emitted inside the same budget.
func WithBudget(bytes int) Option {
	return func(c *config) { c.budget = bytes }
}

// WithRedactionToken replaces the default "[REDACTED]" token used for
// sensitive arguments. Width and precision of the consuming verb apply to this
// token rather than to the secret.
func WithRedactionToken(token string) Option {
	return func(c *config) { c.redactionToken = token }
}

// WithTruncationMarker replaces the default "..." marker appended when the
// output budget truncates content. The marker is accounted for inside the
// budget; when no room remains the marker is shortened or omitted.
func WithTruncationMarker(marker string) Option {
	return func(c *config) { c.truncationMark = marker }
}

// WithErrorMarker replaces the default "[FORMAT_ERROR]" marker emitted when a
// template is malformed. Error output never contains argument values.
func WithErrorMarker(marker string) Option {
	return func(c *config) { c.errorMark = marker }
}

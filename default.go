package netsec

import (
	"context"
	"io"
	"net/http"
	"net/url"
)

var defaultValidator = MustNewValidator(Config{})

// Default returns the package-level validator with default policy. It rejects
// private, loopback, link-local, and reserved addresses, and only permits
// standard HTTP(S) ports.
func Default() *Validator {
	return defaultValidator
}

// Validate validates rawURL with the default policy.
func Validate(rawURL string) (*Target, error) {
	return defaultValidator.Validate(rawURL)
}

// ValidateTarget is an alias for Validate.
func ValidateTarget(rawURL string) (*Target, error) {
	return Validate(rawURL)
}

// ParseTarget is an alias for Validate.
func ParseTarget(rawURL string) (*Target, error) {
	return Validate(rawURL)
}

// ValidateURL validates an already parsed URL with the default policy.
func ValidateURL(u *url.URL) (*Target, error) {
	return defaultValidator.ValidateURL(u)
}

// Check validates rawURL with the default policy and returns only the error.
func Check(rawURL string) error {
	_, err := Validate(rawURL)
	return err
}

// CheckTarget is an alias for Check.
func CheckTarget(rawURL string) error {
	return Check(rawURL)
}

// Client returns an HTTP client using the default validator.
func Client() *http.Client {
	return defaultValidator.Client()
}

// NewRequest parses rawURL once with the default policy and returns a request
// pinned to the validated target.
func NewRequest(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, *Target, error) {
	return defaultValidator.NewRequest(ctx, method, rawURL, body)
}

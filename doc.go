// Package netsec provides routing-safety checks for a protocol gateway.
//
// This package adjudicates HTTP trailer blocks. Trailers arrive only after the
// message body is complete, i.e. after routing has already happened, so they
// must never be allowed to change the effective host or the framing/length of
// the message. A trailer containing host, content-length, transfer-encoding or
// any pseudo-header (a field whose name starts with ":") therefore fails the
// whole stream. Custom trailers that were declared ahead of the body remain
// allowed and pass through untouched.
//

// The typical gateway use is:
//
//	validator := netsec.NewTrailerValidator(announcedTrailerNames)
//	if err := validator.Validate(req.Trailer); err != nil {
//		// Fail and reset the stream; do not forward the trailers.
//	}
//
// Use WithRequireDeclared to additionally enforce the RFC 7230 rule that a
// trailer must have been announced in the Trailer header before the body.
package netsec

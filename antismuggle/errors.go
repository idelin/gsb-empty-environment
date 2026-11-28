package antismuggle

// Reason is a machine-readable classification of a hard protocol violation.
type Reason string

const (
	// ReasonBareLF means a line was terminated by LF without a preceding CR.
	ReasonBareLF Reason = "bare_lf"
	// ReasonBareCR means a CR was not immediately followed by LF.
	ReasonBareCR Reason = "bare_cr"
	// ReasonObsFold means a header continuation line (obs-fold) was seen.
	ReasonObsFold Reason = "obs_fold"
	// ReasonLineTooLong means a line exceeded the configured length limit.
	ReasonLineTooLong Reason = "line_too_long"
	// ReasonBadRequestLine means the request line is malformed.
	ReasonBadRequestLine Reason = "bad_request_line"
	// ReasonBadHeaderName means a field name contains illegal bytes.
	ReasonBadHeaderName Reason = "bad_header_name"
	// ReasonBadHeaderValue means a field value contains illegal bytes.
	ReasonBadHeaderValue Reason = "bad_header_value"
	// ReasonBothCLAndTE means Content-Length and Transfer-Encoding coexist.
	ReasonBothCLAndTE Reason = "content_length_and_transfer_encoding"
	// ReasonMultipleCL means more than one Content-Length field is present.
	ReasonMultipleCL Reason = "multiple_content_length"
	// ReasonBadCL means a Content-Length value is not a pure decimal length.
	ReasonBadCL Reason = "bad_content_length"
	// ReasonBadTE means a Transfer-Encoding value is not the single exact
	// "chunked" token accepted by this gateway.
	ReasonBadTE Reason = "bad_transfer_encoding"
	// ReasonBadConnectionOption means a forbidden connection token was used
	// (e.g. "Connection: Transfer-Encoding", a classic TE.TE smuggle vector).
	ReasonBadConnectionOption Reason = "bad_connection_option"
	// ReasonBadChunkSize means a chunk-size line is malformed.
	ReasonBadChunkSize Reason = "bad_chunk_size"
	// ReasonChunkTooLarge means a chunk exceeds the configured maximum.
	ReasonChunkTooLarge Reason = "chunk_too_large"
	// ReasonBadChunkExtension means a chunk extension is overlong or contains
	// control characters.
	ReasonBadChunkExtension Reason = "bad_chunk_extension"
	// ReasonBadChunkTerminator means chunk data was not followed by exact CRLF.
	ReasonBadChunkTerminator Reason = "bad_chunk_terminator"
	// ReasonBodyTooLarge means the decoded body exceeds the configured maximum.
	ReasonBodyTooLarge Reason = "body_too_large"
	// ReasonTrailerForbidden means a trailer is not on the allow list or is a
	// forbidden hop/security header.
	ReasonTrailerForbidden Reason = "trailer_forbidden"
	// ReasonTrailerAnnouncement means the Trailer header announces a field
	// that the policy would reject.
	ReasonTrailerAnnouncement Reason = "bad_trailer_announcement"
	// ReasonStreamPoisoned means a prior fatal error already killed the stream.
	ReasonStreamPoisoned Reason = "stream_poisoned"
	// ReasonBodyPending means Next was called before the previous body was
	// fully consumed.
	ReasonBodyPending Reason = "body_pending"
	// ReasonTruncated means the input ended in the middle of a frame.
	ReasonTruncated Reason = "truncated"
)

// Error is a hard protocol violation. Every Error forces the connection to
// be closed: a strict anti-smuggling gateway never attempts to recover a
// frame whose boundaries are in doubt.
type Error struct {
	Reason          Reason
	Message         string
	CloseConnection bool
}

func (e *Error) Error() string {
	return "antismuggle: " + string(e.Reason) + ": " + e.Message
}

// StatusCode always reports 400 Bad Request for hard framing violations.
func (e *Error) StatusCode() int { return 400 }

func hardError(reason Reason, format string, args ...any) *Error {
	return &Error{
		Reason:          reason,
		Message:         fmtSprintf(format, args...),
		CloseConnection: true,
	}
}

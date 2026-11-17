package netsec

// Severity of a finding. Error findings invalidate the security claim;
// clue findings are unauthenticated material worth investigating but prove
// nothing on their own.
type Severity string

const (
	// SeverityError marks a condition that breaks the authenticated
	// negotiation guarantee. Any error finding makes the report insecure.
	SeverityError Severity = "error"
	// SeverityClue marks unauthenticated material that may indicate a
	// hidden capability or tampering but cannot prove anything.
	SeverityClue Severity = "clue"
)

// Finding codes emitted by the checker.
const (
	FindingMalformedMessage              = "malformed_message"
	FindingSessionMismatch               = "session_mismatch"
	FindingUnauthenticated               = "unauthenticated"
	FindingSignerMismatch                = "signer_mismatch"
	FindingOrderViolation                = "order_violation"
	FindingDuplicateMessage              = "duplicate_message"
	FindingMissingCapabilities           = "missing_capabilities"
	FindingMissingSelection              = "missing_selection"
	FindingSelectionNotInCapabilities    = "selection_not_in_capabilities"
	FindingAlgorithmNotRanked            = "algorithm_not_ranked"
	FindingPreferenceViolation           = "preference_violation"
	FindingMissingConfirmation           = "missing_confirmation"
	FindingConfirmationHashMismatch      = "confirmation_hash_mismatch"
	FindingConfirmationAlgorithmMismatch = "confirmation_algorithm_mismatch"
	FindingUnauthenticatedCapabilities   = "unauthenticated_capabilities"
	FindingUnauthenticatedSelection      = "unauthenticated_selection"
	FindingUnauthenticatedConfirmation   = "unauthenticated_confirmation"
)

// Finding is one structured result of a transcript check.
type Finding struct {
	// Index is the zero-based envelope index, or -1 for transcript-wide
	// findings.
	Index    int
	Code     string
	Severity Severity
	Peer     string
	Detail   string
}

// Transcript is the verification host's input: the ordered raw entries and
// the caller-supplied preference order, strongest first.
type Transcript struct {
	// Envelopes are the captured messages in observed order.
	Envelopes []Envelope
	// PreferenceOrder ranks algorithms from most to least preferred. An
	// algorithm absent from the list has no rank and cannot satisfy the
	// preference requirement.
	PreferenceOrder []string
}

// Report is the full check result.
type Report struct {
	// Secure is true only when no error findings were emitted.
	Secure bool
	// SessionID is the session identifier taken from parsed messages, or
	// empty when none parsed.
	SessionID string
	// TranscriptHash is the SHA-256 digest over every envelope's raw bytes
	// in order, hex encoded.
	TranscriptHash string
	// AuthenticatedCapabilities maps each peer to its authenticated set.
	AuthenticatedCapabilities map[string][]string
	// UnauthenticatedCapabilities maps each peer to capability names seen
	// only on entries that failed authentication. These are clues.
	UnauthenticatedCapabilities map[string][]string
	// AuthenticatedSelection is the authenticated selected algorithm, or
	// empty when none exists.
	AuthenticatedSelection string
	// Intersection is the authenticated capability intersection of both
	// peers, ordered by the input preference order.
	Intersection []string
	Findings     []Finding
}

// HasError reports whether any error-severity finding with the given code
// exists.
func (r *Report) HasError(code string) bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError && f.Code == code {
			return true
		}
	}
	return false
}

// HasCode reports whether any finding with the given code exists.
func (r *Report) HasCode(code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

package netsec

// Report is the result of processing an SNMPv3 USM message at the time-window
// boundary. Reports are comparable, so callers can switch on or compare the
// return value directly.
type Report string

// USM reports emitted by this package. In particular, there is deliberately
// no distinct "unknown user" report: an unknown user and a failed
// authentication are both AuthenticationFailed.
const (
	// ReportOK means that authentication and the time-window check succeeded.
	ReportOK Report = ""
	// NotInTimeWindow is returned when the message's engineBoots differs from
	// the local value, or when its engineTime is outside the 150 second window.
	NotInTimeWindow Report = "notInTimeWindow"
	// UnknownEngineID is the only report returned for a discovery request for
	// an engine that this validator does not manage.
	UnknownEngineID Report = "unknownEngineID"
	// AuthenticationFailed covers both an unknown username and a failed
	// authentication callback. They intentionally share the same report.
	AuthenticationFailed Report = "authenticationFailed"
)

// Compatibility names for callers that spell reports as constants or errors.
const (
	OK                         = ReportOK
	ReportNotInTimeWindow      = NotInTimeWindow
	ReportUnknownEngineID      = UnknownEngineID
	ReportAuthenticationFailed = AuthenticationFailed

	NotInTimeWindowReport      = NotInTimeWindow
	UnknownEngineIDReport      = UnknownEngineID
	AuthenticationFailedReport = AuthenticationFailed

	ErrNotInTimeWindow      = NotInTimeWindow
	ErrUnknownEngineID      = UnknownEngineID
	ErrAuthenticationFailed = AuthenticationFailed
)

// Status is an alias for Report.
type Status = Report

// IsOK reports whether the report indicates a successful operation.
func (r Report) IsOK() bool {
	return r == ReportOK
}

// Code returns the protocol-style report code.
func (r Report) Code() string {
	return string(r)
}

// Error implements error. A successful Report is still a non-nil error value;
// use IsOK or Err when a nil error is required.
func (r Report) Error() string {
	return string(r)
}

// Err returns the report as an error, or nil for ReportOK.
func (r Report) Err() error {
	if r.IsOK() {
		return nil
	}
	return r
}

package rtsp

import "strconv"

// RTSP status codes used by the session manager.
const (
	StatusBadRequest          = 400
	StatusSessionNotFound     = 454
	StatusMethodNotValidState = 455
)

// Error is a session-manager failure that carries the RTSP status code the
// server should return to the client.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return "rtsp: " + strconv.Itoa(e.Status) + " " + e.Message
}

var (
	// errBadRequest covers malformed Transport headers, invalid port pairs,
	// non-increasing CSeq values and bad setup arguments.
	errBadRequest = &Error{Status: StatusBadRequest, Message: "bad request"}
	// errSessionNotFound is returned both when no session matches the id and
	// when the requesting principal does not own it. Returning the same error
	// avoids leaking which ids exist to probing clients.
	errSessionNotFound = &Error{Status: StatusSessionNotFound, Message: "session not found"}
	errInvalidState    = &Error{Status: StatusMethodNotValidState, Message: "method not valid in this state"}
)

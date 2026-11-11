package passkey

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
)

// MaxAssertionBody caps the request body so a login endpoint cannot be used as
// an unbounded memory sink.
const MaxAssertionBody = 1 << 16

type loginResponse struct {
	Token        string `json:"sessionToken"`
	CredentialID string `json:"credentialId"`
	Counter      string `json:"counter"`
	SignCount    uint32 `json:"signCount"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// NewLoginCompleteHandler returns an http.Handler that accepts a JSON
// AssertionInput, runs Service.FinishAssertion and, on success, answers 200
// with the session token. Every verifier error is mapped deterministically to a
// status code so clients cannot probe for internal details.
func NewLoginCompleteHandler(svc *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxAssertionBody)
		var in AssertionInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		result, err := svc.FinishAssertion(in)
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}

		writeJSON(w, http.StatusOK, loginResponse{
			Token:        result.SessionToken,
			CredentialID: base64.StdEncoding.EncodeToString(result.CredentialID),
			Counter:      result.Counter.String(),
			SignCount:    result.SignCount,
		})
	})
}

// statusForError maps verifier failures to HTTP status codes. Notably
// ErrChallengeAlreadyUsed is 409 (conflict) and ErrInvalidSignature is 401.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ErrMalformedAssertion),
		errors.Is(err, ErrInvalidClientData):
		return http.StatusBadRequest
	case errors.Is(err, ErrUnknownCredential),
		errors.Is(err, ErrInvalidSignature),
		errors.Is(err, ErrCounterReplay),
		errors.Is(err, ErrBackupFlagMismatch):
		return http.StatusUnauthorized
	case errors.Is(err, ErrRPIDMismatch),
		errors.Is(err, ErrOriginMismatch),
		errors.Is(err, ErrChallengeMismatch),
		errors.Is(err, ErrUserMismatch):
		return http.StatusForbidden
	case errors.Is(err, ErrChallengeExpired):
		return http.StatusGone
	case errors.Is(err, ErrChallengeAlreadyUsed):
		return http.StatusConflict
	case errors.Is(err, ErrChallengeNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

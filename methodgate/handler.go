package methodgate

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// errorEnvelope is the JSON-RPC 2.0 response object returned on rejection.
type errorEnvelope struct {
	JSONRPC string    `json:"jsonrpc"`
	Error   *RPCError `json:"error"`
	ID      any       `json:"id"`
}

// maxBodyBytes bounds how much request data the gate will read.
const maxBodyBytes = 8 << 20

// Handler returns an http.Handler that runs the gate before forwarding to next.
// Requests that fail inspection never reach next; a JSON-RPC 2.0 error is
// written instead. next is typically the reverse proxy's ServeHTTP method.
func (g *Gate) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			writeRPCError(w, &RPCError{Code: CodeParseError, Message: "parse error: cannot read request body"})
			return
		}

		if err := g.Inspect(body); err != nil {
			rpcErr, ok := err.(*RPCError)
			if !ok {
				rpcErr = &RPCError{Code: CodePolicyViolation, Message: err.Error()}
			}
			writeRPCError(w, rpcErr)
			return
		}

		// Restore the consumed body so the upstream reverse proxy can read it.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))

		next.ServeHTTP(w, r)
	})
}

func writeRPCError(w http.ResponseWriter, rpcErr *RPCError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		JSONRPC: "2.0",
		Error:   rpcErr,
		ID:      nil,
	})
}

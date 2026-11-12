package taskpkg

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// JSON wire types. Byte slices travel as base64 strings.

type wireRequest struct {
	Sequence        uint64 `json:"sequence"`
	Subject         string `json:"subject"`
	Summary         string `json:"summary"`
	Payload         string `json:"payload,omitempty"`
	OperationDigest string `json:"operation_digest"`
	PriorCommit     string `json:"prior_commit"`
}

type wireResponse struct {
	Sequence        uint64 `json:"sequence"`
	Subject         string `json:"subject"`
	OperationDigest string `json:"operation_digest"`
	PriorCommit     string `json:"prior_commit"`
	Signature       string `json:"signature"`
}

// HTTPWitness is a Witness that obtains confirmations from a trusted remote
// service over HTTP. Under a network partition every call fails and the
// committer fails closed.
type HTTPWitness struct {
	endpoint   string
	publicKey  ed25519.PublicKey
	httpClient *http.Client
}

// NewHTTPWitness builds an HTTP witness. endpoint is the full callback URL.
func NewHTTPWitness(endpoint string, publicKey ed25519.PublicKey) *HTTPWitness {
	return &HTTPWitness{
		endpoint:  endpoint,
		publicKey: publicKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// SetTransport overrides the HTTP transport. It exists for tests and for
// deployments that need a custom dialer/TLS configuration.
func (w *HTTPWitness) SetTransport(transport http.RoundTripper) {
	w.httpClient.Transport = transport
}

// PublicKey implements Witness.
func (w *HTTPWitness) PublicKey() ed25519.PublicKey {
	return w.publicKey
}

// RequestConfirmation implements Witness.
func (w *HTTPWitness) RequestConfirmation(ctx context.Context, req CommitmentRequest) (Confirmation, error) {
	body, err := json.Marshal(wireRequest{
		Sequence:        req.Sequence,
		Subject:         req.Operation.Subject,
		Summary:         req.Operation.Summary,
		Payload:         base64.StdEncoding.EncodeToString(req.Operation.Payload),
		OperationDigest: base64.StdEncoding.EncodeToString(req.OperationDigest),
		PriorCommit:     base64.StdEncoding.EncodeToString(req.PriorCommit),
	})
	if err != nil {
		return Confirmation{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint, bytes.NewReader(body))
	if err != nil {
		return Confirmation{}, errors.Join(ErrWitnessUnavailable, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := w.httpClient.Do(httpReq)
	if err != nil {
		return Confirmation{}, errors.Join(ErrWitnessUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Confirmation{}, errors.Join(ErrWitnessUnavailable, errors.New("witness returned status "+resp.Status))
	}

	var wire wireResponse
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return Confirmation{}, errors.Join(ErrWitnessUnavailable, err)
	}
	return decodeWireResponse(wire)
}

func decodeWireResponse(wire wireResponse) (Confirmation, error) {
	digest, err := base64.StdEncoding.DecodeString(wire.OperationDigest)
	if err != nil {
		return Confirmation{}, errors.Join(ErrWitnessUnavailable, err)
	}
	prior, err := base64.StdEncoding.DecodeString(wire.PriorCommit)
	if err != nil {
		return Confirmation{}, errors.Join(ErrWitnessUnavailable, err)
	}
	signature, err := base64.StdEncoding.DecodeString(wire.Signature)
	if err != nil {
		return Confirmation{}, errors.Join(ErrWitnessUnavailable, err)
	}
	return Confirmation{
		Sequence:        wire.Sequence,
		Subject:         wire.Subject,
		OperationDigest: digest,
		PriorCommit:     prior,
		Signature:       signature,
	}, nil
}

// WitnessHandler exposes a LocalWitness as the HTTP callback expected by
// HTTPWitness. It is primarily used to host the trusted service and in tests.
func WitnessHandler(witness *LocalWitness) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var wire wireRequest
		if err := json.NewDecoder(req.Body).Decode(&wire); err != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		payload, err := base64.StdEncoding.DecodeString(wire.Payload)
		if err != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		digest, err := base64.StdEncoding.DecodeString(wire.OperationDigest)
		if err != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		prior, err := base64.StdEncoding.DecodeString(wire.PriorCommit)
		if err != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		confirmation, err := witness.RequestConfirmation(req.Context(), CommitmentRequest{
			Operation: Operation{
				Subject: wire.Subject,
				Summary: wire.Summary,
				Payload: payload,
			},
			Sequence:        wire.Sequence,
			PriorCommit:     prior,
			OperationDigest: digest,
		})
		if err != nil {
			http.Error(rw, "signing failure", http.StatusInternalServerError)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(wireResponse{
			Sequence:        confirmation.Sequence,
			Subject:         confirmation.Subject,
			OperationDigest: base64.StdEncoding.EncodeToString(confirmation.OperationDigest),
			PriorCommit:     base64.StdEncoding.EncodeToString(confirmation.PriorCommit),
			Signature:       base64.StdEncoding.EncodeToString(confirmation.Signature),
		})
	})
}

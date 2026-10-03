package witness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTP API exposed by the Witness. The datacenter controllers talk to the
// judge exclusively through these endpoints.

type leaseRequest struct {
	Holder    string `json:"holder"`
	Epoch     uint64 `json:"epoch"`
	TTLMillis int64  `json:"ttl_ms"`
}

type leaseResponse struct {
	Granted bool  `json:"granted"`
	Lease   Lease `json:"lease"`
}

// Handler returns the Witness HTTP handler.
func (w *Witness) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/lease/acquire", func(rw http.ResponseWriter, r *http.Request) {
		var req leaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		lease, ok := w.Acquire(req.Holder, time.Duration(req.TTLMillis)*time.Millisecond)
		writeJSON(rw, leaseResponse{Granted: ok, Lease: lease})
	})
	mux.HandleFunc("/lease/renew", func(rw http.ResponseWriter, r *http.Request) {
		var req leaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		lease, ok := w.Renew(req.Holder, req.Epoch, time.Duration(req.TTLMillis)*time.Millisecond)
		writeJSON(rw, leaseResponse{Granted: ok, Lease: lease})
	})
	mux.HandleFunc("/lease/status", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, leaseResponse{Lease: w.Status()})
	})
	return mux
}

func writeJSON(rw http.ResponseWriter, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(v)
}

// HTTPClient is a Witness client speaking the HTTP API above.
type HTTPClient struct {
	base string
	hc   *http.Client
}

// NewHTTPClient builds a client for the Witness at baseURL, e.g. http://127.0.0.1:9000.
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{base: baseURL, hc: &http.Client{Timeout: 2 * time.Second}}
}

func (c *HTTPClient) Acquire(holder string, ttl time.Duration) (Lease, bool, error) {
	return c.post("/lease/acquire", leaseRequest{Holder: holder, TTLMillis: int64(ttl / time.Millisecond)})
}

func (c *HTTPClient) Renew(holder string, epoch uint64, ttl time.Duration) (Lease, bool, error) {
	return c.post("/lease/renew", leaseRequest{Holder: holder, Epoch: epoch, TTLMillis: int64(ttl / time.Millisecond)})
}

func (c *HTTPClient) post(path string, req leaseRequest) (Lease, bool, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Lease{}, false, err
	}
	resp, err := c.hc.Post(c.base+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return Lease{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Lease{}, false, fmt.Errorf("witness: unexpected status %s", resp.Status)
	}
	var lr leaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return Lease{}, false, err
	}
	return lr.Lease, lr.Granted, nil
}

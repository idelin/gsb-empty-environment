package quota

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// This file wires the Master and Workers together over TCP using only
// net/http + encoding/json from the standard library: the Master exposes
// two endpoints and the worker's Heartbeater POSTs to them.
//
//	POST /register   initial fair-share allocation (rejects duplicates)
//	POST /heartbeat  steady-state batched renewal

// ServeHTTP exposes the Master over HTTP on addr (e.g. "127.0.0.1:0") and
// returns the listener. Close the listener to shut the server down.
func ServeHTTP(m *Master, addr string) (net.Listener, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		serve(func(req HeartbeatRequest) (HeartbeatResponse, error) {
			return m.Register(req.WorkerID)
		}, w, r)
	})
	mux.HandleFunc("/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		serve(m.Heartbeat, w, r)
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go func() { _ = http.Serve(ln, mux) }()
	return ln, nil
}

func serve(fn func(HeartbeatRequest) (HeartbeatResponse, error), w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := fn(req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrUnknownWorker) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HTTPHeartbeater talks to a remote Master over HTTP.
type HTTPHeartbeater struct {
	base    string
	client  *http.Client
	initial bool
}

// DialMaster creates a Heartbeater for the Master listening at addr.
func DialMaster(addr string) *HTTPHeartbeater {
	return &HTTPHeartbeater{base: "http://" + addr, client: &http.Client{}, initial: true}
}

// Heartbeat implements Heartbeater. The first call is routed to the
// /register endpoint so the worker receives its initial fair share;
// subsequent calls are steady-state batched renewals.
func (h *HTTPHeartbeater) Heartbeat(req HeartbeatRequest) (HeartbeatResponse, error) {
	path := "/heartbeat"
	if h.initial {
		path = "/register"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return HeartbeatResponse{}, err
	}
	resp, err := h.client.Post(h.base+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return HeartbeatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return HeartbeatResponse{}, fmt.Errorf("master: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	var out HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return HeartbeatResponse{}, err
	}
	h.initial = false
	return out, nil
}

// DirectHeartbeater calls a Master in-process, without sockets. Useful
// for single-binary deployments, demos, and tests.
type DirectHeartbeater struct {
	M       *Master
	initial bool
}

// NewDirectHeartbeater returns a Heartbeater backed by m directly.
func NewDirectHeartbeater(m *Master) *DirectHeartbeater {
	return &DirectHeartbeater{M: m, initial: true}
}

// Heartbeat implements Heartbeater; the first call registers the worker.
func (d *DirectHeartbeater) Heartbeat(req HeartbeatRequest) (HeartbeatResponse, error) {
	if d.initial {
		d.initial = false
		return d.M.Register(req.WorkerID)
	}
	return d.M.Heartbeat(req)
}

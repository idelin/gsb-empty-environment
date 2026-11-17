package netsec

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
)

// Handler adapts an Ingress to net/http CONNECT requests. It hijacks the
// connection and preserves bytes that net/http already buffered after the
// request headers, so they take part in the bounded early-data path.
type Handler struct {
	Ingress *Ingress

	// Target resolves the CONNECT authority. A nil function uses the request
	// Host (or URL host) and requires an explicit port.
	Target func(*http.Request) (Target, error)

	// Policy resolves the policy snapshot for a request. ConfigSource, when
	// present on Ingress, remains authoritative for both target and policy.
	Policy func(*http.Request) Policy
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		w.Header().Set("Allow", http.MethodConnect)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Ingress == nil {
		http.Error(w, "tunnel ingress unavailable", http.StatusInternalServerError)
		return
	}

	target, err := h.target(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT requires connection hijacking", http.StatusInternalServerError)
		return
	}
	conn, readWriter, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if err := readWriter.Writer.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	policy := Policy{}
	if h.Policy != nil {
		policy = h.Policy(r)
	}
	request := Request{
		Target: target,
		Policy: policy,
		Method: r.Method,
		Host:   r.Host,
		Header: r.Header.Clone(),
	}
	client := &hijackedClient{
		Conn:   conn,
		reader: readWriter.Reader,
		writer: readWriter.Writer,
	}
	_ = h.Ingress.Serve(r.Context(), client, request)
}

func (h *Handler) target(r *http.Request) (Target, error) {
	if h.Target != nil {
		return h.Target(r)
	}
	authority := r.Host
	if authority == "" && r.URL != nil {
		authority = r.URL.Host
	}
	if authority == "" {
		return Target{}, errors.New("netsec: CONNECT request has no authority")
	}
	target := NewTarget(authority)
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

type hijackedClient struct {
	net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	mu     sync.Mutex
}

func (c *hijackedClient) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *hijackedClient) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, err := c.writer.Write(p)
	if err != nil {
		return n, err
	}
	if err := c.writer.Flush(); err != nil {
		return n, err
	}
	return n, nil
}

func (c *hijackedClient) Close() error {
	c.mu.Lock()
	_ = c.writer.Flush()
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *hijackedClient) CloseWrite() error {
	if closer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return nil
}

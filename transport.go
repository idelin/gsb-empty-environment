package netsec

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

func newDoHClient(upstream *preparedUpstream, dialer Dialer) *http.Client {
	connectionHints := func(address string) []string {
		if len(upstream.upstream.Bootstrap) == 0 && upstream.upstream.Address == "" {
			return nil
		}
		defaultPort := "443"
		if _, port, err := net.SplitHostPort(address); err == nil && port != "" {
			defaultPort = port
		}
		hints := upstreamHints(upstream.upstream)
		targets := make([]string, 0, len(hints))
		for _, hint := range hints {
			targets = append(targets, normalizeAddress(hint, defaultPort))
		}
		return targets
	}
	transport := &http.Transport{
		Proxy:              nil,
		TLSClientConfig:    upstream.tlsConfig,
		DisableCompression: true,
		MaxIdleConns:       4,
		IdleConnTimeout:    90 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			hints := connectionHints(address)
			if len(hints) == 0 {
				return dialer.DialContext(ctx, network, address)
			}
			return dialHints(ctx, dialer, network, "443", hints)
		},
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			targets := connectionHints(address)
			if len(targets) == 0 {
				targets = []string{address}
			}
			return dialTLS(ctx, dialer, targets, upstream.tlsConfig)
		},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func dialHints(ctx context.Context, dialer Dialer, network, defaultPort string, hints []string) (net.Conn, error) {
	if len(hints) == 0 {
		return nil, errors.New("netsec: no connection hint")
	}
	var errs []error
	for _, hint := range hints {
		target := normalizeAddress(hint, defaultPort)
		conn, err := dialer.DialContext(ctx, network, target)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", target, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

func dialTLS(ctx context.Context, dialer Dialer, targets []string, config *tls.Config) (net.Conn, error) {
	var errs []error
	for _, target := range targets {
		raw, err := dialer.DialContext(ctx, "tcp", target)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", target, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		setDeadline(raw, ctx)
		conn := tls.Client(raw, config)
		if err := conn.HandshakeContext(ctx); err != nil {
			raw.Close()
			errs = append(errs, fmt.Errorf("%s: TLS handshake: %w", target, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		setDeadline(conn, ctx)
		return conn, nil
	}
	return nil, errors.Join(errs...)
}

func setDeadline(conn net.Conn, ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
}

func (s *Selector) exchangeDoH(ctx context.Context, upstream *preparedUpstream, query []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.doHURL.String(), bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	response, err := s.clients[upstream.index].Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("netsec: DoH server returned HTTP %d", response.StatusCode)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/dns-message") {
		return nil, fmt.Errorf("%w: got %q", ErrContentType, contentType)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, s.maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > s.maxResponseBytes {
		return nil, fmt.Errorf("netsec: DoH response exceeds %d bytes", s.maxResponseBytes)
	}
	if err := validateResponse(query, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Selector) exchangeDoT(ctx context.Context, upstream *preparedUpstream, query []byte) ([]byte, error) {
	conn, err := dialTLS(ctx, s.dialer, upstream.dotTargets, upstream.tlsConfig)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if len(query) > 65535 {
		return nil, fmt.Errorf("netsec: DNS query exceeds TCP length limit")
	}
	writer := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(writer[0:2], uint16(len(query)))
	copy(writer[2:], query)
	if _, err := conn.Write(writer); err != nil {
		return nil, err
	}
	var lengthBytes [2]byte
	if _, err := io.ReadFull(conn, lengthBytes[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(lengthBytes[:]))
	if length == 0 {
		return nil, fmt.Errorf("%w: empty DoT response", ErrMalformedMessage)
	}
	if int64(length) > s.maxResponseBytes {
		return nil, fmt.Errorf("netsec: DoT response exceeds %d bytes", s.maxResponseBytes)
	}
	response := make([]byte, length)
	if _, err := io.ReadFull(conn, response); err != nil {
		return nil, err
	}
	if err := validateResponse(query, response); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Selector) exchangePlain(ctx context.Context, upstream *preparedUpstream, query []byte) ([]byte, error) {
	conn, err := s.dialer.DialContext(ctx, "udp", upstream.plainTarget)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	setDeadline(conn, ctx)
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	limit := s.maxResponseBytes
	if limit > 65535 {
		limit = 65535
	}
	response := make([]byte, limit)
	n, err := conn.Read(response)
	if err != nil {
		return nil, err
	}
	response = response[:n]
	if err := validateResponse(query, response); err != nil {
		return nil, err
	}
	return response, nil
}

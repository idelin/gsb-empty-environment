package netsec

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Transport is an http.RoundTripper that adds Basic authentication from a
// netrc file. It never mutates the request passed to RoundTrip.
type Transport struct {
	netrc     *Netrc
	base      http.RoundTripper
	allowHTTP bool
}

// RoundTripper is an alias for Transport.
type RoundTripper = Transport

// NewRoundTripper constructs a Transport from a netrc source. Arguments may
// be supplied in any order:
//
//   - one netrc source: a file path string, an io.Reader, a []byte, or *Netrc
//   - an optional http.RoundTripper (defaults to http.DefaultTransport)
//   - an optional bool enabling credentials over plaintext HTTP (default false)
//
// For example:
//
//	rt, err := netsec.NewRoundTripper("path/to/netrc", http.DefaultTransport, true)
func NewRoundTripper(args ...any) (*Transport, error) {
	var (
		netrc     *Netrc
		base      http.RoundTripper
		allowHTTP bool
		haveBase  bool
		haveBool  bool
	)

	for _, arg := range args {
		switch value := arg.(type) {
		case *Netrc:
			if netrc != nil {
				return nil, fmt.Errorf("netsec: multiple netrc sources")
			}
			netrc = value
		case string:
			if netrc != nil {
				return nil, fmt.Errorf("netsec: multiple netrc sources")
			}
			parsed, err := ParseNetrcFile(value)
			if err != nil {
				return nil, err
			}
			netrc = parsed
		case io.Reader:
			if netrc != nil {
				return nil, fmt.Errorf("netsec: multiple netrc sources")
			}
			parsed, err := ParseNetrc(value)
			if err != nil {
				return nil, err
			}
			netrc = parsed
		case []byte:
			if netrc != nil {
				return nil, fmt.Errorf("netsec: multiple netrc sources")
			}
			parsed, err := parseNetrcBytes(value)
			if err != nil {
				return nil, err
			}
			netrc = parsed
		case http.RoundTripper:
			if haveBase {
				return nil, fmt.Errorf("netsec: multiple base transports")
			}
			base = value
			haveBase = true
		case bool:
			if haveBool {
				return nil, fmt.Errorf("netsec: multiple plaintext HTTP options")
			}
			allowHTTP = value
			haveBool = true
		case nil:
			// Ignore nil optional arguments.
		default:
			return nil, fmt.Errorf("netsec: unsupported NewRoundTripper argument %T", arg)
		}
	}

	if netrc == nil {
		return nil, fmt.Errorf("netsec: no netrc source supplied")
	}
	if !haveBase {
		base = http.DefaultTransport
	}

	return &Transport{
		netrc:     netrc,
		base:      base,
		allowHTTP: allowHTTP && haveBool,
	}, nil
}

// NewTransport is an alias for NewRoundTripper.
func NewTransport(args ...any) (*Transport, error) {
	return NewRoundTripper(args...)
}

// New is an alias for NewRoundTripper.
func New(args ...any) (*Transport, error) {
	return NewRoundTripper(args...)
}

// MustNewRoundTripper is like NewRoundTripper but panics on configuration
// errors.
func MustNewRoundTripper(args ...any) *Transport {
	transport, err := NewRoundTripper(args...)
	if err != nil {
		panic(err)
	}
	return transport
}

// NewRoundTripperFromFile constructs a Transport from a netrc file.
func NewRoundTripperFromFile(path string, base http.RoundTripper, allowHTTP bool) (*Transport, error) {
	netrc, err := ParseNetrcFile(path)
	if err != nil {
		return nil, err
	}
	return NewRoundTripperFromNetrc(netrc, base, allowHTTP), nil
}

// NewRoundTripperFromReader constructs a Transport from netrc data supplied
// by r.
func NewRoundTripperFromReader(r io.Reader, base http.RoundTripper, allowHTTP bool) (*Transport, error) {
	netrc, err := ParseNetrc(r)
	if err != nil {
		return nil, err
	}
	return NewRoundTripperFromNetrc(netrc, base, allowHTTP), nil
}

// NewRoundTripperFromString constructs a Transport from in-memory netrc data.
func NewRoundTripperFromString(data string, base http.RoundTripper, allowHTTP bool) (*Transport, error) {
	netrc, err := ParseNetrcString(data)
	if err != nil {
		return nil, err
	}
	return NewRoundTripperFromNetrc(netrc, base, allowHTTP), nil
}

// NewRoundTripperFromNetrc constructs a Transport from an already parsed
// netrc.
func NewRoundTripperFromNetrc(netrc *Netrc, base http.RoundTripper, allowHTTP bool) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{netrc: netrc, base: base, allowHTTP: allowHTTP}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := http.DefaultTransport
	if t != nil && t.base != nil {
		base = t.base
	}
	if req == nil {
		return nil, fmt.Errorf("netsec: nil request")
	}
	if t == nil || t.netrc == nil {
		return base.RoundTrip(req)
	}

	if !t.canAttach(req) || requestHasAuthorization(req) {
		return base.RoundTrip(req)
	}

	credential, ok := t.credentialFor(req)
	if !ok {
		return base.RoundTrip(req)
	}

	outgoing := req.Clone(req.Context())
	if outgoing.Header == nil {
		outgoing.Header = make(http.Header)
	}
	outgoing.SetBasicAuth(credential.Login, credential.Password)
	return base.RoundTrip(outgoing)
}

func (t *Transport) canAttach(req *http.Request) bool {
	if req.URL == nil {
		return false
	}
	switch {
	case strings.EqualFold(req.URL.Scheme, "https"):
		return true
	case strings.EqualFold(req.URL.Scheme, "http"):
		return t.allowHTTP
	default:
		return false
	}
}

func (t *Transport) credentialFor(req *http.Request) (Credential, bool) {
	host, err := normalizeHost(req.URL.Hostname())
	if err != nil {
		return Credential{}, false
	}
	if credential, ok := t.netrc.machines[host]; ok {
		return credential, true
	}

	// A default entry is only eligible for the first request. Requests made
	// while following a redirect carry a non-nil Response field, so a
	// redirect to another host (or back to the original host) never inherits
	// the default credentials.
	if req.Response == nil {
		return t.netrc.Default()
	}
	return Credential{}, false
}

func requestHasAuthorization(req *http.Request) bool {
	for key := range req.Header {
		if strings.EqualFold(key, "Authorization") {
			return true
		}
	}
	return false
}

package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// URI is a parsed SIP/SIPS URI.
type URI struct {
	Scheme string
	User   string
	Host   string
	Port   int
	Params map[string]string
}

// ParseURI parses a SIP URI, tolerating angle brackets and display names.
func ParseURI(s string) (URI, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<"); i >= 0 {
		j := strings.Index(s, ">")
		if j < 0 {
			return URI{}, fmt.Errorf("netsec: malformed URI %q", s)
		}
		s = s[i+1 : j]
	}
	u := URI{Params: map[string]string{}}
	rest := s
	if i := strings.Index(rest, ":"); i >= 0 {
		u.Scheme = strings.ToLower(rest[:i])
		rest = rest[i+1:]
	} else {
		return URI{}, fmt.Errorf("netsec: URI missing scheme %q", s)
	}
	if u.Scheme != "sip" && u.Scheme != "sips" {
		return URI{}, fmt.Errorf("netsec: unsupported scheme %q", u.Scheme)
	}
	if i := strings.Index(rest, ";"); i >= 0 {
		for _, p := range strings.Split(rest[i+1:], ";") {
			if p == "" {
				continue
			}
			kv := strings.SplitN(p, "=", 2)
			if len(kv) == 2 {
				u.Params[strings.ToLower(kv[0])] = kv[1]
			} else {
				u.Params[strings.ToLower(kv[0])] = ""
			}
		}
		rest = rest[:i]
	}
	if i := strings.Index(rest, "@"); i >= 0 {
		u.User = rest[:i]
		rest = rest[i+1:]
	}
	host, port := rest, 0
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		if p, err := strconv.Atoi(rest[i+1:]); err == nil {
			host, port = rest[:i], p
		}
	}
	u.Host = strings.ToLower(strings.Trim(host, "[]"))
	u.Port = port
	if u.Host == "" {
		return URI{}, fmt.Errorf("netsec: URI missing host %q", s)
	}
	return u, nil
}

func (u URI) String() string {
	var b strings.Builder
	b.WriteString(u.Scheme + ":")
	if u.User != "" {
		b.WriteString(u.User + "@")
	}
	b.WriteString(u.Host)
	if u.Port != 0 {
		fmt.Fprintf(&b, ":%d", u.Port)
	}
	for k, v := range u.Params {
		b.WriteString(";" + k)
		if v != "" {
			b.WriteString("=" + v)
		}
	}
	return b.String()
}

// Via is one parsed Via header entry (one hop).
type Via struct {
	Transport  string // UDP, TCP, TLS
	SentByHost string
	SentByPort int
	Branch     string
	Received   string // must only ever be set by this proxy
	RPort      int
	HasRPort   bool
}

// BranchMagicCookie is the RFC 3261 branch magic cookie.
const BranchMagicCookie = "z9hG4bK"

// ParseVia parses a single Via header value such as
// "SIP/2.0/UDP client.example.com:5060;branch=z9hG4bK776;received=10.0.0.1".
func ParseVia(s string) (Via, error) {
	s = strings.TrimSpace(s)
	var v Via
	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return v, fmt.Errorf("netsec: malformed Via %q", s)
	}
	proto := strings.ToUpper(parts[0])
	const prefix = "SIP/2.0/"
	if !strings.HasPrefix(proto, prefix) {
		return v, fmt.Errorf("netsec: unsupported Via protocol %q", proto)
	}
	v.Transport = strings.TrimPrefix(proto, prefix)

	fields := strings.Split(parts[1], ";")
	hostport := strings.TrimSpace(fields[0])
	host, port := hostport, 0
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		if p, err := strconv.Atoi(hostport[i+1:]); err == nil {
			host, port = hostport[:i], p
		}
	}
	v.SentByHost = strings.ToLower(strings.Trim(host, "[]"))
	v.SentByPort = port
	if v.SentByHost == "" {
		return v, fmt.Errorf("netsec: Via missing sent-by host %q", s)
	}
	for _, f := range fields[1:] {
		kv := strings.SplitN(strings.TrimSpace(f), "=", 2)
		key := strings.ToLower(kv[0])
		val := ""
		if len(kv) == 2 {
			val = kv[1]
		}
		switch key {
		case "branch":
			v.Branch = val
		case "received":
			v.Received = strings.ToLower(val)
		case "rport":
			v.HasRPort = true
			if p, err := strconv.Atoi(val); err == nil {
				v.RPort = p
			}
		}
	}
	return v, nil
}

func (v Via) sentByPortOrDefault() int {
	if v.SentByPort != 0 {
		return v.SentByPort
	}
	return 5060
}

// Message is the subset of a SIP message the validator operates on.
// SrcAddr/SrcPort are the transport-level peer observed by the proxy and
// must be filled in by the caller from the connection, never from headers.
type Message struct {
	IsResponse   bool
	Method       string // empty for responses
	StatusCode   int
	RequestURI   URI
	Vias         []Via // topmost (nearest hop) first
	Routes       []URI // Route header, topmost first
	RecordRoutes []URI // Record-Route header as received (untrusted)
	CallID       string
	FromTag      string
	ToTag        string
	CSeqNum      uint32
	CSeqMethod   string
	SrcAddr      string
	SrcPort      int
}

func (m *Message) topVia() (Via, error) {
	if len(m.Vias) == 0 {
		return Via{}, fmt.Errorf("netsec: message has no Via")
	}
	return m.Vias[0], nil
}

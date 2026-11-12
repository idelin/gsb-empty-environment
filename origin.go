package netsec

import (
	"net"
	"net/url"
	"strings"
)

// Origin is the canonical identity of an HTTP endpoint: the triple
// scheme, host and port, with scheme and host lowercased and default
// ports normalized away. It deliberately excludes userinfo, path,
// query and fragment, none of which are part of an origin.
type Origin struct {
	Scheme string
	Host   string
	Port   string
}

// CanonicalOrigin parses rawURL and returns its normalized origin.
// Only http and https are accepted. URL userinfo is ignored entirely:
// a redirect target's userinfo is server-controlled data and must
// never be treated as a credential.
func CanonicalOrigin(rawURL string) (Origin, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Origin{}, err
	}
	return originFromURL(u)
}

func originFromURL(u *url.URL) (Origin, error) {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return Origin{}, &RedirectError{Reason: "unsupported scheme in redirect target: " + u.Scheme}
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return Origin{}, &RedirectError{Reason: "redirect target has no host"}
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}

	port := u.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	return Origin{Scheme: scheme, Host: host, Port: port}, nil
}

// String renders the origin in the canonical "scheme://host:port" form.
func (o Origin) String() string {
	return o.Scheme + "://" + net.JoinHostPort(o.Host, o.Port)
}

func (o Origin) key() string {
	return o.String()
}

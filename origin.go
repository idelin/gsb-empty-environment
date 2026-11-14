package netsec

import (
	"net/http"
	"net/url"
	"strings"
)

type originValue struct {
	scheme string
	host   string
	name   string
}

func normalizeOrigin(raw string) (originValue, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return originValue{}, false
	}
	if strings.ContainsAny(raw, " \t\r\n,") {
		return originValue{}, false
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return originValue{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return originValue{}, false
	}
	if u.Path != "" && u.Path != "/" {
		return originValue{}, false
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return originValue{}, false
	}

	host := canonicalHost(u.Host, scheme)
	if host == "" {
		return originValue{}, false
	}
	return originValue{
		scheme: scheme,
		host:   host,
		name:   scheme + "://" + host,
	}, true
}

func originFromReferer(raw string) (originValue, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return originValue{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return originValue{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return originValue{}, false
	}

	host := canonicalHost(u.Host, scheme)
	if host == "" {
		return originValue{}, false
	}
	return originValue{
		scheme: scheme,
		host:   host,
		name:   scheme + "://" + host,
	}, true
}

func canonicalHost(host, scheme string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	switch scheme {
	case "http":
		host = strings.TrimSuffix(host, ":80")
	case "https":
		host = strings.TrimSuffix(host, ":443")
	}
	return host
}

func requestOrigin(r *http.Request) originValue {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.URL.Scheme, "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	host = canonicalHost(host, scheme)
	return originValue{
		scheme: scheme,
		host:   host,
		name:   scheme + "://" + host,
	}
}

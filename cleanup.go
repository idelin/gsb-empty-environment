package netsec

import (
	"net/http"
	"net/textproto"
	"strings"
)

var hopByHopHeaderNames = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func cleanRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if connectionNamesIdentityHeader(r.Header) {
			clearIdentityHeaders(w.Header())
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		request := r.Clone(r.Context())
		request.Header = r.Header.Clone()
		sanitizeHeaders(request.Header)

		// Trailer is classified as hop-by-hop here. Clear the trailer map too,
		// otherwise net/http could regenerate a Trailer header on the outbound
		// request even after the header itself was removed.
		request.Trailer = nil

		next.ServeHTTP(w, request)
	})
}

func sanitizeHeaders(header http.Header) {
	connectionValues := headerValues(header, "Connection")
	for _, value := range connectionValues {
		for _, name := range strings.Split(value, ",") {
			name = textproto.TrimString(name)
			if name != "" {
				deleteHeader(header, name)
			}
		}
	}

	for _, name := range hopByHopHeaderNames {
		deleteHeader(header, name)
	}
	clearIdentityHeaders(header)
}

func connectionNamesIdentityHeader(header http.Header) bool {
	for _, value := range headerValues(header, "Connection") {
		for _, name := range strings.Split(value, ",") {
			name = textproto.TrimString(name)
			if strings.EqualFold(name, HeaderAuthUser) || strings.EqualFold(name, HeaderAuthGroups) {
				return true
			}
		}
	}
	return false
}

func headerValues(header http.Header, name string) []string {
	var values []string
	for key, current := range header {
		if strings.EqualFold(key, name) {
			values = append(values, current...)
		}
	}
	return values
}

func deleteHeader(header http.Header, name string) {
	for key := range header {
		if strings.EqualFold(key, name) {
			delete(header, key)
		}
	}
}

package netsec

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ServiceValidatePath is the conventional CAS 2.0 validation endpoint.
const ServiceValidatePath = "/serviceValidate"

const casXMLHeader = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
const casXMLNamespace = `http://www.yale.edu/tp/cas`

// ServiceValidateHandler implements a CAS 2.0 /serviceValidate endpoint.
// Validation consumes the ticket exactly once.
type ServiceValidateHandler struct {
	// Store is the ticket repository used by the handler.
	Store *TicketStore
}

// NewServiceValidateHandler returns a handler bound to store.
func NewServiceValidateHandler(store *TicketStore) *ServiceValidateHandler {
	return &ServiceValidateHandler{Store: store}
}

// ServiceValidateHandlerFunc adapts a store to an http.HandlerFunc.
func ServiceValidateHandlerFunc(store *TicketStore) http.HandlerFunc {
	handler := NewServiceValidateHandler(store)
	return handler.ServeHTTP
}

// ServiceValidate validates a ticket and writes a CAS XML response. It is a
// convenience entry point for routers that do not use the handler type.
func ServiceValidate(store *TicketStore, w http.ResponseWriter, r *http.Request) {
	NewServiceValidateHandler(store).ServeHTTP(w, r)
}

func (h *ServiceValidateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")

	if h.Store == nil {
		writeCASFailure(w, CodeInternalError, "ticket store is not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeCASFailure(w, CodeInvalidRequest, "malformed request")
		return
	}

	ticket := r.FormValue("ticket")
	service := r.FormValue("service")
	if ticket == "" || service == "" {
		writeCASFailure(w, CodeInvalidRequest, "ticket and service are required")
		return
	}

	renew := false
	if rawRenew := r.FormValue("renew"); rawRenew != "" {
		value, err := strconv.ParseBool(rawRenew)
		if err != nil {
			writeCASFailure(w, CodeInvalidRequest, "renew must be true or false")
			return
		}
		renew = value
	}

	user, err := h.Store.ValidateServiceTicket(ticket, service, renew)
	if err != nil {
		writeCASFailure(w, casErrorCode(err), err.Error())
		return
	}

	fmt.Fprintf(w, "%s<cas:serviceResponse xmlns:cas=\"%s\">\n", casXMLHeader, xmlEscape(casXMLNamespace))
	fmt.Fprintf(w, "  <cas:authenticationSuccess>\n    <cas:user>%s</cas:user>\n  </cas:authenticationSuccess>\n", xmlEscape(user))
	fmt.Fprintln(w, "</cas:serviceResponse>")
}

func casErrorCode(err error) string {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Code
	}
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return CodeInvalidRequest
	case errors.Is(err, ErrInvalidService):
		return CodeInvalidService
	case errors.Is(err, ErrServiceMismatch):
		return CodeInvalidService
	case errors.Is(err, ErrTicketNotFound),
		errors.Is(err, ErrTicketUsed),
		errors.Is(err, ErrTicketExpired),
		errors.Is(err, ErrRenewRequired),
		errors.Is(err, ErrProxyTicket),
		errors.Is(err, ErrServiceTicket):
		return CodeInvalidTicket
	default:
		return CodeInternalError
	}
}

func writeCASFailure(w http.ResponseWriter, code, reason string) {
	fmt.Fprintf(w, "%s<cas:serviceResponse xmlns:cas=\"%s\">\n", casXMLHeader, xmlEscape(casXMLNamespace))
	fmt.Fprintf(w, "  <cas:authenticationFailure code=\"%s\">%s</cas:authenticationFailure>\n", xmlEscape(code), xmlEscape(reason))
	fmt.Fprintln(w, "</cas:serviceResponse>")
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

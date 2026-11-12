package netsec

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestServiceValidateHandlerConsumesTicket(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ticket, err := store.IssueServiceTicket("alice&bob", service, true)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	handler := NewServiceValidateHandler(store)

	request := httptest.NewRequest(http.MethodGet, ServiceValidatePath+"?"+url.Values{
		"ticket":  {ticket},
		"service": {service},
		"renew":   {"true"},
	}.Encode(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "<cas:authenticationSuccess>") {
		t.Fatalf("success response missing: %s", body)
	}
	if !strings.Contains(body, "alice&amp;bob") {
		t.Fatalf("user was not XML-escaped: %s", body)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request.Clone(request.Context()))
	body = response.Body.String()
	if !strings.Contains(body, `code="INVALID_TICKET"`) {
		t.Fatalf("replay response missing INVALID_TICKET: %s", body)
	}
}

func TestServiceValidateHandlerWrongServiceKeepsTicket(t *testing.T) {
	store, _, _ := newFakeStore(t)
	const service = "https://app.example.com/callback"

	ticket, err := store.IssueServiceTicket("alice", service, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	handler := NewServiceValidateHandler(store)

	badRequest := httptest.NewRequest(http.MethodGet, ServiceValidatePath+"?"+url.Values{
		"ticket":  {ticket},
		"service": {"https://evil.example.com/callback"},
	}.Encode(), nil)
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, badRequest)
	if !strings.Contains(badResponse.Body.String(), `code="INVALID_SERVICE"`) {
		t.Fatalf("wrong service response: %s", badResponse.Body.String())
	}

	goodRequest := httptest.NewRequest(http.MethodGet, ServiceValidatePath+"?"+url.Values{
		"ticket":  {ticket},
		"service": {service},
	}.Encode(), nil)
	goodResponse := httptest.NewRecorder()
	handler.ServeHTTP(goodResponse, goodRequest)
	if !strings.Contains(goodResponse.Body.String(), "<cas:authenticationSuccess>") {
		t.Fatalf("ticket was consumed by failed service validation: %s", goodResponse.Body.String())
	}
}

func TestServiceValidateHandlerRejectsMissingParameters(t *testing.T) {
	handler := NewServiceValidateHandler(NewTicketStore())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, ServiceValidatePath, nil))
	if !strings.Contains(response.Body.String(), `code="INVALID_REQUEST"`) {
		t.Fatalf("missing-parameter response: %s", response.Body.String())
	}
}

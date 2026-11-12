package netsec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAllowsAndEchoesOriginalUID(t *testing.T) {
	uid := "uid-with-\"quotes\"-<&>"
	review := validReviewBody(t, uid, "admission.k8s.io/v1", "AdmissionReview")

	handler := NewHandler(func(ctx context.Context, request *AdmissionRequest) error {
		if request.UID != uid {
			t.Errorf("evaluator got uid %q, want %q", request.UID, uid)
		}
		return nil
	}, 100*time.Millisecond)

	recorder := serve(t, handler, review)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	response := decodeResponse(t, recorder)
	if !response.Allowed {
		t.Fatal("allowed = false, want true")
	}
	if response.UID != uid {
		t.Fatalf("response uid = %q, want original uid %q", response.UID, uid)
	}
}

func TestEvaluationErrorDenies(t *testing.T) {
	uid := "uid-error"
	handler := NewHandler(func(context.Context, *AdmissionReview) error {
		return errors.New("rule engine failed")
	}, 100*time.Millisecond)

	assertDenied(t, handler, validReviewBody(t, uid, "admission.k8s.io/v1", "AdmissionReview"), uid)
}

func TestPanicDenies(t *testing.T) {
	uid := "uid-panic"
	handler := NewHandler(func(context.Context, *AdmissionRequest) error {
		panic("rule engine panic")
	}, 100*time.Millisecond)

	assertDenied(t, handler, validReviewBody(t, uid, "admission.k8s.io/v1", "AdmissionReview"), uid)
}

func TestTimeoutDeniesAndLateResultIsIgnored(t *testing.T) {
	uid := "uid-timeout"
	release := make(chan struct{})
	finished := make(chan struct{})

	handler := NewHandler(func(context.Context, *AdmissionRequest) error {
		<-release
		close(finished)
		return nil
	}, 100*time.Millisecond)

	request := httptest.NewRequest(http.MethodPost, "/admit", bytes.NewReader(
		validReviewBody(t, uid, "admission.k8s.io/v1", "AdmissionReview")))
	recorder := httptest.NewRecorder()

	started := time.Now()
	handler.ServeHTTP(recorder, request)
	elapsed := time.Since(started)

	if elapsed < 80*time.Millisecond {
		t.Fatalf("handler returned after %s, before the deadline", elapsed)
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	response := decodeResponse(t, recorder)
	if response.Allowed {
		t.Fatal("allowed = true after timeout, want false")
	}
	if response.UID != uid {
		t.Fatalf("response uid = %q, want %q", response.UID, uid)
	}

	bodyAfterDeadline := recorder.Body.String()
	close(release)
	<-finished
	time.Sleep(25 * time.Millisecond)

	if got := recorder.Body.String(); got != bodyAfterDeadline {
		t.Fatalf("late evaluator completion rewrote response:\nold: %s\nnew: %s", bodyAfterDeadline, got)
	}
}

func TestUnknownKindDeniesBeforeEvaluation(t *testing.T) {
	var called atomic.Bool
	handler := NewHandler(func(context.Context, *AdmissionReview) error {
		called.Store(true)
		return nil
	}, 100*time.Millisecond)

	uid := "uid-kind"
	assertDenied(t, handler, validReviewBody(t, uid, "admission.k8s.io/v1", "Pod"), uid)
	if called.Load() {
		t.Fatal("evaluator was called for an unknown kind")
	}
}

func TestUnknownAPIVersionDenies(t *testing.T) {
	uid := "uid-api"
	handler := NewHandler(func(context.Context, *AdmissionReview) error {
		return nil
	}, 100*time.Millisecond)

	assertDenied(t, handler, validReviewBody(t, uid, "example.org/v9", "AdmissionReview"), uid)
}

func TestOversizedBodyDeniesAndEchoesUID(t *testing.T) {
	uid := "uid-large"
	var called atomic.Bool
	handler := NewHandler(func(context.Context, *AdmissionReview) error {
		called.Store(true)
		return nil
	}, 100*time.Millisecond)

	body := fmt.Sprintf(
		`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","request":{"uid":%q,"kind":{"kind":"Pod"},"padding":%q}}`,
		uid, strings.Repeat("a", int(MaxAdmissionReviewBytes)))
	if int64(len(body)) <= MaxAdmissionReviewBytes {
		t.Fatalf("generated body is only %d bytes", len(body))
	}

	assertDenied(t, handler, []byte(body), uid)
	if called.Load() {
		t.Fatal("evaluator was called for an oversized body")
	}
}

func TestBodyAtLimitIsAccepted(t *testing.T) {
	uid := "uid-boundary"
	base := validReviewBody(t, uid, "admission.k8s.io/v1", "AdmissionReview")
	body := base
	if int64(len(body)) < MaxAdmissionReviewBytes {
		body = append(body, bytes.Repeat([]byte(" "), int(MaxAdmissionReviewBytes)-len(body))...)
	}
	if int64(len(body)) != MaxAdmissionReviewBytes {
		t.Fatalf("body size = %d, want %d", len(body), MaxAdmissionReviewBytes)
	}

	handler := NewHandler(func(context.Context, *AdmissionReview) error {
		return nil
	}, 100*time.Millisecond)

	recorder := serve(t, handler, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if response := decodeResponse(t, recorder); response.UID != uid {
		t.Fatalf("response uid = %q, want %q", response.UID, uid)
	}
}

func TestMalformedBodyStillEchoesExtractableUID(t *testing.T) {
	uid := "uid-malformed"
	body := []byte(fmt.Sprintf(
		`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","request":{"uid":%q,"object":`, uid))

	handler := NewHandler(func(context.Context, *AdmissionReview) error {
		return nil
	}, 100*time.Millisecond)

	assertDenied(t, handler, body, uid)
}

func TestExplicitDenialUsesForbiddenAndKeepsMessage(t *testing.T) {
	uid := "uid-deny"
	handler := NewHandler(func(context.Context, *AdmissionReview) (*AdmissionResponse, error) {
		return &AdmissionResponse{
			UID:     "tampered-uid",
			Allowed: false,
			Status:  &Status{Message: "policy denied"},
		}, nil
	}, 100*time.Millisecond)

	recorder := serve(t, handler, validReviewBody(t, uid, "admission.k8s.io/v1", "AdmissionReview"))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	response := decodeResponse(t, recorder)
	if response.Allowed {
		t.Fatal("allowed = true, want false")
	}
	if response.UID != uid {
		t.Fatalf("response uid = %q, want original uid %q", response.UID, uid)
	}
	if response.Status == nil || response.Status.Message != "policy denied" {
		t.Fatalf("status message = %#v, want policy denial", response.Status)
	}
}

func validReviewBody(t *testing.T, uid, apiVersion, kind string) []byte {
	t.Helper()
	return []byte(fmt.Sprintf(
		`{"apiVersion":%q,"kind":%q,"request":{"uid":%q,"kind":{"group":"","version":"v1","kind":"Pod"},"object":{"metadata":{"name":"demo"}}}}`,
		apiVersion, kind, uid))
}

func serve(t *testing.T, handler http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/admit", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func assertDenied(t *testing.T, handler http.Handler, body []byte, uid string) {
	t.Helper()
	recorder := serve(t, handler, body)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", recorder.Code, recorder.Body.String())
	}
	response := decodeResponse(t, recorder)
	if response.Allowed {
		t.Fatalf("allowed = true, want false; body: %s", recorder.Body.String())
	}
	if response.UID != uid {
		t.Fatalf("response uid = %q, want %q; body: %s", response.UID, uid, recorder.Body.String())
	}
}

func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder) *AdmissionResponse {
	t.Helper()
	var review AdmissionReview
	if err := json.Unmarshal(recorder.Body.Bytes(), &review); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, recorder.Body.String())
	}
	if review.APIVersion == "" || review.Kind != "AdmissionReview" {
		t.Fatalf("unexpected response envelope: %#v", review)
	}
	if review.Response == nil {
		t.Fatal("response is missing")
	}
	return review.Response
}

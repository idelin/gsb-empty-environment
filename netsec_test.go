package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	secretPSK     = "super-secret-psk-9f3a21"
	secretToken   = "tok_live_842739abcd"
	secretClient  = "client-identity-7781"
	internalStage = stageAuthorization
)

func sensitiveFields() []SensitiveField {
	return []SensitiveField{
		NewSensitiveField("psk", secretPSK),
		NewSensitiveField("session_token", secretToken),
		NewSensitiveField("client_id", secretClient),
	}
}

// secretSubstrings must never appear on ANY surface, including the
// redacted local log: error text, formatting, JSON, wire frames,
// callbacks, and log records.
func secretSubstrings() []string {
	return []string{
		secretPSK, secretToken, secretClient,
	}
}

// internalSubstrings may appear only in redacted local logs, never on a
// host-visible surface.
func internalSubstrings() []string {
	return []string{
		string(internalStage),
		"authorization", "pre-shared", "token-binding",
	}
}

func assertNoSecretLeak(t *testing.T, label string, surfaces ...string) {
	t.Helper()
	for _, surface := range surfaces {
		for _, banned := range secretSubstrings() {
			if strings.Contains(surface, banned) {
				t.Fatalf("%s leaks %q: %q", label, banned, surface)
			}
		}
	}
}

func assertNoInternalLeak(t *testing.T, label string, surfaces ...string) {
	t.Helper()
	for _, surface := range surfaces {
		for _, banned := range internalSubstrings() {
			if strings.Contains(surface, banned) {
				t.Fatalf("%s leaks internal detail %q: %q", label, banned, surface)
			}
		}
	}
}

func renderLogEntry(entry LogEntry) string {
	return fmt.Sprintf("%v", entry)
}

func renderLogMap(m map[string]any) string {
	return fmt.Sprintf("%v", m)
}

func TestPublicMappingAndFixedShape(t *testing.T) {
	cases := []struct {
		stage internalCategory
		want  PublicCategory
	}{
		{stageVersionNegotiation, PublicHandshakeFailed},
		{stageCipherSuites, PublicHandshakeFailed},
		{stageCertificate, PublicHandshakeFailed},
		{stageKeyExchange, PublicHandshakeFailed},
		{stageClientIdentity, PublicAuthFailed},
		{stagePreSharedKey, PublicAuthFailed},
		{stageTokenBinding, PublicAuthFailed},
		{stageAuthorization, PublicAuthFailed},
		{stageRateLimited, PublicUnavailable},
		{stageBackendUnavailable, PublicUnavailable},
		{stagePanicRecovered, PublicInternal},
		{stageUnknown, PublicInternal},
		{internalCategory("stage:not-registered"), PublicInternal},
	}

	var wireLenSeen, jsonLenSeen, errLenSeen int

	for _, tc := range cases {
		err := NewInternalError(string(tc.stage), "detail must stay internal",
			errors.New("root cause detail"), sensitiveFields()...)
		pub := Publicize(err)

		if pub.Category != tc.want {
			t.Fatalf("stage %s: got category %s, want %s", tc.stage, pub.Category, tc.want)
		}
		if len(pub.CorrelationID) != correlationIDLen {
			t.Fatalf("stage %s: correlation id length %d", tc.stage, len(pub.CorrelationID))
		}

		frame, err := pub.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText: %v", err)
		}
		if len(frame) != wireLen {
			t.Fatalf("stage %s: wire length %d, want %d", tc.stage, len(frame), wireLen)
		}
		if string(frame[0]) != wirePrefix {
			t.Fatalf("stage %s: missing wire prefix", tc.stage)
		}

		rawJSON, err := pub.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}

		// All categories share identical field widths, so every rendering
		// keeps the same length regardless of the internal stage.
		if wireLenSeen == 0 {
			wireLenSeen, jsonLenSeen, errLenSeen = len(frame), len(rawJSON), len(pub.Error())
		} else if len(frame) != wireLenSeen || len(rawJSON) != jsonLenSeen || len(pub.Error()) != errLenSeen {
			t.Fatalf("stage %s: rendering length diverged (wire %d/%d json %d/%d text %d/%d)",
				tc.stage, len(frame), wireLenSeen, len(rawJSON), jsonLenSeen, len(pub.Error()), errLenSeen)
		}

		var decoded map[string]string
		if err := json.Unmarshal(rawJSON, &decoded); err != nil {
			t.Fatalf("invalid JSON %q: %v", rawJSON, err)
		}
		if decoded["code"] != string(tc.want) || decoded["ref"] != pub.CorrelationID {
			t.Fatalf("stage %s: unexpected JSON %v", tc.stage, decoded)
		}

		if !errors.Is(pub, publicSentinel(tc.want)) {
			t.Fatalf("stage %s: errors.Is sentinel mismatch", tc.stage)
		}

		if got := Publicize(errors.New("ad hoc failure with detail")).Category; got != PublicInternal {
			t.Fatalf("ad hoc error mapped to %s, want %s", got, PublicInternal)
		}
	}
}

func TestFormattingConvergesForEveryVerb(t *testing.T) {
	detailed := NewInternalError(string(internalStage), "detail must stay internal",
		errors.New("root cause detail"), sensitiveFields()...)
	pub := Publicize(detailed)

	internalSurfaces := []string{
		detailed.Error(),
		fmt.Sprintf("%s", detailed),
		fmt.Sprintf("%v", detailed),
		fmt.Sprintf("%+v", detailed),
		fmt.Sprintf("%#v", detailed),
		fmt.Sprintf("%200s", detailed),
		fmt.Sprintf("%.5v", detailed),
	}
	for _, surface := range internalSurfaces {
		if surface != "netsec: internal failure" {
			t.Fatalf("internal error rendered non-fixed output: %q", surface)
		}
	}

	wantText := pub.Error()
	publicSurfaces := []string{
		pub.Error(),
		pub.String(),
		fmt.Sprintf("%s", pub),
		fmt.Sprintf("%v", pub),
		fmt.Sprintf("%+v", pub),
		fmt.Sprintf("%#v", pub),
		fmt.Sprintf("%200v", pub),
	}
	for _, surface := range publicSurfaces {
		if surface != wantText {
			t.Fatalf("public error formatting diverged: %q vs %q", surface, wantText)
		}
	}
}

func TestNoSensitiveLeakAcrossAllSurfaces(t *testing.T) {
	detailed := NewInternalError(string(internalStage), "detail must stay internal",
		errors.New("root cause detail"), sensitiveFields()...).(*internalError)

	transport := &countingTransport{}
	logger := &recordingLogger{}
	callback := &countingCallback{}
	guard := NewGuard(transport, WithLogger(logger), WithFailureCallback(callback.callback))

	pub, err := guard.Report(detailed)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	frame, _ := pub.MarshalText()
	rawJSON, _ := pub.MarshalJSON()
	snapshot := transport.snapshot()
	entry := logger.single()

	publicSurfaces := []string{
		detailed.Error(),
		fmt.Sprintf("%+v", detailed),
		fmt.Sprintf("%#v", detailed),
		pub.Error(),
		fmt.Sprintf("%+v", pub),
		fmt.Sprintf("%#v", pub),
		string(frame),
		string(rawJSON),
		string(snapshot.payload),
		callback.single().Error(),
		fmt.Sprintf("%+v", callback.single()),
	}
	logSurfaces := []string{
		renderLogEntry(entry),
		renderLogMap(detailed.redactedLog()),
	}
	// Host-visible surfaces reveal neither secrets nor internal stage names.
	assertNoSecretLeak(t, "boundary", publicSurfaces...)
	assertNoInternalLeak(t, "boundary", publicSurfaces...)
	// Local logs retain stage names for diagnostics but must still never
	// carry secret values.
	assertNoSecretLeak(t, "redacted log", logSurfaces...)

	for name, value := range entry.SensitiveFields {
		if value != redactedMarker {
			t.Fatalf("field %s not redacted: %v", name, value)
		}
	}
	if len(entry.SensitiveFields) != len(sensitiveFields()) {
		t.Fatalf("redacted field count = %d, want %d",
			len(entry.SensitiveFields), len(sensitiveFields()))
	}
	if entry.InternalCategory != string(internalStage) {
		t.Fatalf("log lost internal category: %q", entry.InternalCategory)
	}
	if entry.PublicCategory != PublicAuthFailed || entry.CorrelationID != pub.CorrelationID {
		t.Fatalf("log entry correlation mismatch: %+v vs %+v", entry, pub)
	}

	raw, err := json.Marshal(pub)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	assertNoSecretLeak(t, "json.Marshal", string(raw))
	assertNoInternalLeak(t, "json.Marshal", string(raw))
}

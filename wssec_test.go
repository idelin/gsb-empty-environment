package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, time.November, 15, 12, 0, 0, 0, time.UTC)

func testMessage(timestampID, bodyID, created, expires string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<soapenv:Envelope
  xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
  xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
  xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
  <soapenv:Header>
    <wsse:Security>
      <wsu:Timestamp wsu:Id="%s">
        <wsu:Created>%s</wsu:Created>
        <wsu:Expires>%s</wsu:Expires>
      </wsu:Timestamp>
      <wsse:UsernameToken>
        <wsse:Username>alice</wsse:Username>
        <wsse:Nonce>bm9uY2UtMQ==</wsse:Nonce>
        <wsu:Created>%s</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </soapenv:Header>
  <soapenv:Body wsu:Id="%s">
    <request xmlns="urn:test">payload</request>
  </soapenv:Body>
</soapenv:Envelope>`, timestampID, created, expires, created, bodyID)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func fixedClock() time.Time {
	return testNow
}

func signatureFor(ids ...string) SignatureVerifier {
	return func([]byte) ([]string, error) {
		return ids, nil
	}
}

func validMessage() string {
	return testMessage(
		"TS-1",
		"BODY-1",
		formatTime(testNow.Add(-10*time.Second)),
		formatTime(testNow.Add(2*time.Minute)),
	)
}

func TestValidateAcceptsValidMessage(t *testing.T) {
	validator := NewValidator(signatureFor("#TS-1", "#BODY-1"), WithClock(fixedClock))
	result, err := validator.Validate([]byte(validMessage()))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if result.TimestampID != "TS-1" || result.BodyID != "BODY-1" {
		t.Fatalf("unexpected result IDs: %+v", result)
	}
	if result.Username != "alice" || result.Nonce != "bm9uY2UtMQ==" {
		t.Fatalf("unexpected UsernameToken result: %+v", result)
	}
}

func TestValidateRejectsReplayedUsernameToken(t *testing.T) {
	validator := NewValidator(signatureFor("#TS-1", "#BODY-1"), WithClock(fixedClock))
	message := []byte(validMessage())

	if _, err := validator.Validate(message); err != nil {
		t.Fatalf("first Validate() error = %v", err)
	}
	if _, err := validator.Validate(message); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("second Validate() error = %v, want ErrNonceReplay", err)
	}
}

func TestValidateRejectsDoubleTimestamp(t *testing.T) {
	message := strings.Replace(validMessage(),
		"<wsse:UsernameToken>",
		`<wsu:Timestamp wsu:Id="TS-2">
        <wsu:Created>`+formatTime(testNow.Add(-time.Second))+`</wsu:Created>
        <wsu:Expires>`+formatTime(testNow.Add(time.Minute))+`</wsu:Expires>
      </wsu:Timestamp>
      <wsse:UsernameToken>`,
		1,
	)

	validator := NewValidator(signatureFor("#TS-1", "#TS-2", "#BODY-1"), WithClock(fixedClock))
	if _, err := validator.Validate([]byte(message)); !errors.Is(err, ErrTimestampCount) {
		t.Fatalf("Validate() error = %v, want ErrTimestampCount", err)
	}
}

func TestValidateRejectsSwappedUnsignedTimestamp(t *testing.T) {
	validator := NewValidator(signatureFor("#TS-OLD", "#BODY-1"), WithClock(fixedClock))
	if _, err := validator.Validate([]byte(validMessage())); !errors.Is(err, ErrTimestampUnsigned) {
		t.Fatalf("Validate() error = %v, want ErrTimestampUnsigned", err)
	}
}

func TestValidateRejectsUnsignedBody(t *testing.T) {
	validator := NewValidator(signatureFor("#TS-1"), WithClock(fixedClock))
	if _, err := validator.Validate([]byte(validMessage())); !errors.Is(err, ErrBodyUnsigned) {
		t.Fatalf("Validate() error = %v, want ErrBodyUnsigned", err)
	}
}

func TestValidateRejectsDuplicateWSUID(t *testing.T) {
	message := strings.Replace(validMessage(),
		"<wsse:Security>",
		`<wsse:Security>
      <wsse:BinarySecurityToken wsu:Id="TS-1">duplicate</wsse:BinarySecurityToken>`,
		1,
	)

	validator := NewValidator(signatureFor("#TS-1", "#BODY-1"), WithClock(fixedClock))
	if _, err := validator.Validate([]byte(message)); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("Validate() error = %v, want ErrDuplicateID", err)
	}
}

func TestValidateRejectsRepeatedTimestampIDAttribute(t *testing.T) {
	message := strings.Replace(validMessage(),
		`wsu:Id="TS-1"`,
		`wsu:Id="TS-1" wsu:Id="TS-2"`,
		1,
	)

	validator := NewValidator(signatureFor("#TS-1", "#TS-2", "#BODY-1"), WithClock(fixedClock))
	if _, err := validator.Validate([]byte(message)); !errors.Is(err, ErrWSUID) {
		t.Fatalf("Validate() error = %v, want ErrWSUID", err)
	}
}

func TestValidateTimestampLimits(t *testing.T) {
	tests := []struct {
		name    string
		created time.Time
		expires time.Time
		want    error
	}{
		{
			name:    "created too far in future",
			created: testNow.Add(MaxClockSkew + time.Second),
			expires: testNow.Add(2 * time.Minute),
			want:    ErrTimestampCreatedInFuture,
		},
		{
			name:    "expired",
			created: testNow.Add(-2 * time.Minute),
			expires: testNow.Add(-time.Second),
			want:    ErrTimestampExpired,
		},
		{
			name:    "lifetime too long",
			created: testNow.Add(-time.Second),
			expires: testNow.Add(MaxTimestampLifetime + time.Minute),
			want:    ErrTimestampLifetime,
		},
		{
			name:    "expires before created",
			created: testNow.Add(-time.Second),
			expires: testNow.Add(-2 * time.Second),
			want:    ErrTimestampLifetime,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := testMessage("TS-1", "BODY-1", formatTime(test.created), formatTime(test.expires))
			validator := NewValidator(signatureFor("#TS-1", "#BODY-1"), WithClock(fixedClock))
			if _, err := validator.Validate([]byte(message)); !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateRequiresUsernameTokenByDefault(t *testing.T) {
	message := strings.Replace(validMessage(), "<wsse:UsernameToken>", "<wsse:Unrelated>", 1)
	message = strings.Replace(message, "</wsse:UsernameToken>", "</wsse:Unrelated>", 1)

	validator := NewValidator(signatureFor("#TS-1", "#BODY-1"), WithClock(fixedClock))
	if _, err := validator.Validate([]byte(message)); !errors.Is(err, ErrUsernameTokenCount) {
		t.Fatalf("Validate() error = %v, want ErrUsernameTokenCount", err)
	}
}

func TestValidateCanAllowMissingUsernameToken(t *testing.T) {
	message := strings.Replace(validMessage(), "<wsse:UsernameToken>", "<wsse:Unrelated>", 1)
	message = strings.Replace(message, "</wsse:UsernameToken>", "</wsse:Unrelated>", 1)

	validator := NewValidator(
		signatureFor("#TS-1", "#BODY-1"),
		WithClock(fixedClock),
		WithOptionalUsernameToken(),
	)
	if _, err := validator.Validate([]byte(message)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsSignatureVerifierError(t *testing.T) {
	validator := NewValidator(func([]byte) ([]string, error) {
		return nil, errors.New("bad digest")
	}, WithClock(fixedClock))

	if _, err := validator.Validate([]byte(validMessage())); !errors.Is(err, ErrSignatureVerification) {
		t.Fatalf("Validate() error = %v, want ErrSignatureVerification", err)
	}
}

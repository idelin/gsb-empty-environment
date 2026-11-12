package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)

const (
	validFrom = "2026-01-01T00:00:00Z"
	validTo   = "2027-01-01T00:00:00Z"
)

func permissionsDocument(subject string, rules ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<dds xmlns="http://www.omg.org/spec/DDS-Security/20180822/permissions">
  <permissions>
    <grant name="test-grant">
      <subject_name>` + subject + `</subject_name>
      <validity>
        <not_before>` + validFrom + `</not_before>
        <not_after>` + validTo + `</not_after>
      </validity>
      ` + strings.Join(rules, "\n") + `
      <default>DENY</default>
    </grant>
  </permissions>
</dds>`
}

func ruleXML(effect string, domains string, partitions string, publishTopics, subscribeTopics []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<%s>", effect)
	fmt.Fprintf(&b, "<domains>%s</domains>", domains)
	if len(publishTopics) != 0 {
		b.WriteString("<publish><topics>")
		for _, topic := range publishTopics {
			fmt.Fprintf(&b, "<topic>%s</topic>", topic)
		}
		b.WriteString("</topics></publish>")
	}
	if len(subscribeTopics) != 0 {
		b.WriteString("<subscribe><topics>")
		for _, topic := range subscribeTopics {
			fmt.Fprintf(&b, "<topic>%s</topic>", topic)
		}
		b.WriteString("</topics></subscribe>")
	}
	if partitions != "" {
		b.WriteString("<partitions>")
		for _, partition := range strings.Split(partitions, "|") {
			fmt.Fprintf(&b, "<partition>%s</partition>", partition)
		}
		b.WriteString("</partitions>")
	}
	fmt.Fprintf(&b, "</%s>", effect)
	return b.String()
}

func domainIDs(values ...string) string {
	var b strings.Builder
	for _, value := range values {
		if strings.Contains(value, "-") {
			parts := strings.SplitN(value, "-", 2)
			fmt.Fprintf(&b, "<id_range><min>%s</min><max>%s</max></id_range>", parts[0], parts[1])
		} else {
			fmt.Fprintf(&b, "<id>%s</id>", value)
		}
	}
	return b.String()
}

func mustParse(t *testing.T, document, subject string) *Document {
	t.Helper()
	doc, err := ParsePermissions([]byte(document), subject, testNow)
	if err != nil {
		t.Fatalf("ParsePermissions failed: %v", err)
	}
	return doc
}

func check(t *testing.T, doc *Document, req Request, want Decision) {
	t.Helper()
	if got := doc.Authorize(req); got != want {
		t.Fatalf("Authorize(%+v) = %s, want %s", req, got, want)
	}
}

// TestDenyBeforeAllow verifies that rules are evaluated in document order and
// that the first matching rule wins even when a later allow is broader.
func TestDenyBeforeAllow(t *testing.T) {
	doc := mustParse(t, permissionsDocument("CN=Alice,O=Example",
		ruleXML("deny_rule", domainIDs("0"), "", []string{"Secret*"}, nil),
		ruleXML("allow_rule", domainIDs("0"), "", []string{"*"}, nil),
	), "CN=Alice,O=Example")

	check(t, doc, Request{DomainID: 0, Topic: "SecretPayload", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "PublicTelemetry", Operation: Publish}, Allow)
	check(t, doc, Request{DomainID: 1, Topic: "PublicTelemetry", Operation: Publish}, Deny)
}

// TestWildcardCannotBypassDeny exercises a broad allow glob together with a
// narrower deny that appears first. The allow's '*' must not turn the denied
// subtree into an allowed one.
func TestWildcardCannotBypassDeny(t *testing.T) {
	doc := mustParse(t, permissionsDocument("CN=Alice",
		ruleXML("deny_rule", domainIDs("0"), "", []string{"Public/Internal/*"}, nil),
		ruleXML("allow_rule", domainIDs("0"), "", []string{"Public*"}, nil),
	), "CN=Alice")

	check(t, doc, Request{DomainID: 0, Topic: "Public/Internal/Keys", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Public/Internal/Keys/Export", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Public/Telemetry", Operation: Publish}, Allow)
	check(t, doc, Request{DomainID: 0, Topic: "Private/Telemetry", Operation: Publish}, Deny)
}

// TestTopicWildcardIsCaseSensitive ensures a wildcard does not acquire
// case-insensitive behavior and that literal deny patterns remain exact.
func TestTopicWildcardIsCaseSensitive(t *testing.T) {
	doc := mustParse(t, permissionsDocument("CN=Alice",
		ruleXML("deny_rule", domainIDs("0"), "", []string{"secret/*"}, nil),
		ruleXML("allow_rule", domainIDs("0"), "", []string{"*"}, nil),
	), "CN=Alice")

	check(t, doc, Request{DomainID: 0, Topic: "secret/Data", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Secret/Data", Operation: Publish}, Allow)
}

// TestDomainPartitionAndOperation verifies that all dimensions of a rule have
// to match; matching only the topic is not enough.
func TestDomainPartitionAndOperation(t *testing.T) {
	doc := mustParse(t, permissionsDocument("CN=Alice",
		ruleXML("allow_rule", domainIDs("0"), "Prod*", []string{"*"}, []string{"Events"}),
		ruleXML("allow_rule", domainIDs("2-4"), "*", []string{"Sensor*"}, nil),
	), "CN=Alice")

	check(t, doc, Request{DomainID: 0, Topic: "Any", Partition: "Prod-EU", Operation: Publish}, Allow)
	check(t, doc, Request{DomainID: 0, Topic: "Any", Partitions: []string{"Staging", "Prod-US"}, Operation: Publish}, Allow)
	check(t, doc, Request{DomainID: 1, Topic: "Any", Partition: "Prod-EU", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Any", Partition: "Staging", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Any", Partitions: []string{"Staging", "Dev"}, Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Any", Partition: "", Operation: Publish}, Deny)

	check(t, doc, Request{DomainID: 0, Topic: "Events", Partition: "Prod-A", Operation: Subscribe}, Allow)
	check(t, doc, Request{DomainID: 0, Topic: "Any", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Events", Operation: Publish}, Deny)

	check(t, doc, Request{DomainID: 3, Topic: "Sensor/1", Partitions: []string{"one", "two"}, Operation: Publish}, Allow)
	check(t, doc, Request{DomainID: 5, Topic: "Sensor/1", Operation: Publish}, Deny)
}

func TestNoMatchingRuleDenies(t *testing.T) {
	doc := mustParse(t, permissionsDocument("CN=Alice",
		ruleXML("allow_rule", domainIDs("0"), "", []string{"Approved"}, nil),
	), "CN=Alice")

	check(t, doc, Request{DomainID: 0, Topic: "Unapproved", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Approved", Operation: Subscribe}, Deny)
}

func TestDenyWithoutPartitionsAppliesToAllPartitions(t *testing.T) {
	doc := mustParse(t, permissionsDocument("CN=Alice",
		ruleXML("deny_rule", domainIDs("0"), "", []string{"Secret*"}, nil),
		ruleXML("allow_rule", domainIDs("0"), "", []string{"*"}, nil),
	), "CN=Alice")

	check(t, doc, Request{DomainID: 0, Topic: "Secret", Partition: "Prod", Operation: Publish}, Deny)
	check(t, doc, Request{DomainID: 0, Topic: "Secret", Partitions: []string{"A", "B"}, Operation: Publish}, Deny)
}

func TestExpiredAndNotYetValidDocumentsAreRejected(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr error
	}{
		{
			name:    "expired",
			from:    "2024-01-01T00:00:00Z",
			to:      "2025-01-01T00:00:00Z",
			wantErr: ErrExpired,
		},
		{
			name:    "not yet valid",
			from:    "2027-01-01T00:00:00Z",
			to:      "2028-01-01T00:00:00Z",
			wantErr: ErrNotYetValid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			document := `<permissions>
  <grant name="g">
    <subject_name>CN=Alice</subject_name>
    <validity><not_before>` + tc.from + `</not_before><not_after>` + tc.to + `</not_after></validity>
    <allow_rule>
      <domains><id>0</id></domains>
      <publish><topics><topic>*</topic></topics></publish>
    </allow_rule>
  </grant>
</permissions>`
			doc, err := ParsePermissions([]byte(document), "CN=Alice", testNow)
			if err == nil {
				t.Fatalf("ParsePermissions accepted invalid document: %+v", doc)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, tc.wantErr)
			}
			if !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("error = %v, want it to also match ErrInvalidDocument", err)
			}
			if decision, _ := CheckAccess([]byte(document), "CN=Alice", testNow, Request{DomainID: 0, Topic: "Any", Operation: Publish}); decision != Deny {
				t.Fatalf("CheckAccess decision = %s, want DENY", decision)
			}
		})
	}
}

func TestValidityBoundariesAreInclusive(t *testing.T) {
	document := permissionsDocument("CN=Alice",
		ruleXML("allow_rule", domainIDs("0"), "", []string{"*"}, nil),
	)
	for _, at := range []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		if _, err := ParsePermissions([]byte(document), "CN=Alice", at); err != nil {
			t.Fatalf("boundary time %s rejected: %v", at, err)
		}
	}
}

func TestSubjectMustMatchCertificate(t *testing.T) {
	document := permissionsDocument("CN=Alice,O=Example",
		ruleXML("allow_rule", domainIDs("0"), "", []string{"*"}, nil),
	)

	if _, err := ParsePermissions([]byte(document), "CN=Mallory,O=Example", testNow); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("error = %v, want ErrSubjectMismatch", err)
	}

	// RFC 2253 attribute types are case-insensitive and insignificant
	// whitespace around separators must not prevent a successful binding.
	if _, err := ParsePermissions([]byte(document), "cn = Alice, o = Example", testNow); err != nil {
		t.Fatalf("normalized subject was rejected: %v", err)
	}

	// A DN with a different attribute order is a different string from this
	// package's point of view and must not be accepted.
	if _, err := ParsePermissions([]byte(document), "O=Example,CN=Alice", testNow); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("reordered DN error = %v, want ErrSubjectMismatch", err)
	}
}

func TestSelectsGrantForCertificateSubject(t *testing.T) {
	document := `<permissions>
  <grant name="alice">
    <subject_name>CN=Alice</subject_name>
    <validity><not_before>` + validFrom + `</not_before><not_after>` + validTo + `</not_after></validity>
    <allow_rule><domains><id>0</id></domains><publish><topics><topic>*</topic></topics></publish></allow_rule>
  </grant>
  <grant name="bob">
    <subject_name>CN=Bob</subject_name>
    <validity><not_before>` + validFrom + `</not_before><not_after>` + validTo + `</not_after></validity>
    <allow_rule><domains><id>1</id></domains><publish><topics><topic>BobOnly</topic></topics></publish></allow_rule>
  </grant>
</permissions>`

	bob := mustParse(t, document, "CN=Bob")
	check(t, bob, Request{DomainID: 0, Topic: "Any", Operation: Publish}, Deny)
	check(t, bob, Request{DomainID: 1, Topic: "BobOnly", Operation: Publish}, Allow)
	check(t, bob, Request{DomainID: 1, Topic: "AliceOnly", Operation: Publish}, Deny)
}

func TestCheckAccessRejectsInvalidDocument(t *testing.T) {
	doc := permissionsDocument("CN=Alice")
	decision, err := CheckAccess([]byte(doc), "CN=Mallory", testNow, Request{DomainID: 0, Topic: "Any", Operation: Publish})
	if decision != Deny {
		t.Fatalf("decision = %s, want DENY", decision)
	}
	if !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("error = %v, want ErrSubjectMismatch", err)
	}
}

func TestMalformedDocumentsAreRejected(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{name: "not xml", doc: "not xml"},
		{name: "no grant", doc: `<permissions></permissions>`},
		{name: "missing validity", doc: `<permissions><grant><subject_name>CN=Alice</subject_name><allow_rule><domains><id>0</id></domains></allow_rule></grant></permissions>`},
		{name: "bad date", doc: `<permissions><grant><subject_name>CN=Alice</subject_name><validity><not_before>yesterday</not_before><not_after>2030-01-01T00:00:00Z</not_after></validity></grant></permissions>`},
		{name: "no domains", doc: `<permissions><grant><subject_name>CN=Alice</subject_name><validity><not_before>` + validFrom + `</not_before><not_after>` + validTo + `</not_after></validity><allow_rule><publish><topics><topic>*</topic></topics></publish></allow_rule></grant></permissions>`},
		{name: "bad domain", doc: `<permissions><grant><subject_name>CN=Alice</subject_name><validity><not_before>` + validFrom + `</not_before><not_after>` + validTo + `</not_after></validity><allow_rule><domains><id>x</id></domains><publish><topics><topic>*</topic></topics></publish></allow_rule></grant></permissions>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParsePermissions([]byte(tc.doc), "CN=Alice", testNow); err == nil {
				t.Fatal("malformed document was accepted")
			}
		})
	}
}

func TestFnmatch(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "", true},
		{"*", "anything/at/all", true},
		{"rt/*", "rt/chatter", true},
		{"rt/*", "rt2/chatter", false},
		{"Sensor?", "Sensor1", true},
		{"Sensor?", "Sensor10", false},
		{"Sensor[0-9]", "Sensor7", true},
		{"Sensor[!0-9]", "Sensor7", false},
		{"Sensor[!0-9]", "SensorX", true},
		{"[[:digit:]]*", "123abc", true},
		{"[[:digit:]]*", "abc123", false},
		{`\*`, "*", true},
		{`\*`, "x", false},
		{"secret/*", "Secret/Data", false},
		{"[abc", "a", false},
		{"[z-a]", "z", false},
	}
	for _, tc := range tests {
		if got := Fnmatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("Fnmatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

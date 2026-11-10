package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseTraceAndReplay(t *testing.T) {
	trace := strings.NewReader(`
# comment line
init p=cap_chown i=none e=cap_chown
capset p=cap_chown i=none e=cap_chown,cap_net_admin
ambient raise cap_chown
ambient lower cap_chown
bset drop cap_chown
`)
	events, err := parseTrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5", len(events))
	}
	if events[0].Kind != "init" || events[0].P != bit("cap_chown") || events[0].E != bit("cap_chown") {
		t.Fatalf("init parsed wrong: %+v", events[0])
	}

	var buf bytes.Buffer
	if err := runTrace(&buf, events, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "REJECTED") {
		t.Fatalf("expected rejected capset in report:\n%s", out)
	}
	// bset drop must also be rejected: after init, E has cap_chown only,
	// not cap_setpcap.
	if strings.Count(out, "REJECTED") != 3 {
		t.Fatalf("expected 3 rejected ops (capset, ambient raise, bset), report:\n%s", out)
	}
	if !strings.Contains(out, "cap_chown") {
		t.Fatalf("evidence section missing, report:\n%s", out)
	}
}

func TestStrictModeStops(t *testing.T) {
	trace := strings.NewReader("capset p=cap_chown i=none e=cap_net_admin\n")
	events, err := parseTrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runTrace(&buf, events, true); err == nil {
		t.Fatal("strict mode should return the kernel rejection error")
	}
}

func TestParseTraceErrors(t *testing.T) {
	cases := []string{
		"frobnicate x\n",
		"capset p=cap_chown i=none\n",
		"exec\n",
		"ambient raise cap_nope\n",
		"bset add cap_chown\n",
		"init p=none i=none e=none\ninit p=none i=none e=none\n",
	}
	for _, in := range cases {
		if _, err := parseTrace(strings.NewReader(in)); err == nil {
			t.Fatalf("expected parse error for %q", in)
		}
	}
}

func TestInitBoundingField(t *testing.T) {
	events, err := parseTrace(strings.NewReader("init p=none i=none e=none b=cap_chown\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := initialState()
	if err := s.applyInit(events[0]); err != nil {
		t.Fatal(err)
	}
	if s.Bounding != bit("cap_chown") {
		t.Fatalf("custom bounding not applied: %s", renderSet(s.Bounding))
	}
}

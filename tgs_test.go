package netsec

import (
	"testing"
	"time"
)

var testBase = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

const (
	optsNormal = 0x40810000 // forwardable|renewable, ordinary TGS request
	optsRenew  = optsNormal | optRenew
)

func tgsEvent(at time.Time, account, service string) TGSEvent {
	return TGSEvent{
		Time:          at,
		AccountName:   account,
		ServiceName:   service,
		EType:         ETypeRC4HMAC,
		TicketOptions: optsNormal,
		StatusCode:    0,
	}
}

func hasAlert(alerts []TGSAlert, kind string) bool {
	for _, alert := range alerts {
		if alert.Kind == kind {
			return true
		}
	}
	return false
}

func spn(i int) string {
	return "spn/svc" + string(rune('0'+i)) + ".corp.local"
}

func TestSPNSweepAlertsAtThreshold(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	for i := 0; i < 4; i++ {
		if alerts := detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "alice", spn(i))); len(alerts) != 0 {
			t.Fatalf("distinct SPN count %d must not alert, got %v", i+1, alerts)
		}
	}

	alerts := detector.Process(tgsEvent(testBase.Add(4*time.Minute), "alice", spn(4)))
	if len(alerts) != 1 || alerts[0].Kind != AlertKerberoastingSPNSweep {
		t.Fatalf("5th distinct SPN must trigger one sweep alert, got %v", alerts)
	}
	if alerts[0].DistinctSPNs != 5 {
		t.Fatalf("alert must report 5 distinct SPNs, got %d", alerts[0].DistinctSPNs)
	}
	if alerts[0].AccountName != "alice" || alerts[0].EType != ETypeRC4HMAC {
		t.Fatalf("alert fields mismatch: %+v", alerts[0])
	}
}

func TestRepeatedSPNDoesNotCount(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	for i := 0; i < 4; i++ {
		detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "bob", spn(i)))
	}
	// Repeat every already-seen SPN many times: none may add to the count.
	for round := 0; round < 10; round++ {
		for i := 0; i < 4; i++ {
			if alerts := detector.Process(tgsEvent(testBase.Add(5*time.Minute), "bob", spn(i))); len(alerts) != 0 {
				t.Fatalf("repeated SPNs must not alert, got %v", alerts)
			}
		}
	}
	if got := detector.distinctSPNCount("bob"); got != 4 {
		t.Fatalf("repeated SPNs must keep distinct count at 4, got %d", got)
	}

	// One genuinely new SPN crosses the threshold of 5.
	alerts := detector.Process(tgsEvent(testBase.Add(6*time.Minute), "bob", spn(4)))
	if len(alerts) != 1 || alerts[0].Kind != AlertKerberoastingSPNSweep {
		t.Fatalf("new distinct SPN must trigger sweep alert, got %v", alerts)
	}

	// Further events in the same window must not raise the same alert again.
	alerts = detector.Process(tgsEvent(testBase.Add(7*time.Minute), "bob", spn(5)))
	if len(alerts) != 0 {
		t.Fatalf("sweep alert must fire once per window, got %v", alerts)
	}
}

func TestWindowExpiry(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	// Four SPNs at the start of the window.
	for i := 0; i < 4; i++ {
		detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "carol", spn(i)))
	}

	// One second before the boundary nothing has expired yet.
	near := tgsEvent(testBase.Add(10*time.Minute-time.Second), "carol", spn(9))
	detector.Process(near)
	if got := detector.distinctSPNCount("carol"); got != 5 {
		t.Fatalf("events inside 10 minutes must be retained, got %d", got)
	}
	detector.Reset()
	for i := 0; i < 4; i++ {
		detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "carol", spn(i)))
	}

	// At exactly 10 minutes after the first event, the earliest
	// observations age out of the trailing 10-minute window.
	at := testBase.Add(10 * time.Minute)
	detector.Process(tgsEvent(at, "carol", spn(4)))
	if got := detector.distinctSPNCount("carol"); got != 4 {
		t.Fatalf("expired SPNs must be evicted before counting, want 4 in window, got %d", got)
	}
	if alerts := detector.Process(tgsEvent(at.Add(time.Minute), "carol", spn(5))); len(alerts) != 0 {
		t.Fatalf("cross-window traffic must not alert while under threshold, got %v", alerts)
	}
}

func TestRealertAfterWindowResets(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	for i := 0; i < 5; i++ {
		detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "dave", spn(i)))
	}

	// A second sweep later, after every earlier SPN has aged out, must
	// re-arm and alert again.
	base2 := testBase.Add(30 * time.Minute)
	for i := 0; i < 5; i++ {
		alerts := detector.Process(tgsEvent(base2.Add(time.Duration(i)*time.Minute), "dave", spn(10+i)))
		if i < 4 && len(alerts) != 0 {
			t.Fatalf("second sweep must stay silent before threshold, got %v", alerts)
		}
		if i == 4 && (len(alerts) != 1 || alerts[0].Kind != AlertKerberoastingSPNSweep) {
			t.Fatalf("second sweep must re-alert at threshold, got %v", alerts)
		}
	}
}

func TestMachineAndKRBTGTServicesAreIgnored(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())
	services := []string{
		"DC01$",
		"WEB01$@CORP.LOCAL",
		"krbtgt",
		"KRBTGT@CORP.LOCAL",
	}

	for round, service := range services {
		for i := 0; i < 8; i++ {
			event := tgsEvent(testBase.Add(time.Duration(round*8+i)*time.Minute), "mallory", service)
			if alerts := detector.Process(event); len(alerts) != 0 {
				t.Fatalf("service %q is out of scope, got alerts %v", service, alerts)
			}
		}
	}
	if got := detector.distinctSPNCount("mallory"); got != 0 {
		t.Fatalf("out-of-scope services must never enter window state, got %d", got)
	}
}

func TestAESEnforcedAccountAlertsImmediately(t *testing.T) {
	cfg := DefaultDetectorConfig()
	cfg.AESOnlyServiceAccounts = []string{"MSSQLSvc/db01.corp.local:1433"}
	detector := NewTGSDetector(cfg)

	// A single RC4 ticket for an AES-enforced account alerts even though
	// the sweep threshold is nowhere near.
	alerts := detector.Process(tgsEvent(testBase, "eve", "MSSQLSvc/db01.corp.local:1433"))
	if len(alerts) != 1 || alerts[0].Kind != AlertAESEnforcedRC4Ticket {
		t.Fatalf("AES-enforced account must alert on first RC4 ticket, got %v", alerts)
	}
	if alerts[0].ServiceName != "mssqlsvc/db01.corp.local:1433" {
		t.Fatalf("service name must be normalized, got %q", alerts[0].ServiceName)
	}

	// Every subsequent RC4 ticket for that account is independently
	// suspicious and must alert again (there is no per-window dedup here).
	alerts = detector.Process(tgsEvent(testBase.Add(time.Minute), "eve", "MSSQLSvc/db01.corp.local:1433"))
	if len(alerts) != 1 || alerts[0].Kind != AlertAESEnforcedRC4Ticket {
		t.Fatalf("repeat AES-enforced RC4 ticket must alert again, got %v", alerts)
	}

	// AES tickets for the same account are not part of this rule.
	aesEvent := tgsEvent(testBase.Add(2*time.Minute), "eve", "MSSQLSvc/db01.corp.local:1433")
	aesEvent.EType = 0x12
	if alerts := detector.Process(aesEvent); len(alerts) != 0 {
		t.Fatalf("AES ticket must be ignored, got %v", alerts)
	}
}

func TestAESEnforcedMatchByAccountBase(t *testing.T) {
	cfg := DefaultDetectorConfig()
	cfg.AESOnlyServiceAccounts = []string{"svc_sql"}
	detector := NewTGSDetector(cfg)

	// The enforced list holds a bare account name while the event carries
	// an SPN-style / realm-suffixed service name; the base still matches.
	alerts := detector.Process(tgsEvent(testBase, "frank", "svc_sql@CORP.LOCAL"))
	if len(alerts) != 1 || alerts[0].Kind != AlertAESEnforcedRC4Ticket {
		t.Fatalf("realm-suffixed AES-enforced account must match, got %v", alerts)
	}

	other := NewTGSDetector(cfg)
	if alerts := other.Process(tgsEvent(testBase, "frank", "HTTP/web.corp.local")); len(alerts) != 0 {
		t.Fatalf("non-enforced service must not produce AES alerts, got %v", alerts)
	}
}

func TestRenewalRequestsAreIgnored(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	// Flood the window with renewal requests for many distinct SPNs.
	for i := 0; i < 20; i++ {
		event := tgsEvent(testBase.Add(time.Duration(i)*time.Second), "grace", spn(i))
		event.TicketOptions = optsRenew
		if alerts := detector.Process(event); len(alerts) != 0 {
			t.Fatalf("renewal requests must be ignored, got %v", alerts)
		}
	}
	if got := detector.distinctSPNCount("grace"); got != 0 {
		t.Fatalf("renewals must not populate window state, got %d", got)
	}

	// The ordinary renewable bit (set on every normal Windows TGS request)
	// without KDC_OPT_RENEW must still be processed normally.
	for i := 0; i < 5; i++ {
		detector.Process(tgsEvent(testBase.Add(time.Minute+time.Duration(i)*time.Second), "grace", spn(i)))
	}
	if got := detector.distinctSPNCount("grace"); got != 5 {
		t.Fatalf("non-renewal requests must be counted, got %d", got)
	}

	// When renewal filtering is disabled, renewals count like any request.
	permissive := NewTGSDetector(DetectorConfig{
		Window:         10 * time.Minute,
		SPNThreshold:   5,
		IgnoreRenewals: false,
	})
	for i := 0; i < 5; i++ {
		event := tgsEvent(testBase.Add(time.Duration(i)*time.Second), "henry", spn(i))
		event.TicketOptions = optsRenew
		permissive.Process(event)
	}
	if got := permissive.distinctSPNCount("henry"); got != 5 {
		t.Fatalf("permissive detector must count renewals, got %d", got)
	}
}

func TestStatusAndETypeFilters(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	for i := 0; i < 10; i++ {
		failed := tgsEvent(testBase.Add(time.Duration(i)*time.Second), "ivan", spn(i))
		failed.StatusCode = 0x1B // KDC_ERR_ETYPE_NOTSUPP
		if alerts := detector.Process(failed); len(alerts) != 0 {
			t.Fatalf("failed requests must be ignored, got %v", alerts)
		}

		aes := tgsEvent(testBase.Add(time.Duration(i)*time.Second), "ivan", spn(i))
		aes.EType = 0x12
		if alerts := detector.Process(aes); len(alerts) != 0 {
			t.Fatalf("AES tickets must be ignored by the RC4 rule, got %v", alerts)
		}
	}
	if got := detector.distinctSPNCount("ivan"); got != 0 {
		t.Fatalf("filtered events must not enter the window, got %d", got)
	}
}

func TestAccountsAreTrackedIndependently(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	for i := 0; i < 4; i++ {
		detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "judy", spn(i)))
		detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "kim", spn(i)))
	}
	if got := detector.distinctSPNCount("judy"); got != 4 {
		t.Fatalf("judy should hold 4 SPNs, got %d", got)
	}
	alerts := detector.Process(tgsEvent(testBase.Add(4*time.Minute), "kim", spn(4)))
	if len(alerts) != 1 || alerts[0].AccountName != "kim" {
		t.Fatalf("only the crossing account must alert, got %v", alerts)
	}
	if alerts := detector.Process(tgsEvent(testBase.Add(4*time.Minute), "judy", spn(4))); len(alerts) != 1 {
		t.Fatalf("judy crossing the same threshold must alert independently, got %v", alerts)
	}
}

func TestCaseInsensitiveDedup(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())
	services := []string{
		"MSSQLSvc/db01.corp.local:1433",
		"mssqlsvc/DB01.CORP.LOCAL:1433",
		"Mssqlsvc/Db01.Corp.Local:1433",
	}
	for i, service := range services {
		alerts := detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "ALICE", service))
		if len(alerts) != 0 {
			t.Fatalf("case-only differences are the same SPN, got %v", alerts)
		}
	}
	if got := detector.distinctSPNCount("alice"); got != 1 {
		t.Fatalf("case-insensitive SPN must count once, got %d", got)
	}
}

func TestOutOfOrderEvents(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())

	// Events delivered out of order: the trailing window is computed from
	// each event's own timestamp, so the detector stays correct.
	ordered := []int{2, 0, 4, 1, 3}
	var alerted bool
	for _, idx := range ordered {
		alerts := detector.Process(tgsEvent(testBase.Add(time.Duration(idx)*time.Minute), "leo", spn(idx)))
		if len(alerts) > 0 {
			alerted = true
		}
	}
	if !alerted {
		t.Fatal("the 5th chronologically distinct SPN must alert regardless of delivery order")
	}

	// A late event older than the window does not resurrect expired SPNs.
	late := detector.Process(tgsEvent(testBase.Add(30*time.Minute), "leo", spn(9)))
	if len(late) != 0 || detector.distinctSPNCount("leo") != 1 {
		t.Fatalf("late event must start a fresh window, got alerts=%v count=%d", late, detector.distinctSPNCount("leo"))
	}
}

func TestSweepAndAESEnforcedAlertTogether(t *testing.T) {
	cfg := DefaultDetectorConfig()
	cfg.AESOnlyServiceAccounts = []string{"spn/svc4.corp.local"}
	detector := NewTGSDetector(cfg)

	var alerts []TGSAlert
	for i := 0; i < 5; i++ {
		alerts = detector.Process(tgsEvent(testBase.Add(time.Duration(i)*time.Minute), "mia", spn(i)))
	}
	if len(alerts) != 2 {
		t.Fatalf("threshold event for AES account must yield both alerts, got %v", alerts)
	}
	if !hasAlert(alerts, AlertKerberoastingSPNSweep) || !hasAlert(alerts, AlertAESEnforcedRC4Ticket) {
		t.Fatalf("alerts must cover both rules, got %v", alerts)
	}
}

func TestResetAndPurge(t *testing.T) {
	detector := NewTGSDetector(DefaultDetectorConfig())
	for i := 0; i < 3; i++ {
		detector.Process(tgsEvent(testBase, "nina", spn(i)))
	}
	detector.Reset()
	if got := detector.distinctSPNCount("nina"); got != 0 {
		t.Fatalf("Reset must clear window state, got %d", got)
	}

	for i := 0; i < 3; i++ {
		detector.Process(tgsEvent(testBase, "oscar", spn(i)))
	}
	detector.Purge(testBase.Add(10 * time.Minute))
	if got := detector.distinctSPNCount("oscar"); got != 0 {
		t.Fatalf("Purge must drop fully expired account state, got %d", got)
	}

	for i := 0; i < 3; i++ {
		detector.Process(tgsEvent(testBase.Add(20*time.Minute), "oscar", spn(i)))
	}
	detector.Purge(testBase.Add(21 * time.Minute))
	if got := detector.distinctSPNCount("oscar"); got != 3 {
		t.Fatalf("Purge must retain in-window account state, got %d", got)
	}
}

func TestIsRenewal(t *testing.T) {
	cases := []struct {
		name string
		opts uint32
		want bool
	}{
		{"ordinary request", optsNormal, false},
		{"renewable only", optRenewable, false},
		{"renew request", optRenew, true},
		{"renew combined", optsRenew, true},
		{"empty options", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := TGSEvent{TicketOptions: tc.opts}
			if got := event.IsRenewal(); got != tc.want {
				t.Fatalf("IsRenewal for %#x = %v, want %v", tc.opts, got, tc.want)
			}
		})
	}
}

func TestParseUint32Helpers(t *testing.T) {
	cases := []struct {
		raw  string
		want uint32
	}{
		{"0x17", 0x17},
		{"0X17", 0x17},
		{"17", 17},
		{"0x40810000", 0x40810000},
		{"40810000", 40810000},
		{"23", 0x17},
		{"1082195968", 1082195968},
		{"  0x12  ", 0x12},
		{"", 0},
		{"0xZZ", 0},
		{"0x100000000", 0},
	}
	for _, tc := range cases {
		if got := ParseEType(tc.raw); got != tc.want {
			t.Errorf("ParseEType(%q) = %#x, want %#x", tc.raw, got, tc.want)
		}
		if got := ParseTicketOptions(tc.raw); got != tc.want {
			t.Errorf("ParseTicketOptions(%q) = %#x, want %#x", tc.raw, got, tc.want)
		}
	}
}

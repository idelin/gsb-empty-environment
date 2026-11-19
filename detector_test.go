package netsec

import (
	"testing"
	"time"
)

var testBase = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func tgsEvent(requester, spn string, offset time.Duration) Event {
	return Event{
		Time:              testBase.Add(offset),
		RequestingAccount: requester,
		SPN:               spn,
		EType:             RC4EType,
	}
}

func serviceEvent(requester, service, spn string, offset time.Duration) Event {
	event := tgsEvent(requester, spn, offset)
	event.ServiceAccount = service
	return event
}

func TestBulkDistinctSPNsAlertsAtThreshold(t *testing.T) {
	detector := NewDetector()

	for i := 0; i < 4; i++ {
		spn := "MSSQLSvc/sql" + string(rune('a'+i)) + ".corp.local:1433"
		if alert, ok := detector.Evaluate(tgsEvent("alice", spn, time.Duration(i)*time.Minute)); ok {
			t.Fatalf("event %d alerted before threshold: %+v", i+1, alert)
		}
	}

	alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/sql-e.corp.local:1433", 4*time.Minute))
	if !ok {
		t.Fatal("fifth distinct SPN did not alert")
	}
	if !alert.HasReason(ReasonBulkRC4) {
		t.Fatalf("missing bulk reason: %v", alert.Reasons)
	}
	if alert.HasReason(ReasonAESEnforcedRC4) {
		t.Fatalf("unexpected AES reason: %v", alert.Reasons)
	}
	if alert.DistinctSPNCount != 5 {
		t.Fatalf("distinct count = %d, want 5", alert.DistinctSPNCount)
	}
	if len(alert.DistinctSPNs) != 5 {
		t.Fatalf("distinct SPNs = %v, want 5 entries", alert.DistinctSPNs)
	}
}

func TestRepeatedSPNAndRenewalDoNotCountAgain(t *testing.T) {
	detector := NewDetector()
	spns := []string{
		"MSSQLSvc/sql1.corp.local:1433",
		"MSSQLSvc/sql2.corp.local:1433",
		"MSSQLSvc/sql3.corp.local:1433",
		"MSSQLSvc/sql4.corp.local:1433",
	}
	for i, spn := range spns {
		event := tgsEvent("alice", spn, time.Duration(i)*time.Minute)
		event.Renewal = i%2 == 0
		if alert, ok := detector.Evaluate(event); ok {
			t.Fatalf("unexpected alert before fifth SPN: %+v", alert)
		}
	}

	for i := 0; i < 8; i++ {
		event := tgsEvent("alice", "mssqlsvc/SQL1.CORP.LOCAL:1433", 5*time.Minute+time.Duration(i)*time.Second)
		event.Renewal = true
		if alert, ok := detector.Evaluate(event); ok {
			t.Fatalf("repeated/renewed SPN caused an alert: %+v", alert)
		}
	}

	if alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/sql5.corp.local:1433", 6*time.Minute)); !ok {
		t.Fatalf("new fifth distinct SPN did not alert after repeats: %+v", alert)
	}
}

func TestMachineAndKRBTGTServiceAccountsAreExcluded(t *testing.T) {
	tests := []struct {
		name    string
		service string
		spn     string
	}{
		{name: "machine service account", service: "WS01$", spn: "HOST/WS01.corp.local"},
		{name: "krbtgt primary", service: "krbtgt", spn: "krbtgt/CORP.LOCAL"},
		{name: "krbtgt case variant", service: "KRBTGT", spn: "kdc/CORP.LOCAL"},
		{name: "machine account from SPN", spn: "CIFS/fileserver$"},
		{name: "krbtgt account from SPN", spn: "KRBTGT/CORP.LOCAL"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			detector := NewDetector()
			for i := 0; i < 10; i++ {
				event := serviceEvent("alice", test.service, test.spn, time.Duration(i)*time.Second)
				if test.service == "" {
					event.ServiceAccount = ""
				}
				if alert, ok := detector.Evaluate(event); ok {
					t.Fatalf("excluded service alerted: %+v", alert)
				}
			}
		})
	}
}

func TestExcludedServicesDoNotHelpReachThreshold(t *testing.T) {
	detector := NewDetector()
	for i := 0; i < 4; i++ {
		spn := "MSSQLSvc/sql" + string(rune('a'+i)) + ".corp.local:1433"
		detector.Evaluate(tgsEvent("alice", spn, time.Duration(i)*time.Minute))
	}

	detector.Evaluate(serviceEvent("alice", "WS01$", "HOST/WS01.corp.local", 4*time.Minute))
	detector.Evaluate(serviceEvent("alice", "krbtgt", "krbtgt/CORP.LOCAL", 4*time.Minute+time.Second))

	if alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/sql-other.corp.local:1433", 5*time.Minute)); !ok {
		t.Fatalf("excluded services should not count; expected alert for fifth real SPN: %+v", alert)
	}
}

func TestAESEnforcedAccountAlertsOnFirstRC4Ticket(t *testing.T) {
	detector := NewDetector(Config{
		AESEnforcedAccounts: []string{"svc_legacy", "SVC_OTHER"},
	})

	alert, ok := detector.Evaluate(serviceEvent("alice", "SVC_LEGACY", "MSSQLSvc/sql1.corp.local:1433", 0))
	if !ok {
		t.Fatal("RC4 ticket for AES-enforced account did not alert")
	}
	if !alert.HasReason(ReasonAESEnforcedRC4) {
		t.Fatalf("missing AES reason: %v", alert.Reasons)
	}
	if alert.HasReason(ReasonBulkRC4) {
		t.Fatalf("unexpected bulk reason: %v", alert.Reasons)
	}

	aesEvent := serviceEvent("alice", "svc_other", "MSSQLSvc/sql2.corp.local:1433", time.Second)
	aesEvent.EType = 0x12
	if alert, ok := detector.Evaluate(aesEvent); ok {
		t.Fatalf("AES ticket for enforced account alerted: %+v", alert)
	}
}

func TestAESEnforcementFromEventAndPolicy(t *testing.T) {
	event := serviceEvent("alice", "svc_marked", "MSSQLSvc/sql1.corp.local:1433", 0)
	event.AESEnforced = true
	if alert, ok := NewDetector().Evaluate(event); !ok || !alert.HasReason(ReasonAESEnforcedRC4) {
		t.Fatalf("event-level AES enforcement did not alert: %+v, %v", alert, ok)
	}

	detector := NewDetector(Config{
		AESEnforced: func(account string) bool {
			return account == "svc_policy"
		},
	})
	alert, ok := detector.Evaluate(serviceEvent("alice", "svc_policy", "MSSQLSvc/sql1.corp.local:1433", 0))
	if !ok || !alert.HasReason(ReasonAESEnforcedRC4) {
		t.Fatalf("policy callback did not alert: %+v, %v", alert, ok)
	}
}

func TestDistinctSPNsAreCaseInsensitive(t *testing.T) {
	detector := NewDetector()
	spns := []string{
		"MSSQLSvc/sql1.corp.local:1433",
		"mssqlsvc/SQL1.CORP.LOCAL:1433",
		"MSSQLSvc/sql2.corp.local:1433",
		"MSSQLSvc/sql3.corp.local:1433",
		"MSSQLSvc/sql4.corp.local:1433",
	}
	for i, spn := range spns {
		if alert, ok := detector.Evaluate(tgsEvent("alice", spn, time.Duration(i)*time.Minute)); ok {
			t.Fatalf("case variant counted as a new SPN: %+v", alert)
		}
	}
}

func TestRequestersHaveIndependentWindows(t *testing.T) {
	detector := NewDetector()
	for i := 0; i < 4; i++ {
		detector.Evaluate(tgsEvent("alice", "MSSQLSvc/a"+string(rune('0'+i))+".corp.local:1433", time.Duration(i)*time.Minute))
		detector.Evaluate(tgsEvent("bob", "MSSQLSvc/b"+string(rune('0'+i))+".corp.local:1433", time.Duration(i)*time.Minute))
	}

	if alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/a4.corp.local:1433", 5*time.Minute)); !ok {
		t.Fatalf("alice should alert independently: %+v", alert)
	}
	if alert, ok := detector.Evaluate(tgsEvent("bob", "MSSQLSvc/b4.corp.local:1433", 5*time.Minute)); !ok {
		t.Fatalf("bob should alert independently: %+v", alert)
	}
}

func TestCrossWindowExpiry(t *testing.T) {
	t.Run("event at exact boundary is included", func(t *testing.T) {
		detector := NewDetector()
		for i := 0; i < 4; i++ {
			detector.Evaluate(tgsEvent("alice", "MSSQLSvc/a"+string(rune('0'+i))+".corp.local:1433", time.Duration(i)*time.Minute))
		}
		alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/a4.corp.local:1433", 10*time.Minute))
		if !ok || !alert.HasReason(ReasonBulkRC4) {
			t.Fatalf("event exactly 10 minutes later should still be in window: %+v, %v", alert, ok)
		}
	})

	t.Run("event just beyond boundary expires oldest", func(t *testing.T) {
		detector := NewDetector()
		for i := 0; i < 4; i++ {
			detector.Evaluate(tgsEvent("alice", "MSSQLSvc/a"+string(rune('0'+i))+".corp.local:1433", time.Duration(i)*time.Minute))
		}
		if alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/a4.corp.local:1433", 10*time.Minute+time.Nanosecond)); ok {
			t.Fatalf("oldest event should have expired: %+v", alert)
		}
	})

	t.Run("window can alert again after expiry", func(t *testing.T) {
		detector := NewDetector()
		alerts := 0
		for i := 0; i < 5; i++ {
			if _, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/first"+string(rune('0'+i))+".corp.local:1433", time.Duration(i)*time.Minute)); ok {
				alerts++
			}
		}
		if alerts != 1 {
			t.Fatalf("first window alerts = %d, want 1", alerts)
		}

		for i := 0; i < 5; i++ {
			if _, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/second"+string(rune('0'+i))+".corp.local:1433", 20*time.Minute+time.Duration(i)*time.Minute)); ok {
				alerts++
			}
		}
		if alerts != 2 {
			t.Fatalf("total alerts = %d, want 2", alerts)
		}
	})
}

func TestProcessBatchSortsEvents(t *testing.T) {
	detector := NewDetector()
	events := []Event{
		tgsEvent("alice", "MSSQLSvc/sql5.corp.local:1433", 4*time.Minute),
		tgsEvent("alice", "MSSQLSvc/sql3.corp.local:1433", 2*time.Minute),
		tgsEvent("alice", "MSSQLSvc/sql1.corp.local:1433", 0),
		tgsEvent("alice", "MSSQLSvc/sql4.corp.local:1433", 3*time.Minute),
		tgsEvent("alice", "MSSQLSvc/sql2.corp.local:1433", time.Minute),
	}

	alerts := detector.ProcessBatch(events)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	if events[0].SPN != "MSSQLSvc/sql5.corp.local:1433" {
		t.Fatalf("ProcessBatch modified caller input: %v", events)
	}
}

func TestNonRC4AndMalformedEventsAreIgnored(t *testing.T) {
	detector := NewDetector()
	for i := 0; i < 10; i++ {
		event := tgsEvent("alice", "MSSQLSvc/sql"+string(rune('0'+i))+".corp.local:1433", time.Duration(i)*time.Second)
		event.EType = 0x12
		if alert, ok := detector.Evaluate(event); ok {
			t.Fatalf("AES event alerted: %+v", alert)
		}
	}

	if alert, ok := detector.Evaluate(Event{Time: testBase, RequestingAccount: "alice", EType: RC4EType}); ok {
		t.Fatalf("event without service identity alerted: %+v", alert)
	}
}

func TestResetAndConfiguration(t *testing.T) {
	detector := NewDetector(Config{Window: time.Minute, DistinctSPNThreshold: 2})
	if detector.Window() != time.Minute {
		t.Fatalf("window = %s, want 1m", detector.Window())
	}
	if detector.DistinctSPNThreshold() != 2 {
		t.Fatalf("threshold = %d, want 2", detector.DistinctSPNThreshold())
	}

	detector.Evaluate(tgsEvent("alice", "MSSQLSvc/sql1.corp.local:1433", 0))
	if alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/sql2.corp.local:1433", time.Second)); !ok {
		t.Fatalf("configured threshold did not alert: %+v", alert)
	}

	detector.Reset()
	if alert, ok := detector.Evaluate(tgsEvent("alice", "MSSQLSvc/sql3.corp.local:1433", 2*time.Second)); ok {
		t.Fatalf("Reset did not clear state: %+v", alert)
	}
}

package netsec

import "testing"

func TestComposeDecisionsPriorityAndNewestData(t *testing.T) {
	actions := []Action{ActionAllow, ActionNotify, ActionDeny, ActionTerminate}
	for _, older := range actions {
		for _, newer := range actions {
			got := ComposeDecisions(
				Decision{Action: older, Data: 100},
				Decision{Action: newer, Data: 200},
			)
			switch {
			case newer > older:
				if got != (Decision{Action: newer, Data: 200}) {
					t.Fatalf("newer stricter action: older=%s newer=%s got=%s", older, newer, got)
				}
			case older > newer:
				if got != (Decision{Action: older, Data: 100}) {
					t.Fatalf("older stricter action: older=%s newer=%s got=%s", older, newer, got)
				}
			default:
				if got != (Decision{Action: older, Data: 200}) {
					t.Fatalf("equal action must take newest data: older=%s newer=%s got=%s", older, newer, got)
				}
			}
		}
	}
}

func TestActionOrdering(t *testing.T) {
	order := []Action{ActionAllow, ActionNotify, ActionDeny, ActionTerminate}
	for i := 1; i < len(order); i++ {
		if !order[i].MoreRestrictiveThan(order[i-1]) {
			t.Fatalf("%s must be more restrictive than %s", order[i], order[i-1])
		}
	}
	if Action(99).Valid() {
		t.Fatal("unknown action accepted")
	}
}

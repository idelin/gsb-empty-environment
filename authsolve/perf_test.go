package authsolve

import (
	"fmt"
	"testing"

	"prescreen.local/ps-e39099b0dc/clockwire"
)

func TestTwelveCandidateWorstCase(t *testing.T) {
	p := Problem{
		Devices:         []Device{{ID: "ref", Reference: true}},
		Policy:          PolicyNode{Role: "r"},
		Windows:         []Window{{Start: 10, End: 11}, {Start: 12, End: 13}},
		ReferenceDevice: "ref",
	}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("d%d", i)
		p.Devices = append(p.Devices, Device{ID: id})
		p.Clock = append(p.Clock, ClockConstraint{Device: id, Other: "ref", Bound: clockwire.New(0, 0)})
		p.Credentials = append(p.Credentials, Credential{
			ID: fmt.Sprintf("c%d", i), Identity: fmt.Sprintf("s%d", i), Device: id,
			Role: "r", NotBefore: 0, NotAfter: 1,
		})
	}
	if _, err := Solve(p); err == nil {
		t.Fatal("expected no witness")
	}
}

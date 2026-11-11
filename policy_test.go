package netsec

import "testing"

func TestPolicyCheck(t *testing.T) {
	p := Policy{
		Vulnerable: ClosedOpen(MustParse("1.0.0"), MustParse("1.4.0")).
			Union(Closed(MustParse("2.0.0-a"), MustParse("2.0.0"))),
		Exemptions: ExactVersions(MustParse("1.2.3"), MustParse("2.0.0-b")),
	}

	cases := []struct {
		version    string
		vulnerable bool
		exempted   bool
	}{
		{"0.9.9", false, false},
		{"1.0.0", true, false},
		{"1.2.3", false, true}, // backport exemption
		{"1.2.4", true, false},
		{"1.4.0", false, false}, // range is half-open
		{"1.4.0-1", true, false},
		{"2.0.0-b", false, true}, // pre-release backport exemption
		{"2.0.0-c", true, false},
		{"2.0.0", true, false},
		{"2.0.1", false, false},
	}
	for _, c := range cases {
		v := MustParse(c.version)
		got := p.Check(v)
		if got.Vulnerable != c.vulnerable || got.Exempted != c.exempted {
			t.Errorf("Check(%s) = {Vulnerable:%v Exempted:%v}, want {%v %v} (reason: %s)",
				c.version, got.Vulnerable, got.Exempted, c.vulnerable, c.exempted, got.Reason)
		}
		if got.Reason == "" {
			t.Errorf("Check(%s) returned empty evidence", c.version)
		}
		if (c.vulnerable || c.exempted) && !got.Matched {
			t.Errorf("Check(%s) missing matched interval evidence", c.version)
		}
	}
}

package netsec

import "testing"

func TestReasonString(t *testing.T) {
	cases := map[Reason]string{
		ReasonUnknown:   "unknown",
		ReasonLocalCap:  "local_cap",
		ReasonGlobalCap: "global_cap",
		ReasonWithdrawn: "withdrawn",
		Reason(99):      "unknown",
	}
	for reason, want := range cases {
		if got := reason.String(); got != want {
			t.Fatalf("Reason(%d).String() = %q, want %q", reason, got, want)
		}
	}
}

func TestTrimRecordsEmpty(t *testing.T) {
	res, err := TrimRecords(testKey, nil, 1, 1)
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if len(res.Kept) != 0 || len(res.Dropped) != 0 || len(res.Withdrawn) != 0 {
		t.Fatalf("empty input must yield an empty result: %+v", res)
	}
}

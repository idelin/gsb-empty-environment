package netsec

import "fmt"

func ExampleCorrelate() {
	// The revocation is listed first (out-of-order arrival), but trusted
	// sequences and windows decide the outcome rather than slice order.
	events := []Event{
		{ID: "rev-1", Feed: "audit-b", Seq: 1, Kind: KindRevoke,
			Cred: "AKIA123", Lo: 12, Hi: 18},
		{ID: "use-1", Feed: "audit-a", Seq: 2, Kind: KindUse,
			Cred: "AKIA123", Lo: 2, Hi: 8},
	}

	report, err := Correlate(events)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, p := range report.Pairs {
		fmt.Printf("%s %s use-window=[%d,%d] revoke-window=[%d,%d] checked=%v\n",
			p.UseID, p.Verdict,
			p.NarrowedUse.Lo, p.NarrowedUse.Hi,
			p.NarrowedRevoke.Lo, p.NarrowedRevoke.Hi,
			p.EnumChecked)
	}
	// Output:
	// use-1 necessarily-before use-window=[2,8] revoke-window=[12,18] checked=true
}

func ExampleCorrelate_straddle() {
	events := []Event{
		{ID: "use-1", Feed: "audit-a", Seq: 1, Kind: KindUse,
			Cred: "AKIA123", Lo: 5, Hi: 9},
		{ID: "rev-1", Feed: "audit-b", Seq: 1, Kind: KindRevoke,
			Cred: "AKIA123", Lo: 6, Hi: 10},
	}

	report, err := Correlate(events)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	p := report.Pairs[0]
	fmt.Printf("%s before-feasible=%v after-feasible=%v same-time-feasible=%v\n",
		p.Verdict, p.BeforeFeasible, p.AfterFeasible, p.SameTimeFeasible)
	fmt.Printf("enumeration before=%d after=%d same=%d\n",
		p.EnumBefore, p.EnumAfter, p.EnumSame)
	// Output:
	// possibly-straddles before-feasible=true after-feasible=true same-time-feasible=true
	// enumeration before=15 after=6 same=4
}

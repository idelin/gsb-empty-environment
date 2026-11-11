package netsec_test

import (
	"errors"
	"fmt"
	"log"

	netsec "example.org/netsec/uid1288"
)

func ExamplePublish() {
	src, _ := netsec.ParseIPv4("10.0.0.0")
	dst, _ := netsec.ParseIPv4("192.168.1.1")

	rules := []netsec.Rule{{
		ID:        "allow-web",
		SrcIP:     netsec.IPRange{From: src, To: src + 255},
		DstIP:     netsec.IPRange{From: dst, To: dst},
		SrcPort:   netsec.PortRange{From: 1024, To: 65535},
		DstPort:   netsec.PortRange{From: 80, To: 80},
		Protocols: []uint8{6}, // TCP
		Action:    netsec.ActionAllow,
		Comment:   "web access",
	}}

	plan, report, err := netsec.Publish(rules, netsec.Options{
		MaxEntries:         16,
		IncludeDefaultDeny: false,
	})
	if err != nil {
		log.Fatalf("publish rejected: %v", err)
	}

	fmt.Printf("published=%d checked=%d action=%s source=%s\n",
		len(plan.Shards), report.PointsChecked,
		plan.Shards[0].Action, plan.Shards[0].SourceID)
	fmt.Println(plan.Shards[0].Explanation)
	// Output:
	// published=1 checked=13932 action=allow source=allow-web
	// first-match allow from allow-web (rule index 0): web access
}

func ExampleCompress_budgetExceeded() {
	rules := []netsec.Rule{
		{
			SrcIP:     netsec.IPRange{From: 1, To: 1},
			DstIP:     netsec.IPRange{From: 1, To: 1},
			SrcPort:   netsec.PortRange{From: 1, To: 1},
			DstPort:   netsec.PortRange{From: 1, To: 1},
			Protocols: []uint8{6},
			Action:    netsec.ActionAllow,
		},
		{
			SrcIP:     netsec.IPRange{From: 2, To: 2},
			DstIP:     netsec.IPRange{From: 2, To: 2},
			SrcPort:   netsec.PortRange{From: 2, To: 2},
			DstPort:   netsec.PortRange{From: 2, To: 2},
			Protocols: []uint8{17},
			Action:    netsec.ActionDeny,
		},
	}

	_, _, err := netsec.Publish(rules, netsec.Options{MaxEntries: 1})
	fmt.Println("rejected:", errors.Is(err, netsec.ErrBudgetExceeded))
	// Output:
	// rejected: true
}

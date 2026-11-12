package memberstate_test

import (
	"fmt"
	"net/netip"
	"time"

	"example.org/netsec/q0087/memberstate"
)

func ExampleTable() {
	clock := func() time.Time { return time.Unix(1_700_000_000, 0) }
	table, err := memberstate.NewTable(memberstate.Config{
		IPv4Universe: netip.MustParsePrefix("192.0.2.0/28"),
		IPv6Universe: netip.MustParsePrefix("2001:db8::/124"),
		Clock:        clock,
	})
	if err != nil {
		panic(err)
	}

	group := memberstate.GroupKey{VLAN: 7, Addr: netip.MustParseAddr("239.1.2.3")}

	// 租户 A 的端口 1：只包含 0-3。
	_, _ = table.Join(memberstate.Registration{
		Group:   group,
		Tenant:  "tenant-a",
		Port:    "port-1",
		Mode:    memberstate.Include,
		Sources: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/30")},
	})

	// 租户 C 的端口 3：包含 4-7，与端口 1 的许可拼成完整 /29。
	_, _ = table.Join(memberstate.Registration{
		Group:   group,
		Tenant:  "tenant-c",
		Port:    "port-3",
		Mode:    memberstate.Include,
		Sources: []netip.Prefix{netip.MustParsePrefix("192.0.2.4/30")},
	})

	// 租户 B 的端口 2：排除 8-11，许可其余全部源。
	_, _ = table.Join(memberstate.Registration{
		Group:   group,
		Tenant:  "tenant-b",
		Port:    "port-2",
		Mode:    memberstate.Exclude,
		Sources: []netip.Prefix{netip.MustParsePrefix("192.0.2.8/30")},
	})

	for _, block := range table.AllowedSources(group, memberstate.IPv4) {
		fmt.Printf("allowed %s\n", block.Prefix)
		for _, auth := range block.Authorizers {
			mode := "INCLUDE"
			if auth.Mode == memberstate.Exclude {
				mode = "EXCLUDE"
			}
			fmt.Printf("  authorized by %s/%s mode=%s", auth.Tenant, auth.Port, mode)
			if len(auth.IncludePrefixes) > 0 {
				fmt.Printf(" include=%v", auth.IncludePrefixes)
			}
			if len(auth.ExcludePrefixes) > 0 {
				fmt.Printf(" exclude=%v", auth.ExcludePrefixes)
			}
			fmt.Println()
		}
	}

	// Output:
	// allowed 192.0.2.0/29
	//   authorized by tenant-a/port-1 mode=INCLUDE include=[192.0.2.0/30]
	//   authorized by tenant-b/port-2 mode=EXCLUDE exclude=[192.0.2.8/30]
	//   authorized by tenant-c/port-3 mode=INCLUDE include=[192.0.2.4/30]
	// allowed 192.0.2.12/30
	//   authorized by tenant-b/port-2 mode=EXCLUDE exclude=[192.0.2.8/30]
}

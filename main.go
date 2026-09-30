// fwlab：防火墙规则遮蔽/冲突分析 + 合成包反例 + 离散事件仿真。
// 所有包、队列、链路只存在于整数 tick 的离散事件模型中，不产生真实网络流量。
package main

import (
	"flag"
	"fmt"
	"net/netip"
	"os"
)

func mustPrefix(s string) netip.Prefix {
	return netip.MustParsePrefix(s).Masked()
}

// demoRules 是内置演示规则集，覆盖：单规则遮蔽、多规则联合遮蔽、
// 部分重叠、动作冲突、IPv4/IPv6 域隔离。
func demoRules() []Rule {
	return []Rule{
		{Name: "web", Prefix: mustPrefix("10.0.0.0/8"), Protos: ProtoTCP, PortMin: 80, PortMax: 80, Action: ActionAllow},
		{Name: "web-alt", Prefix: mustPrefix("10.0.0.0/8"), Protos: ProtoTCP, PortMin: 8000, PortMax: 8080, Action: ActionAllow},
		{Name: "tcp-block", Prefix: mustPrefix("10.0.0.0/8"), Protos: ProtoTCP, PortMin: 0, PortMax: 65535, Action: ActionDeny},
		{Name: "legacy-app", Prefix: mustPrefix("10.1.2.0/24"), Protos: ProtoTCP, PortMin: 8080, PortMax: 8080, Action: ActionAllow}, // 被 tcp-block 遮蔽
		{Name: "dns", Prefix: mustPrefix("10.0.0.0/8"), Protos: ProtoUDP, PortMin: 53, PortMax: 53, Action: ActionAllow},
		{Name: "lab-low", Prefix: mustPrefix("172.16.0.0/16"), Protos: ProtoTCP, PortMin: 0, PortMax: 1023, Action: ActionDeny},
		{Name: "lab-high", Prefix: mustPrefix("172.16.0.0/16"), Protos: ProtoTCP, PortMin: 1024, PortMax: 65535, Action: ActionDeny},
		{Name: "lab-open", Prefix: mustPrefix("172.16.0.0/16"), Protos: ProtoTCP, PortMin: 0, PortMax: 65535, Action: ActionAllow}, // 被 lab-low+lab-high 联合遮蔽
		{Name: "v6-web", Prefix: mustPrefix("2001:db8::/32"), Protos: ProtoTCP, PortMin: 443, PortMax: 443, Action: ActionAllow},
		{Name: "v4-catchall", Prefix: mustPrefix("0.0.0.0/0"), Protos: ProtoAny, PortMin: 0, PortMax: 65535, Action: ActionDeny},
		{Name: "ghost", Prefix: mustPrefix("192.0.2.0/24"), Protos: ProtoUDP, PortMin: 53, PortMax: 53, Action: ActionAllow}, // 被 v4-catchall 遮蔽
		{Name: "v6-catchall", Prefix: mustPrefix("::/0"), Protos: ProtoAny, PortMin: 0, PortMax: 65535, Action: ActionDeny},
	}
}

// syntheticPackets 从分析结果生成合成包：每条可达规则一个见证包，
// 每条被遮蔽规则一个被截获示例包，依次排在间隔 arrivalGap 的 tick 上。
func syntheticPackets(rep Report, arrivalGap int) []Packet {
	var pkts []Packet
	id := 0
	for _, rr := range rep.Rules {
		var p *Packet
		if rr.Reachable {
			p = rr.Witness
		} else {
			p = rr.Captured
		}
		pkt := *p
		pkt.ID = id
		pkt.Arrival = id * arrivalGap
		pkts = append(pkts, pkt)
		id++
	}
	return pkts
}

func main() {
	rulesPath := flag.String("rules", "", "规则 JSON 文件路径（缺省使用内置演示规则集）")
	verbose := flag.Bool("v", false, "打印完整仿真事件日志")
	flag.Parse()

	rules := demoRules()
	cfg := SimConfig{ServiceTicks: 1, LinkDelay: 2, BytesPerTick: 64, QueueCapacity: 8}
	if *rulesPath != "" {
		var err error
		rules, cfg, err = LoadConfig(*rulesPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "加载配置失败:", err)
			os.Exit(1)
		}
	}
	if err := ValidateRules(rules); err != nil {
		fmt.Fprintln(os.Stderr, "规则校验失败:", err)
		os.Exit(1)
	}

	fmt.Println("== 规则集（按优先级） ==")
	v4, v6 := 0, 0
	for i, r := range rules {
		fmt.Printf("#%-2d %-12s [%s] %s\n", i, r.Name, r.Family(), r)
		if r.Family() == "ipv4" {
			v4++
		} else {
			v6++
		}
	}
	fmt.Printf("（ipv4 规则 %d 条，ipv6 规则 %d 条；两域严格隔离，互不匹配、互不遮蔽）\n\n", v4, v6)

	rep := Analyze(rules)
	fmt.Println(rep.String(rules))

	pkts := syntheticPackets(rep, 3)
	fmt.Printf("== 离散事件仿真 ==\n合成包 %d 个，service=%d tick，linkDelay=%d tick，速率=%d B/tick，队列容量=%d\n",
		len(pkts), cfg.ServiceTicks, cfg.LinkDelay, cfg.BytesPerTick, cfg.QueueCapacity)
	sim := NewSim(cfg, rules)
	stats := sim.Run(pkts)
	if *verbose {
		for _, e := range sim.Log {
			fmt.Println(e)
		}
	}
	fmt.Println("-- 每包结果 --")
	mismatch := 0
	for _, e := range sim.Log {
		if e.Event != "match" {
			continue
		}
		fmt.Println(e)
	}
	fmt.Println("-- 规则命中计数 --")
	for i, r := range rules {
		fmt.Printf("#%-2d %-12s hits=%d\n", i, r.Name, stats.Hits[i])
	}
	fmt.Printf("默认拒绝=%d 交付=%d 丢弃=%d 峰值队列=%d\n", stats.Default, stats.Delivered, stats.Dropped, stats.MaxQueue)

	// 一致性校验：仿真命中结果必须与分析预测一致。
	for i, rr := range rep.Rules {
		pkt := pkts[i]
		got := Match(rules, pkt)
		want := rr.RuleIndex
		if !rr.Reachable {
			want = rr.CoveredBy[0]
		}
		if got != want {
			mismatch++
			fmt.Printf("!! 不一致: pkt#%d 分析预测命中 #%d，实际命中 #%d\n", pkt.ID, want, got)
		}
	}
	if mismatch == 0 {
		fmt.Println("一致性校验通过：所有合成包的仿真命中结果与分析预测一致。")
	} else {
		os.Exit(1)
	}
}

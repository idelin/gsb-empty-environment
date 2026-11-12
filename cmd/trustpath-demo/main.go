// 脚本解析器演示：跨区别名、同名不同类型、途中超时三种场景，
// 打印组合器给出的完整信任路径与每跳（查询类型/权威区/有效期）。
package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	netsec "example.org/netsec/q0141"
)

func main() {
	demoCrossZone()
	demoSameNameDifferentTypes()
	demoMidChainTimeout()
}

func demoCrossZone() {
	fmt.Println("== 场景 1：跨区别名，完整信任路径 ==")
	r := netsec.NewScriptedResolver()
	r.SetReply("www.example.com", netsec.TypeA, &netsec.Message{
		QueryName: "www.example.com", QueryType: netsec.TypeA, Rcode: netsec.RcodeNoError,
		Zone:   "example.com",
		Answer: []netsec.RR{{Name: "www.example.com", Type: netsec.TypeCNAME, TTL: 300 * time.Second, Target: "edge.cdn.example.net"}},
	})
	r.SetReply("edge.cdn.example.net", netsec.TypeA, &netsec.Message{
		QueryName: "edge.cdn.example.net", QueryType: netsec.TypeA, Rcode: netsec.RcodeNoError,
		Zone:   "cdn.example.net",
		Answer: []netsec.RR{{Name: "edge.cdn.example.net", Type: netsec.TypeA, TTL: 60 * time.Second, Data: "203.0.113.7"}},
	})

	res, err := netsec.NewComposer(r).Resolve("www.example.com", netsec.TypeA)
	if err != nil {
		panic(err)
	}
	fmt.Printf("信任路径: %s\n", strings.Join(res.TrustPath(), " -> "))
	printHops(res.Hops)
	fmt.Printf("末端 A: %s，最短有效期 %v\n\n", res.Records[0].Data, res.MinTTL)
}

func demoSameNameDifferentTypes() {
	fmt.Println("== 场景 2：同名不同类型，NODATA 否定只覆盖 (末端, A) ==")
	r := netsec.NewScriptedResolver()
	for _, qtype := range []uint16{netsec.TypeA, netsec.TypeAAAA} {
		r.SetReply("alias.example.com", qtype, &netsec.Message{
			QueryName: "alias.example.com", QueryType: qtype, Rcode: netsec.RcodeNoError,
			Zone:   "example.com",
			Answer: []netsec.RR{{Name: "alias.example.com", Type: netsec.TypeCNAME, TTL: 300 * time.Second, Target: "target.example.org"}},
		})
	}
	r.SetReply("target.example.org", netsec.TypeA, &netsec.Message{
		QueryName: "target.example.org", QueryType: netsec.TypeA, Rcode: netsec.RcodeNoError,
		Zone:      "example.org",
		Authority: []netsec.RR{{Name: "example.org", Type: netsec.TypeSOA, TTL: 90 * time.Second}},
	})
	r.SetReply("target.example.org", netsec.TypeAAAA, &netsec.Message{
		QueryName: "target.example.org", QueryType: netsec.TypeAAAA, Rcode: netsec.RcodeNoError,
		Zone:   "example.org",
		Answer: []netsec.RR{{Name: "target.example.org", Type: netsec.TypeAAAA, TTL: 300 * time.Second, Data: "2001:db8::7"}},
	})

	c := netsec.NewComposer(r)
	resA, err := c.Resolve("alias.example.com", netsec.TypeA)
	if err != nil {
		panic(err)
	}
	fmt.Printf("A 查询: 末端否定 NXDOMAIN=%v 名字=%s 类型=%d\n",
		resA.Negative.NXDOMAIN, resA.Negative.Name, resA.Negative.Type)
	fmt.Printf("  否定覆盖起始名? %v；覆盖末端 AAAA? %v；覆盖末端 A? %v\n",
		resA.Negative.Covers("alias.example.com", netsec.TypeA),
		resA.Negative.Covers("target.example.org", netsec.TypeAAAA),
		resA.Negative.Covers("target.example.org", netsec.TypeA))

	resAAAA, err := c.Resolve("alias.example.com", netsec.TypeAAAA)
	if err != nil {
		panic(err)
	}
	fmt.Printf("AAAA 查询独立成功: %s，路径 %s\n\n",
		resAAAA.Records[0].Data, strings.Join(resAAAA.TrustPath(), " -> "))
}

func demoMidChainTimeout() {
	fmt.Println("== 场景 3：途中超时，返回部分信任路径且不编造否定 ==")
	r := netsec.NewScriptedResolver()
	r.SetReply("start.example.com", netsec.TypeA, &netsec.Message{
		QueryName: "start.example.com", QueryType: netsec.TypeA, Rcode: netsec.RcodeNoError,
		Zone:   "example.com",
		Answer: []netsec.RR{{Name: "start.example.com", Type: netsec.TypeCNAME, TTL: 300 * time.Second, Target: "slow.example.net"}},
	})
	r.SetTimeout("slow.example.net", netsec.TypeA)

	_, err := netsec.NewComposer(r).Resolve("start.example.com", netsec.TypeA)
	var incomplete *netsec.IncompleteChainError
	if !errors.As(err, &incomplete) {
		panic(err)
	}
	fmt.Printf("失败原因: %v\n", incomplete.Err)
	fmt.Printf("部分信任路径: %s\n", strings.Join(incomplete.Partial.Names(), " -> "))
	printHops(incomplete.Partial.Hops)
	fmt.Println("（未产生任何 NXDOMAIN/NODATA 否定结论）")
}

func printHops(hops []netsec.Hop) {
	for i, hop := range hops {
		kind := "别名 -> " + hop.AliasTarget
		if hop.Negative != nil {
			if hop.Negative.NXDOMAIN {
				kind = "末端 NXDOMAIN（覆盖末端及其下属）"
			} else {
				kind = fmt.Sprintf("末端 NODATA（仅类型 %d）", hop.Negative.Type)
			}
		} else if hop.AliasTarget == "" {
			kind = "末端肯定数据"
		}
		fmt.Printf("  跳 %d: 查询 (%s, 类型%d) 权威区=%s 有效期=%v -> %s\n",
			i+1, hop.QueryName, hop.QueryType, hop.Zone, hop.TTL, kind)
	}
}

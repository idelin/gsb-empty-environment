// 分层仲裁多数派决策引擎 —— 运行样例
//
// 场景：IDC-A(权重2, 主中心) / IDC-B(权重1) / IDC-C(权重1)
//  1. 正常拓扑：A+B 达成全局多数派
//  2. A 失联：自动降级，B+C 达成临时多数派
//  3. A 恢复：出示更高纪元重新接管，无状态冲突
package main

import (
	"encoding/hex"
	"fmt"
	"log"

	"hierquorum/quorum"
)

var (
	regionKeys = map[string][]byte{
		"IDC-A": []byte("key-idc-a"),
		"IDC-B": []byte("key-idc-b"),
		"IDC-C": []byte("key-idc-c"),
	}
	evidenceKey = []byte("arbiter-evidence-key")
)

func printDecision(a *quorum.Arbiter, d *quorum.Decision) {
	snapshotRegions = a.Snapshot()
	fmt.Printf("  决议: epoch=%d value=%q mode=%s 票数权重=%d\n",
		d.Epoch, d.Value, d.Mode, d.TotalWeight(snapshotRegions))
	for _, v := range d.Votes {
		fmt.Printf("    投票 %-5s 签名=%s...\n", v.RegionID, hex.EncodeToString(v.Signature)[:16])
	}
	fmt.Printf("    证据=%s...\n", hex.EncodeToString(d.Evidence)[:16])
}

var snapshotRegions map[string]quorum.Region

func verify(a *quorum.Arbiter, d *quorum.Decision) {
	if err := quorum.Verify(d, snapshotRegions, regionKeys, evidenceKey); err != nil {
		log.Fatalf("证据验证失败: %v", err)
	}
	fmt.Println("  证据验证: OK")
}

func main() {
	a := quorum.NewArbiter(evidenceKey)
	for _, r := range []quorum.Region{
		{ID: "IDC-A", Weight: 2, LocalThreshold: 2, Primary: true},
		{ID: "IDC-B", Weight: 1, LocalThreshold: 1},
		{ID: "IDC-C", Weight: 1, LocalThreshold: 1},
	} {
		if err := a.AddRegion(r, regionKeys[r.ID]); err != nil {
			log.Fatal(err)
		}
	}
	for id, acks := range map[string]int{"IDC-A": 2, "IDC-B": 1, "IDC-C": 1} {
		if err := a.ReportLocalAcks(id, acks); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Println("== 1. 正常拓扑 (总权重4, 门限3) ==")
	d1, err := a.Propose(1, "set x=1", []string{"IDC-A", "IDC-B"})
	if err != nil {
		log.Fatal(err)
	}
	printDecision(a, d1)
	verify(a, d1)

	fmt.Println("\n== 2. IDC-A 失联 → 降级仲裁拓扑 (参与权重2, 门限2) ==")
	if err := a.SetReachable("IDC-A", false); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  当前拓扑: %s\n", a.Mode())
	d2, err := a.Propose(2, "set x=2 (degraded)", []string{"IDC-B", "IDC-C"})
	if err != nil {
		log.Fatal(err)
	}
	printDecision(a, d2)
	verify(a, d2)

	fmt.Println("\n== 3. IDC-A 恢复，出示更高纪元重新接管 ==")
	if err := a.SetReachable("IDC-A", true); err != nil {
		log.Fatal(err)
	}
	if err := a.Takeover(2); err != nil {
		fmt.Printf("  Takeover(epoch=2) 被拒绝: %v\n", err)
	}
	if err := a.Takeover(3); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Takeover(epoch=3) 成功, 当前拓扑: %s\n", a.Mode())
	d3, err := a.Propose(4, "set x=3 (recovered)", []string{"IDC-A", "IDC-C"})
	if err != nil {
		log.Fatal(err)
	}
	printDecision(a, d3)
	verify(a, d3)

	fmt.Println("\n== 4. 围栏检查：旧纪元提案被拒绝 ==")
	if _, err := a.Propose(2, "stale write", []string{"IDC-A", "IDC-B"}); err != nil {
		fmt.Printf("  Propose(epoch=2) 被拒绝: %v\n", err)
	}
	fmt.Println("\n样例运行完成，无状态冲突。")
}

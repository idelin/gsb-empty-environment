// 运行样例:分层仲裁多数派决策引擎全流程演示。
// 场景:IDC-A(权重2,主中心)、IDC-B/IDC-C(权重各1)。
// 1. 正常拓扑仲裁 -> 2. A 失联,B+C 降级临时多数派 ->
// 3. A 恢复,出示更高纪元重新接管 -> 4. 审计全部签名证据。
package main

import (
	"fmt"
	"log"

	"hierarchical-quorum/quorum"
)

func main() {
	cfg := quorum.Config{
		Primary: "IDC-A",
		Regions: []quorum.Region{
			{ID: "IDC-A", Weight: 2, LocalNodes: 3, LocalThreshold: 2, Key: []byte("key-idc-a")},
			{ID: "IDC-B", Weight: 1, LocalNodes: 3, LocalThreshold: 2, Key: []byte("key-idc-b")},
			{ID: "IDC-C", Weight: 1, LocalNodes: 3, LocalThreshold: 2, Key: []byte("key-idc-c")},
		},
	}
	a, err := quorum.NewArbiter(cfg)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("=== 1. 正常拓扑:全局权重 4,多数派门限 3 ===")
	decide(a, cfg, "set x=1")

	fmt.Println("\n=== 2. IDC-A 失联(跨区网络闪断),进入降级仲裁拓扑 ===")
	must(a.SetReachable("IDC-A", false))
	fmt.Printf("当前拓扑: %v (B+C 临时多数派门限 2)\n", a.Mode())
	decide(a, cfg, "set y=2")

	fmt.Println("\n=== 3. IDC-A 恢复,出示更高纪元重新接管 ===")
	must(a.SetReachable("IDC-A", true))
	stale := a.Epoch()
	if err := a.RestorePrimary(stale); err != nil {
		fmt.Printf("拒绝过期接管(epoch=%d): %v\n", stale, err)
	}
	must(a.RestorePrimary(stale + 1))
	fmt.Printf("接管成功,新纪元=%d,拓扑=%v\n", a.Epoch(), a.Mode())
	decide(a, cfg, "set z=3")

	fmt.Println("\n=== 4. 审计:已提交决议日志(纪元单调,证据可独立验证)===")
	for _, d := range a.Committed() {
		fmt.Printf("epoch=%d mode=%-8s proposal=%-10s weight=%d/%d 证据投票=%d 份\n",
			d.Epoch, d.Mode, d.Proposal, d.Weight, d.Quorum, len(d.Votes))
	}
}

func decide(a *quorum.Arbiter, cfg quorum.Config, proposal string) {
	d, err := a.Propose(proposal)
	if err != nil {
		fmt.Printf("提案 %q 未通过: %v\n", proposal, err)
		return
	}
	fmt.Printf("提案 %q 通过: epoch=%d mode=%v weight=%d quorum=%d\n",
		proposal, d.Epoch, d.Mode, d.Weight, d.Quorum)
	for _, v := range d.Votes {
		fmt.Printf("  证据: %s epoch=%d local=%d sig=%.16s...\n",
			v.RegionID, v.Epoch, v.LocalVotes, v.Signature)
	}
	if err := quorum.VerifyDecision(cfg, d); err != nil {
		log.Fatalf("证据验证失败: %v", err)
	}
	fmt.Println("  签名证据独立验证: OK")
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

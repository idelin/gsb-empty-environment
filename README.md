# 扩展公钥与子私钥的恢复闭包

分层确定性钱包允许导出扩展公钥给对账或监控服务，但只要攻击者再拿到该公钥下任意一个非硬化子私钥，就能反推出父私钥，进而得到整棵子树。Go钱包导出服务需要可导入的暴露审查包，输入已导出的材料列表（扩展公钥、扩展私钥或叶私钥，各带派生路径，路径片段带硬化标记）和钱包树结构：导出叶私钥或扩展私钥时，其全部后代的私钥视为暴露；持有某节点的扩展公钥（含链码）与它任一非硬化子节点的私钥，则该节点的私钥暴露，并向上继续传导：该节点若又是其父节点的非硬化子节点，且父节点的扩展公钥也已导出，则父节点私钥同样暴露；硬化子节点的私钥不能反推父节点，也不能由父扩展公钥得到；输出全部暴露的私钥路径集合，以及导致每项暴露的一条推导链；新增导出若使原先安全的路径暴露，整个导出请求拒绝。用扩展公钥加非硬化子私钥、硬化子私钥、逐级向上传导、仅导出扩展公钥和后代暴露验证。

## API

`Audit` 接受 `AuditRequest`（结构体或指针），也接受 `材料列表, 钱包树` 两个参数。路径使用 `m/0/1'/2` 形式，`'`、`h`、`H` 和 Unicode prime 都识别为硬化标记。

```go
package main

import (
	"fmt"

	netsec "example.org/netsec/q0825"
)

func main() {
	tree := &netsec.WalletTree{Root: &netsec.WalletNode{
		Path: "m",
		Children: []*netsec.WalletNode{
			{Path: "m/0", Children: []*netsec.WalletNode{
				{Path: "m/0/1"},
				{Path: "m/0/2'"},
			}},
			{Path: "m/1"},
		},
	}}

	report, err := netsec.Audit(netsec.AuditRequest{
		Materials: []netsec.Export{
			{Kind: netsec.XPub, Path: "m", Key: "xpub6..."},
			{Kind: netsec.LeafPriv, Path: "m/0/1", Key: "L1..."},
		},
		Tree: tree,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(report.ExposedPaths)
	for _, exposure := range report.Exposed {
		fmt.Printf("%s: %s\n", exposure.Path, exposure.Reason)
	}
}
```

审计结果包含 `Exposed`（每项带 `Path`、`Chain` 和结构化 `Steps`）、`ExposedPaths`、`Chains` 以及可直接 JSON 导入导出的请求/报告结构。

## 增量导出

`AuditSession` 保存已接受的基线。`Apply` 在同一事务中审查整批新增材料；只要出现一条原先安全的路径，就返回 `*UnsafeExportError`（可用 `errors.Is(err, ErrPreviouslySafeExposed)` 判断），且不提交该批次的任何材料。`DryRun`/`ReviewExport` 会返回候选报告但同样给出拒绝错误。

```go
session, err := netsec.NewAuditSession(
	[]netsec.Export{{Kind: netsec.XPub, Path: "m"}},
	tree,
)
if err != nil {
	panic(err)
}

_, err = session.Apply([]netsec.Export{
	{Kind: netsec.XPub, Path: "m/0"},
	{Kind: netsec.LeafPriv, Path: "m/0/1"},
})
// err 匹配 ErrPreviouslySafeExposed；两项新增都不会被提交。
```

## 规则

- 直接导出 `xprv`：该节点及其全部后代私钥暴露；继续沿硬化边向下派生。
- 直接导出叶私钥：该节点及其后代按题目要求视为暴露。
- 直接导出 `xpub`：只暴露公钥能力；`xpub` 可沿非硬化边继续派生子孙 `xpub`。
- `xpub(父)` 加任一非硬化子私钥：恢复父 `xprv`，再暴露父节点整棵子树。
- 恢复出的父 `xprv` 可作为上一层的子私钥继续反推；遇到硬化边立即停止。
- 硬化子私钥不能反推父节点，父 `xpub` 也不能派生硬化子公钥/私钥。

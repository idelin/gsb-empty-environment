# 版本撤回安全回退

`netsec` 根据更新服务下发的版本清单，为 Go 更新宿主选择安全迁移目标。
清单之外不存在任何隐式迁移边。

## 选择规则

- 当前版本可以作为迁移起点，即使它已经被撤回；目标版本不能已撤回。
- 路径上的每个中间版本和目标版本都必须通过签名校验并满足宿主机兼容条件。
- 路径上每一步的安全级别都不能下降。
- 在满足条件的可达版本中，选择迁移总成本最低者。
- 成本相同时依次优先：安全级别更高、跳数更少、版本 ID 字典序更小。
- 清单最多包含 30 个节点。

`Select` 会把仍然安全的当前版本作为零成本候选；`SelectUpgrade` 则要求
必须迁移到另一个版本。失败时返回 `Selection.Rejections`，其中包含撤回、
签名、兼容性、级别下降、路径被阻断、不可达或成本非最低等结构化原因。
可用 `errors.Is(err, netsec.ErrNoSafeVersion)` 和
`errors.Is(err, netsec.ErrInvalidManifest)` 区分失败类别。

## 用法

```go
manifest := netsec.Manifest{
	Nodes: []netsec.Node{
		{Version: "1.2.0", Level: netsec.UnsignedLevel(2), Revoked: true},
		{Version: "1.3.0", Level: netsec.UnsignedLevel(2), Revoked: true},
		{
			Version: "1.4.0",
			Level:   netsec.UnsignedLevel(4),
			Compatibility: netsec.Compatibility{
				Requirements: map[string]string{"os": "linux"},
			},
		},
		{Version: "2.0.0", Level: netsec.UnsignedLevel(5), Revoked: true},
		{Version: "2.1.0", Level: netsec.UnsignedLevel(5)},
	},
	Edges: []netsec.Edge{
		{From: "1.2.0", To: "1.3.0", Cost: 1},
		{From: "1.3.0", To: "1.4.0", Cost: 1},
		{From: "1.4.0", To: "2.1.0", Cost: 2},
		{From: "1.3.0", To: "2.0.0", Cost: 1},
		{From: "2.0.0", To: "2.1.0", Cost: 1},
	},
}

selection, err := netsec.SelectUpgrade(
	manifest,
	"1.3.0",
	netsec.Environment{"os": "linux"},
)
if err != nil {
	log.Fatal(err)
}
// selection.Path == ["1.3.0", "1.4.0", "2.1.0"]
// selection.Cost == 3
```

生产清单应为安全级别生成 Ed25519 签名，并配置 `Manifest.VerifyLevel`：

```go
manifest.Nodes[4].Level = netsec.SignLevel(
	"2.1.0", 5, "release-2026", privateKey,
)
manifest.VerifyLevel = netsec.Ed25519Verifier(map[string]ed25519.PublicKey{
	"release-2026": publicKey,
})
```

未配置 `VerifyLevel` 时，签名被视为已由上层信任链验证，不会在包内检查。

## 测试

`select_test.go` 对 4 节点小图穷举全部合法边子集与撤回组合，并与独立的
简单路径穷举结果对拍；测试覆盖被撤回的中间节点、零成本环、安全级别下降、
兼容性阻断、签名篡改和 30 节点上限。
